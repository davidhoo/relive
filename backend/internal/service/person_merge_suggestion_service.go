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
	if reason != "" {
		s.appendBackgroundLogLocked("合并建议待更新: " + reason)
	}
	return s.saveStateLocked()
}

func (s *personMergeSuggestionService) RunBackgroundSlice() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state.Paused {
		s.task.Status = model.TaskStatusPaused
		s.task.CurrentMessage = "已暂停"
		return nil
	}

	now := time.Now()
	if s.task.StartedAt == nil {
		s.task.StartedAt = &now
	}
	s.task.Status = model.TaskStatusRunning
	s.task.CurrentMessage = "巡检人物合并建议"

	targets, err := s.listSuggestionTargets()
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		s.finishSliceLocked(now, 0, "没有可巡检的人物目标")
		return nil
	}

	batchSize, err := s.currentBatchSize()
	if err != nil {
		return err
	}
	selected := selectNextSuggestionTargets(targets, s.state.CursorTargetID, batchSize)
	if len(selected) == 0 {
		s.state.CursorTargetID = 0
		s.state.Dirty = false
		s.state.LastRunAt = now
		s.task.Status = model.TaskStatusIdle
		s.task.CurrentMessage = "本轮巡检完成"
		s.task.StoppedAt = &now
		return s.saveStateLocked()
	}

	assignments, err := s.buildAssignments(selected)
	if err != nil {
		return err
	}

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
	allPeople, err := s.personRepo.ListAll()
	if err != nil {
		return nil, err
	}
	faces, err := s.faceRepo.ListAssigned()
	if err != nil {
		return nil, err
	}

	facesByPerson := make(map[uint][]*model.Face)
	for _, face := range faces {
		if face == nil || face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		facesByPerson[*face.PersonID] = append(facesByPerson[*face.PersonID], face)
	}

	candidatePrototypes := make(map[uint][]faceWithEmbedding)
	for _, person := range allPeople {
		if person == nil {
			continue
		}
		personFaces := facesByPerson[person.ID]
		if len(personFaces) == 0 {
			continue
		}
		candidatePrototypes[person.ID] = decodeFacesWithEmbeddings(selectDiversePrototypes(personFaces, peoplePrototypeCount))
	}

	bestByCandidate := make(map[uint]mergeSuggestionCandidate)
	threshold := s.mergeSuggestionThreshold()
	for _, target := range targets {
		if target == nil {
			continue
		}
		targetFaces := facesByPerson[target.ID]
		if len(targetFaces) == 0 {
			continue
		}
		targetEmbeddings := decodeFacesWithEmbeddings(selectDiversePrototypes(targetFaces, peoplePrototypeCount))
		for _, person := range allPeople {
			if person == nil || person.ID == target.ID {
				continue
			}
			candidateEmbeddings := candidatePrototypes[person.ID]
			if len(candidateEmbeddings) == 0 {
				continue
			}
			blocked, err := s.cannotLinkRepo.ExistsBetween(target.ID, person.ID)
			if err != nil {
				return nil, err
			}
			if blocked {
				continue
			}

			score := averageBestSuggestionSimilarity(targetEmbeddings, candidateEmbeddings)
			if score < threshold || score >= s.attachThreshold() {
				continue
			}

			current, exists := bestByCandidate[person.ID]
			if !exists || score > current.score || (score == current.score && target.ID < current.targetID) {
				bestByCandidate[person.ID] = mergeSuggestionCandidate{
					targetID:     target.ID,
					candidateID:  person.ID,
					score:        score,
					targetPerson: target,
				}
			}
		}
	}

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
