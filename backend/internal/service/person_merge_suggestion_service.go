package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"gorm.io/gorm"
)

const (
	personMergeSuggestionStateKey = "people.merge_suggestions.state"
)

type PersonMergeSuggestionService interface {
	GetTask() *model.PersonMergeSuggestionTask
	GetStats() (*model.PersonMergeSuggestionStatsResponse, error)
	GetBackgroundLogs() []string
	Pause() error
	Resume() error
	Rebuild() error
	MarkDirty(reason string) error
	RunBackgroundSlice() error
	ExcludeCandidates(suggestionID uint, candidateIDs []uint) error
	ApplySuggestion(suggestionID uint, candidateIDs []uint) error
	ListPending(page, pageSize int) ([]model.PersonMergeSuggestionResponse, int64, error)
	GetPendingByID(id uint) (*model.PersonMergeSuggestionResponse, error)
}

type personMergeSuggestionState struct {
	Paused         bool      `json:"paused"`
	Dirty          bool      `json:"dirty"`
	CursorTargetID uint      `json:"cursor_target_id"`
	LastRunAt      time.Time `json:"last_run_at,omitempty"`
}

type personMergeSuggestionService struct {
	db                  *gorm.DB
	photoRepo           repository.PhotoRepository
	faceRepo            repository.FaceRepository
	personRepo          repository.PersonRepository
	jobRepo             repository.PeopleJobRepository
	cannotLinkRepo      repository.CannotLinkRepository
	mergeSuggestionRepo repository.PersonMergeSuggestionRepository
	configService       ConfigService
	config              *config.Config

	mu             sync.RWMutex
	task           *model.PersonMergeSuggestionTask
	state          personMergeSuggestionState
	backgroundLogs []string
	annIdx         *annIndex
	annDirty       bool      // index is stale; rebuild when cooldown passes
	annBuiltAt     time.Time // when index was last successfully built
}

type mergeSuggestionCandidate struct {
	targetID     uint
	candidateID  uint
	score        float64
	targetPerson *model.Person
}

func NewPersonMergeSuggestionService(
	db *gorm.DB,
	photoRepo repository.PhotoRepository,
	faceRepo repository.FaceRepository,
	personRepo repository.PersonRepository,
	jobRepo repository.PeopleJobRepository,
	cannotLinkRepo repository.CannotLinkRepository,
	mergeSuggestionRepo repository.PersonMergeSuggestionRepository,
	configService ConfigService,
	cfg *config.Config,
) PersonMergeSuggestionService {
	svc := &personMergeSuggestionService{
		db:                  db,
		photoRepo:           photoRepo,
		faceRepo:            faceRepo,
		personRepo:          personRepo,
		jobRepo:             jobRepo,
		cannotLinkRepo:      cannotLinkRepo,
		mergeSuggestionRepo: mergeSuggestionRepo,
		configService:       configService,
		config:              cfg,
		task: &model.PersonMergeSuggestionTask{
			Status:         model.TaskStatusIdle,
			CurrentMessage: "等待巡检",
		},
		backgroundLogs: make([]string, 0, 32),
	}
	_ = svc.loadState()
	return svc
}

func (s *personMergeSuggestionService) GetTask() *model.PersonMergeSuggestionTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePersonMergeSuggestionTask(s.task)
}

func (s *personMergeSuggestionService) GetStats() (*model.PersonMergeSuggestionStatsResponse, error) {
	resp := &model.PersonMergeSuggestionStatsResponse{}

	rows, err := s.db.Model(&model.PersonMergeSuggestion{}).
		Select("status, COUNT(*) as count").
		Group("status").
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		resp.Total += count
		switch status {
		case model.PersonMergeSuggestionStatusPending:
			resp.Pending = count
		case model.PersonMergeSuggestionStatusApplied:
			resp.Applied = count
		case model.PersonMergeSuggestionStatusDismissed:
			resp.Dismissed = count
		case model.PersonMergeSuggestionStatusObsolete:
			resp.Obsolete = count
		}
	}

	itemRows, err := s.db.Model(&model.PersonMergeSuggestionItem{}).
		Select("status, COUNT(*) as count").
		Group("status").
		Rows()
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()

	for itemRows.Next() {
		var status string
		var count int64
		if err := itemRows.Scan(&status, &count); err != nil {
			return nil, err
		}
		switch status {
		case model.PersonMergeSuggestionItemStatusPending:
			resp.PendingItems = count
		case model.PersonMergeSuggestionItemStatusExcluded:
			resp.ExcludedItems = count
		case model.PersonMergeSuggestionItemStatusMerged:
			resp.MergedItems = count
		}
	}

	return resp, nil
}

func (s *personMergeSuggestionService) GetBackgroundLogs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	logs := make([]string, len(s.backgroundLogs))
	copy(logs, s.backgroundLogs)
	return logs
}

func (s *personMergeSuggestionService) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state.Paused = true
	now := time.Now()
	s.task.Status = model.TaskStatusPaused
	s.task.CurrentMessage = "已暂停"
	s.task.StoppedAt = &now
	s.appendBackgroundLogLocked("人物合并建议后台任务已暂停")
	return s.saveStateLocked()
}

func (s *personMergeSuggestionService) Resume() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state.Paused = false
	s.task.Status = model.TaskStatusIdle
	s.task.CurrentMessage = "等待巡检"
	s.task.StoppedAt = nil
	s.appendBackgroundLogLocked("人物合并建议后台任务已恢复")
	return s.saveStateLocked()
}

func (s *personMergeSuggestionService) Rebuild() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.PersonMergeSuggestion{}).
			Where("status = ?", model.PersonMergeSuggestionStatusPending).
			Update("status", model.PersonMergeSuggestionStatusObsolete).Error; err != nil {
			return err
		}
		return tx.Model(&model.PersonMergeSuggestionItem{}).
			Where("status = ?", model.PersonMergeSuggestionItemStatusPending).
			Update("status", model.PersonMergeSuggestionItemStatusObsolete).Error
	}); err != nil {
		return err
	}

	s.state.Dirty = true
	s.state.CursorTargetID = 0
	s.annIdx = nil
	s.annDirty = false
	s.task.Status = model.TaskStatusIdle
	s.task.CurrentMessage = "等待重建巡检"
	s.appendBackgroundLogLocked("人物合并建议已标记重建")
	return s.saveStateLocked()
}

func (s *personMergeSuggestionService) MarkDirty(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state.Dirty = true
	s.state.CursorTargetID = 0
	s.annDirty = true
	if reason != "" {
		s.appendBackgroundLogLocked("合并建议待更新: " + reason)
	}
	return s.saveStateLocked()
}

func (s *personMergeSuggestionService) RunBackgroundSlice() error {
	// 快速检查是否暂停（持锁）
	s.mu.Lock()
	if s.state.Paused {
		s.task.Status = model.TaskStatusPaused
		s.task.CurrentMessage = "已暂停"
		s.mu.Unlock()
		return nil
	}

	// 冷却时间检查：避免频繁执行繁重的相似度计算
	// 注意：如果有脏数据（Dirty=true），则跳过冷却检查，立即处理
	cooldown := 300 // 默认 5 分钟
	if s.config != nil && s.config.People.MergeSuggestionCooldownSeconds > 0 {
		cooldown = s.config.People.MergeSuggestionCooldownSeconds
	}
	if !s.state.Dirty && !s.state.LastRunAt.IsZero() && time.Since(s.state.LastRunAt) < time.Duration(cooldown)*time.Second {
		s.mu.Unlock()
		return nil // 还在冷却期内且无脏数据，跳过
	}

	// 读取状态后释放锁
	cursor := s.state.CursorTargetID
	s.mu.Unlock()

	// 耗时操作在锁外执行
	targets, err := s.listSuggestionTargets()
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		s.mu.Lock()
		s.finishSliceLocked(time.Now(), 0, "没有可巡检的人物目标")
		s.mu.Unlock()
		return nil
	}

	batchSize, err := s.currentBatchSize()
	if err != nil {
		return err
	}
	selected := selectNextSuggestionTargets(targets, cursor, batchSize)
	if len(selected) == 0 {
		s.mu.Lock()
		now := time.Now()
		s.state.CursorTargetID = 0
		s.state.Dirty = false
		s.state.LastRunAt = now
		s.task.Status = model.TaskStatusIdle
		s.task.CurrentMessage = "本轮巡检完成"
		s.task.StoppedAt = &now
		err := s.saveStateLocked()
		s.mu.Unlock()
		return err
	}

	// 最耗时的操作：计算相似度
	assignments, err := s.buildAssignments(selected)
	if err != nil {
		return err
	}

	// 写入数据库和更新状态（持锁）
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.task.StartedAt == nil {
		s.task.StartedAt = &now
	}
	s.task.Status = model.TaskStatusRunning
	s.task.CurrentMessage = "保存合并建议"

	processedPairs := 0
	for _, target := range selected {
		items := assignments[target.ID]
		if err := s.mergeSuggestionRepo.ReplacePendingForTarget(target.ID, target.Category, items); err != nil {
			return err
		}
		processedPairs += len(items)
	}

	s.task.ProcessedPairs += int64(processedPairs)
	lastTargetID := selected[len(selected)-1].ID
	if lastTargetID >= targets[len(targets)-1].ID {
		s.state.CursorTargetID = 0
		s.state.Dirty = false
	} else {
		s.state.CursorTargetID = lastTargetID
	}
	s.finishSliceLocked(now, processedPairs, fmt.Sprintf("完成 %d 个目标人物巡检", len(selected)))
	return nil
}

func (s *personMergeSuggestionService) ExcludeCandidates(suggestionID uint, candidateIDs []uint) error {
	if len(candidateIDs) == 0 {
		return nil
	}

	suggestion, err := s.mergeSuggestionRepo.GetByID(suggestionID)
	if err != nil {
		return err
	}
	if suggestion == nil {
		return fmt.Errorf("merge suggestion %d not found", suggestionID)
	}

	for _, candidateID := range candidateIDs {
		if candidateID == 0 {
			continue
		}
		if err := s.cannotLinkRepo.Create(suggestion.TargetPersonID, candidateID); err != nil {
			return err
		}
	}

	if err := s.mergeSuggestionRepo.MarkItemsStatus(suggestionID, candidateIDs, model.PersonMergeSuggestionItemStatusExcluded); err != nil {
		return err
	}
	return s.MarkDirty("exclude merge suggestion candidates")
}

func (s *personMergeSuggestionService) ApplySuggestion(suggestionID uint, candidateIDs []uint) error {
	if len(candidateIDs) == 0 {
		return nil
	}

	suggestion, err := s.mergeSuggestionRepo.GetByID(suggestionID)
	if err != nil {
		return err
	}
	if suggestion == nil {
		return fmt.Errorf("merge suggestion %d not found", suggestionID)
	}

	if _, err := s.personRepo.MergeInto(suggestion.TargetPersonID, candidateIDs); err != nil {
		return err
	}
	if err := s.mergeSuggestionRepo.MarkItemsStatus(suggestionID, candidateIDs, model.PersonMergeSuggestionItemStatusMerged); err != nil {
		return err
	}
	return s.MarkDirty("apply merge suggestion candidates")
}

func (s *personMergeSuggestionService) ListPending(page, pageSize int) ([]model.PersonMergeSuggestionResponse, int64, error) {
	suggestions, total, err := s.mergeSuggestionRepo.ListPending(page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	items := make([]*model.PersonMergeSuggestionItem, 0)
	for _, suggestion := range suggestions {
		suggestionItems, itemErr := s.mergeSuggestionRepo.GetItems(suggestion.ID, model.PersonMergeSuggestionItemStatusPending)
		if itemErr != nil {
			return nil, 0, itemErr
		}
		items = append(items, suggestionItems...)
	}
	return s.buildSuggestionResponses(suggestions, items), total, nil
}

func (s *personMergeSuggestionService) GetPendingByID(id uint) (*model.PersonMergeSuggestionResponse, error) {
	suggestion, err := s.mergeSuggestionRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if suggestion == nil || suggestion.Status != model.PersonMergeSuggestionStatusPending {
		return nil, nil
	}
	items, err := s.mergeSuggestionRepo.GetItems(id, model.PersonMergeSuggestionItemStatusPending)
	if err != nil {
		return nil, err
	}
	responses := s.buildSuggestionResponses([]*model.PersonMergeSuggestion{suggestion}, items)
	if len(responses) == 0 {
		return nil, nil
	}
	return &responses[0], nil
}

func (s *personMergeSuggestionService) listSuggestionTargets() ([]*model.Person, error) {
	people, err := s.personRepo.ListAll()
	if err != nil {
		return nil, err
	}

	targets := make([]*model.Person, 0, len(people))
	for _, person := range people {
		if person == nil {
			continue
		}
		if person.Category != model.PersonCategoryFamily && person.Category != model.PersonCategoryFriend {
			continue
		}
		if person.FaceCount <= 0 {
			continue
		}
		targets = append(targets, person)
	}

	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	return targets, nil
}

func (s *personMergeSuggestionService) currentBatchSize() (int, error) {
	batchSize := 100
	if s.config != nil && s.config.People.MergeSuggestionBatchSize > 0 {
		batchSize = s.config.People.MergeSuggestionBatchSize
	}

	jobStats, err := s.jobRepo.GetStats()
	if err != nil {
		return 0, err
	}
	pendingFaceStats, err := s.faceRepo.GetPendingStats()
	if err != nil {
		return 0, err
	}
	if jobStats.Pending+jobStats.Queued+jobStats.Processing > 0 || pendingFaceStats.Total > 0 {
		if batchSize > 1 {
			return 1, nil
		}
	}
	if batchSize <= 0 {
		return 1, nil
	}
	return batchSize, nil
}

func (s *personMergeSuggestionService) mergeSuggestionThreshold() float64 {
	if s.config != nil && s.config.People.MergeSuggestionThreshold > 0 {
		return s.config.People.MergeSuggestionThreshold
	}
	return 0.62
}

func (s *personMergeSuggestionService) buildAssignments(targets []*model.Person) (map[uint][]model.PersonMergeSuggestionItem, error) {
	// Phase 1: ensure ANN index is ready (lazy build, cached across slices).
	idx, err := s.ensureANNIndex()
	if err != nil {
		return nil, err
	}

	// Phase 2: load cannot-link constraints.
	cannotLinkCache := make(map[uint]map[uint]bool)
	allCannotLinks, err := s.cannotLinkRepo.ListAll()
	if err != nil {
		return nil, err
	}
	for _, cl := range allCannotLinks {
		if cannotLinkCache[cl.PersonIDA] == nil {
			cannotLinkCache[cl.PersonIDA] = make(map[uint]bool)
		}
		cannotLinkCache[cl.PersonIDA][cl.PersonIDB] = true
		if cannotLinkCache[cl.PersonIDB] == nil {
			cannotLinkCache[cl.PersonIDB] = make(map[uint]bool)
		}
		cannotLinkCache[cl.PersonIDB][cl.PersonIDA] = true
	}

	// Phase 3: ANN candidate generation — serialized because hnsw.Graph is not thread-safe.
	candidatesByTarget := make(map[uint]map[uint]struct{}, len(targets))
	for _, target := range targets {
		if target == nil {
			continue
		}
		targetProtos := idx.personProtos[target.ID]
		if len(targetProtos) == 0 {
			continue
		}
		candidatesByTarget[target.ID] = idx.annCandidates(target.ID, targetProtos, annSearchK)
	}

	// Phase 4: exact re-scoring on shortlisted candidates — parallelized with 4 workers.
	bestByCandidate := make(map[uint]mergeSuggestionCandidate)
	bestMutex := sync.Mutex{}
	threshold := s.mergeSuggestionThreshold()

	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for _, target := range targets {
		if target == nil {
			continue
		}
		candidates := candidatesByTarget[target.ID]
		if len(candidates) == 0 {
			continue
		}
		targetProtos := idx.personProtos[target.ID]

		wg.Add(1)
		go func(tgt *model.Person, tgtEmb []faceWithEmbedding, candIDs map[uint]struct{}) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			for candidateID := range candIDs {
				candidateEmbeddings := idx.personProtos[candidateID]
				if len(candidateEmbeddings) == 0 {
					continue
				}
				if cannotLinkCache[tgt.ID] != nil && cannotLinkCache[tgt.ID][candidateID] {
					continue
				}
				if cannotLinkCache[candidateID] != nil && cannotLinkCache[candidateID][tgt.ID] {
					continue
				}

				score := averageBestSuggestionSimilarity(tgtEmb, candidateEmbeddings)
				if score < threshold || score >= s.attachThreshold() {
					continue
				}

				bestMutex.Lock()
				current, exists := bestByCandidate[candidateID]
				if !exists || score > current.score || (score == current.score && tgt.ID < current.targetID) {
					bestByCandidate[candidateID] = mergeSuggestionCandidate{
						targetID:     tgt.ID,
						candidateID:  candidateID,
						score:        score,
						targetPerson: tgt,
					}
				}
				bestMutex.Unlock()
			}
		}(target, targetProtos, candidates)
	}
	wg.Wait()

	assignments := make(map[uint][]model.PersonMergeSuggestionItem, len(targets))
	for _, assignment := range bestByCandidate {
		assignments[assignment.targetID] = append(assignments[assignment.targetID], model.PersonMergeSuggestionItem{
			CandidatePersonID: assignment.candidateID,
			SimilarityScore:   assignment.score,
			Status:            model.PersonMergeSuggestionItemStatusPending,
		})
	}

	for _, target := range targets {
		items := assignments[target.ID]
		sort.Slice(items, func(i, j int) bool {
			if items[i].SimilarityScore == items[j].SimilarityScore {
				return items[i].CandidatePersonID < items[j].CandidatePersonID
			}
			return items[i].SimilarityScore > items[j].SimilarityScore
		})
		for i := range items {
			items[i].Rank = i + 1
		}
		assignments[target.ID] = items
	}

	return assignments, nil
}

func (s *personMergeSuggestionService) buildSuggestionResponses(
	suggestions []*model.PersonMergeSuggestion,
	items []*model.PersonMergeSuggestionItem,
) []model.PersonMergeSuggestionResponse {
	if len(suggestions) == 0 {
		return nil
	}

	personIDs := make([]uint, 0, len(suggestions)+len(items))
	for _, suggestion := range suggestions {
		personIDs = append(personIDs, suggestion.TargetPersonID)
	}
	for _, item := range items {
		personIDs = append(personIDs, item.CandidatePersonID)
	}

	people, _ := s.personRepo.ListByIDs(uniqueUintIDs(personIDs))
	peopleByID := make(map[uint]*model.Person, len(people))
	for _, person := range people {
		if person != nil {
			peopleByID[person.ID] = person
		}
	}

	itemsBySuggestion := make(map[uint][]model.PersonMergeSuggestionItemResponse)
	for _, item := range items {
		if item == nil {
			continue
		}
		itemsBySuggestion[item.SuggestionID] = append(itemsBySuggestion[item.SuggestionID], model.PersonMergeSuggestionItemResponse{
			ID:                item.ID,
			SuggestionID:      item.SuggestionID,
			CandidatePersonID: item.CandidatePersonID,
			SimilarityScore:   item.SimilarityScore,
			Rank:              item.Rank,
			Status:            item.Status,
			CandidatePerson:   toSuggestionPersonResponse(peopleByID[item.CandidatePersonID]),
		})
	}

	responses := make([]model.PersonMergeSuggestionResponse, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if suggestion == nil {
			continue
		}
		responses = append(responses, model.PersonMergeSuggestionResponse{
			ID:                     suggestion.ID,
			TargetPersonID:         suggestion.TargetPersonID,
			TargetCategorySnapshot: suggestion.TargetCategorySnapshot,
			Status:                 suggestion.Status,
			CandidateCount:         suggestion.CandidateCount,
			TopSimilarity:          suggestion.TopSimilarity,
			ReviewedAt:             suggestion.ReviewedAt,
			CreatedAt:              suggestion.CreatedAt,
			UpdatedAt:              suggestion.UpdatedAt,
			TargetPerson:           toSuggestionPersonResponse(peopleByID[suggestion.TargetPersonID]),
			Items:                  itemsBySuggestion[suggestion.ID],
		})
	}
	return responses
}

func (s *personMergeSuggestionService) loadState() error {
	var raw string
	switch {
	case s.configService != nil:
		value, err := s.configService.GetWithDefault(personMergeSuggestionStateKey, "")
		if err != nil {
			return err
		}
		raw = value
	default:
		var cfg model.AppConfig
		if err := s.db.Where("key = ?", personMergeSuggestionStateKey).First(&cfg).Error; err == nil {
			raw = cfg.Value
		}
	}

	if raw == "" {
		return nil
	}

	var state personMergeSuggestionState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	if state.Paused {
		s.task.Status = model.TaskStatusPaused
		s.task.CurrentMessage = "已暂停"
	} else {
		s.task.Status = model.TaskStatusIdle
	}
	return nil
}

func (s *personMergeSuggestionService) saveStateLocked() error {
	payload, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	if s.configService != nil {
		return s.configService.Set(personMergeSuggestionStateKey, string(payload))
	}
	return upsertMergeSuggestionState(s.db, string(payload))
}

func (s *personMergeSuggestionService) finishSliceLocked(now time.Time, processedPairs int, message string) {
	s.state.LastRunAt = now
	s.task.Status = model.TaskStatusIdle
	s.task.CurrentMessage = message
	s.task.ProcessedPairs += int64(processedPairs)
	s.task.StoppedAt = &now
	s.appendBackgroundLogLocked(message)
	_ = s.saveStateLocked()
}

func (s *personMergeSuggestionService) appendBackgroundLogLocked(message string) {
	if message == "" {
		return
	}
	entry := fmt.Sprintf("%s %s", time.Now().Format(time.RFC3339), message)
	s.backgroundLogs = append(s.backgroundLogs, entry)
	if len(s.backgroundLogs) > 50 {
		s.backgroundLogs = s.backgroundLogs[len(s.backgroundLogs)-50:]
	}
}

func clonePersonMergeSuggestionTask(task *model.PersonMergeSuggestionTask) *model.PersonMergeSuggestionTask {
	if task == nil {
		return nil
	}
	cloned := *task
	return &cloned
}

func selectNextSuggestionTargets(targets []*model.Person, cursorTargetID uint, batchSize int) []*model.Person {
	if batchSize <= 0 {
		batchSize = 1
	}

	start := 0
	if cursorTargetID != 0 {
		start = len(targets)
		for i, target := range targets {
			if target != nil && target.ID > cursorTargetID {
				start = i
				break
			}
		}
	}
	if start >= len(targets) {
		return nil
	}

	end := start + batchSize
	if end > len(targets) {
		end = len(targets)
	}
	return targets[start:end]
}

func bestSuggestionSimilarity(targetEmbeddings, candidateEmbeddings []faceWithEmbedding) float64 {
	best := -1.0
	for _, target := range targetEmbeddings {
		for _, candidate := range candidateEmbeddings {
			score := cosineSimilarityPrecomputed(
				target.embedding, target.norm,
				candidate.embedding, candidate.norm,
			)
			if score > best {
				best = score
			}
		}
	}
	return best
}

func averageBestSuggestionSimilarity(targetEmbeddings, candidateEmbeddings []faceWithEmbedding) float64 {
	if len(targetEmbeddings) == 0 || len(candidateEmbeddings) == 0 {
		return -1
	}

	var sum float64
	count := 0
	for _, candidate := range candidateEmbeddings {
		best := -1.0
		for _, target := range targetEmbeddings {
			score := cosineSimilarityPrecomputed(
				target.embedding, target.norm,
				candidate.embedding, candidate.norm,
			)
			if score > best {
				best = score
			}
		}
		if best >= 0 {
			sum += best
			count++
		}
	}
	if count == 0 {
		return -1
	}
	return sum / float64(count)
}

func (s *personMergeSuggestionService) attachThreshold() float64 {
	if s.config != nil && s.config.People.AttachThreshold > 0 {
		return s.config.People.AttachThreshold
	}
	return defaultAttachThreshold
}

func toSuggestionPersonResponse(person *model.Person) *model.PersonResponse {
	if person == nil {
		return nil
	}
	return &model.PersonResponse{
		ID:                   person.ID,
		Name:                 person.Name,
		Category:             person.Category,
		RepresentativeFaceID: person.RepresentativeFaceID,
		HasAvatar:            person.RepresentativeFaceID != nil,
		AvatarLocked:         person.AvatarLocked,
		FaceCount:            person.FaceCount,
		PhotoCount:           person.PhotoCount,
		CreatedAt:            person.CreatedAt,
		UpdatedAt:            person.UpdatedAt,
	}
}

func uniqueUintIDs(ids []uint) []uint {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func upsertMergeSuggestionState(db *gorm.DB, value string) error {
	var cfg model.AppConfig
	err := db.Where("key = ?", personMergeSuggestionStateKey).First(&cfg).Error
	if err == nil {
		return db.Model(&cfg).Update("value", value).Error
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return db.Create(&model.AppConfig{
		Key:   personMergeSuggestionStateKey,
		Value: value,
	}).Error
}
