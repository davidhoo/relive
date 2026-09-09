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
	"github.com/davidhoo/relive/pkg/database"
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
	CalculateSimilarity(personID1, personID2 uint) (float64, error) // 计算两个人物间的相似度
	MergeSuggestionThreshold() float64                              // 获取合并建议阈值
	AttachThreshold() float64                                       // 获取附加阈值
}

type personMergeSuggestionState struct {
	Paused          bool      `json:"paused"`
	Dirty           bool      `json:"dirty"`
	CursorTargetID  uint      `json:"cursor_target_id"`
	LastRunAt       time.Time `json:"last_run_at,omitempty"`
	DirtyGeneration uint64    `json:"dirty_generation,omitempty"`
	// RetryTargets 记录技术不可用目标及预算；重启后保留。
	RetryTargets []mergeSuggestionRetryEntry `json:"retry_targets,omitempty"`
	// RetryOnly 为 true 时本轮只处理到期重试目标，不做全库 cursor 扫描。
	RetryOnly bool `json:"retry_only,omitempty"`
}

type personMergeSuggestionService struct {
	db                  *gorm.DB
	photoRepo           repository.PhotoRepository
	faceRepo            repository.FaceRepository
	personRepo          repository.PersonRepository
	jobRepo             repository.PeopleJobRepository
	cannotLinkRepo      repository.CannotLinkRepository
	mergeSuggestionRepo repository.PersonMergeSuggestionRepository
	feedbackEventRepo   repository.PeopleFeedbackEventRepository
	configService       ConfigService
	config              *config.Config
	writeQueue          *database.WriteQueue

	// profileProvider 注入身份画像相似度 provider；仅非 legacy 模式注入。
	// 为 nil 时合并建议完整走现有 prototype ANN 路径，行为与 Task 10 前一致。
	profileProvider PersonProfileSimilarityProvider

	// identityMatchingEngine + mode/strategy：primary 模式走统一引擎，禁止 legacy 隐式回退。
	identityMatchingEngine    *IdentityMatchingEngine
	identityProfileMode       string
	identitySuggestStrategy   IdentityStrategy
	identityConfigFingerprint string

	mu             sync.RWMutex
	task           *model.PersonMergeSuggestionTask
	state          personMergeSuggestionState
	backgroundLogs []string
	annMu          sync.Mutex // guards all ANN build state below for concurrent access
	// annBuildCond 在 annMu 上等待 annBuilding 完成通知。ensureANNIndex 的并发等待者用它
	// 复用第一个调用者的构建结果，避免重复 DB 读取与 HNSW 建图。
	annBuildCond *sync.Cond
	annIdx       *annIndex
	// ANN rebuild 单实例与 generation 协调：
	//   - annGeneration：单调递增，每次 MarkDirty 推进。记录“最新一次被标记 dirty 的 generation”。
	//   - targetGeneration：当前正在构建（或最近一次启动构建）的目标 generation。
	//   - annBuilding：是否正有 rebuild 在进行。rebuild 启动时置位，完成后清除。
	//   - annDirty：是否有未完成构建的 dirty。build 成功且 target==annGeneration 时才清除；
	//     build 期间若又有 MarkDirty 推进了 annGeneration，则保持 dirty（pending）。
	annGeneration    uint64
	targetGeneration uint64
	annBuilding      bool
	annDirty         bool      // index is stale; rebuild on next ensureANNIndex call
	annBuiltAt       time.Time // when index was last successfully built

	// Dedicated background DB pool (separate from the API pool) so that
	// long-running merge-suggestion work does not starve API connections.
	bgDB                  *gorm.DB
	bgPersonRepo          repository.PersonRepository
	bgFaceRepo            repository.FaceRepository
	bgCannotLinkRepo      repository.CannotLinkRepository
	bgMergeSuggestionRepo repository.PersonMergeSuggestionRepository

	// writeGateFn acquires the write gate from peopleService and returns a release function.
	// This ensures foreground merge operations exclude the background clustering worker
	// from writing faces/people tables simultaneously, preventing SQLite "database is locked".
	writeGateFn func() func()

	// backgroundCoordinator 是统一后台任务准入控制器（Task 12）。RunBackgroundSlice 在
	// heavy work 前请求 BackgroundTaskMergeSuggestion 准入，被拒绝则 skip return nil，
	// 不 mark clean、不 advance cursor。nil 时不 gating（向后兼容）。
	backgroundCoordinator *BackgroundTaskCoordinator

	// postMergeFn is called after a suggestion-based merge completes.
	// It handles syncPersonState, cannot-link cleanup, RecomputeTopPersonCategory,
	// and feedback recluster — mirroring the cleanup that MergePeople does.
	postMergeFn func(targetPersonID uint, sourcePersonIDs []uint, affectedPhotoIDs []uint)

	// annBuildHook 仅供测试注入“rebuild 期间”的并发 MarkDirty，以验证 generation 协调语义；
	// 生产中始终为 nil。在 buildANNIndex 完成 DB 读取、即将构建 HNSW 前调用。
	annBuildHook func()
}

type mergeSuggestionCandidate struct {
	targetID          uint
	candidateID       uint
	score             float64
	targetPerson      *model.Person
	source            string // legacy / identity_profile
	warning           string // "" / same_photo_cooccurrence
	reason            string
	margin            *float64
	profileGen        int
	engineVersion     string
	strategyVersion   string
	configFingerprint string
	indexGeneration   int
	targetProfileGen  int
}

// mergeSuggestionProfileK 是非 legacy 模式下每个目标人物从身份画像召回的最大候选数。
const mergeSuggestionProfileK = 50

func (s *personMergeSuggestionService) buildAssignments(targets []*model.Person) (map[uint][]model.PersonMergeSuggestionItem, error) {
	// Phase 1: ensure ANN index is ready (lazy build, cached across slices).
	idx, err := s.ensureANNIndex()
	if err != nil {
		return nil, err
	}

	// Phase 2: load cannot-link constraints.
	cannotLinkCache, err := s.loadCannotLinkCache()
	if err != nil {
		return nil, err
	}

	// legacy 模式（provider 未注入）完整走现有 prototype ANN 路径，行为与 Task 10 前一致。
	if s.profileProvider == nil && s.identityMatchingEngine == nil {
		return s.legacyAssignments(targets, idx, cannotLinkCache)
	}
	// primary：统一引擎，禁止整批/逐目标/逐对 legacy 回退。
	if s.identityProfileMode == model.PeopleIdentityModePrimary && s.identityMatchingEngine != nil {
		return s.primaryAssignments(targets, cannotLinkCache)
	}
	return s.mixedAssignments(targets, idx, cannotLinkCache)
}

// loadCannotLinkCache 一次性加载全部 cannot-link 约束为对称缓存，供候选过滤使用。
func (s *personMergeSuggestionService) loadCannotLinkCache() (map[uint]map[uint]bool, error) {
	_, _, bgCannotLinkRepo, _ := s.bgRepos()
	cannotLinkCache := make(map[uint]map[uint]bool)
	allCannotLinks, err := bgCannotLinkRepo.ListAll()
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
	return cannotLinkCache, nil
}

// cannotLinkBlocked 返回 target 与 candidate 是否存在 cannot-link 约束（对称）。
func cannotLinkBlocked(cannotLinkCache map[uint]map[uint]bool, targetID, candidateID uint) bool {
	if cannotLinkCache[targetID] != nil && cannotLinkCache[targetID][candidateID] {
		return true
	}
	if cannotLinkCache[candidateID] != nil && cannotLinkCache[candidateID][targetID] {
		return true
	}
	return false
}

// legacyAssignments 是 Task 10 前的现有 prototype ANN 路径：批量召回 → DB 校验 → 并行精确评分 →
// 全局 bestByCandidate。所有候选 MatchSource=legacy、Warning=""。legacy 模式下调用路径与结果不变。
func (s *personMergeSuggestionService) legacyAssignments(targets []*model.Person, idx *annIndex, cannotLinkCache map[uint]map[uint]bool) (map[uint][]model.PersonMergeSuggestionItem, error) {
	bgPersonRepo, _, _, _ := s.bgRepos()

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

	// Phase 3.5: validate candidate persons against the database.
	allCandidateIDs := make([]uint, 0)
	for _, cands := range candidatesByTarget {
		for cid := range cands {
			allCandidateIDs = append(allCandidateIDs, cid)
		}
	}
	if len(allCandidateIDs) > 0 {
		validPeople, err := bgPersonRepo.ListByIDs(uniqueUintIDs(allCandidateIDs))
		if err != nil {
			return nil, fmt.Errorf("validate candidates: %w", err)
		}
		validCandidates := make(map[uint]struct{}, len(validPeople))
		for _, p := range validPeople {
			if p != nil && p.FaceCount > 0 {
				validCandidates[p.ID] = struct{}{}
			}
		}
		for tid, cands := range candidatesByTarget {
			for cid := range cands {
				if _, ok := validCandidates[cid]; !ok {
					delete(candidatesByTarget[tid], cid)
				}
			}
		}
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
				if cannotLinkBlocked(cannotLinkCache, tgt.ID, candidateID) {
					continue
				}

				score := legacyPrototypePairScore(tgtEmb, candidateEmbeddings)
				if score < threshold {
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
						source:       model.PersonMergeMatchSourceLegacy,
						warning:      "",
					}
				}
				bestMutex.Unlock()
			}
		}(target, targetProtos, candidates)
	}
	wg.Wait()

	return s.buildItemsFromBest(bestByCandidate, targets), nil
}

// mixedAssignments 在非 legacy 模式下接入身份画像 provider：逐目标尝试画像召回 + 中心/medoid
// 精确验证，profile 不可用时逐目标回退 legacy prototype 路径，medoid 验证失败时逐对回退 legacy。
// cannot-link 对 profile/legacy 候选都硬阻断；同照片共现的画像候选写入 warning 供人工审核。
func (s *personMergeSuggestionService) mixedAssignments(targets []*model.Person, idx *annIndex, cannotLinkCache map[uint]map[uint]bool) (map[uint][]model.PersonMergeSuggestionItem, error) {
	_, bgFaceRepo, _, _ := s.bgRepos()
	threshold := s.mergeSuggestionThreshold()

	targetIDs := make([]uint, 0, len(targets))
	for _, t := range targets {
		if t == nil {
			continue
		}
		targetIDs = append(targetIDs, t.ID)
	}

	// 整批画像召回不可用 → 完整回退 legacy（结果等同现有路径）。
	profileMatches, profileOK := s.profileProvider.SimilarPeople(targetIDs, mergeSuggestionProfileK)
	if !profileOK {
		return s.legacyAssignments(targets, idx, cannotLinkCache)
	}

	bestByCandidate := make(map[uint]mergeSuggestionCandidate)

	for _, t := range targets {
		if t == nil {
			continue
		}
		matches := profileMatches[t.ID]
		// 该目标无 ready profile（或无召回）→ 逐目标回退 legacy。
		if len(matches) == 0 {
			s.scoreLegacyTarget(t, idx, cannotLinkCache, threshold, bestByCandidate)
			continue
		}

		// 对该目标的画像候选批量做中心 + medoid 精确验证。
		pairs := make([]PersonPair, 0, len(matches))
		for _, m := range matches {
			pairs = append(pairs, PersonPair{TargetID: t.ID, CandidateID: m.PersonID})
		}
		comparisons, cmpOK := s.profileProvider.ComparePeople(pairs)
		if !cmpOK {
			// 画像精确比较不可用 → 该目标回退 legacy。
			s.scoreLegacyTarget(t, idx, cannotLinkCache, threshold, bestByCandidate)
			continue
		}

		// 收集该目标的候选：profile 可用候选 + medoid 失败的逐对 legacy 回退候选。
		type profileCandidate struct {
			candidateID uint
			score       float64
		}
		var profileCands []profileCandidate
		for _, m := range matches {
			candID := m.PersonID
			if cannotLinkBlocked(cannotLinkCache, t.ID, candID) {
				continue // cannot-link 硬阻断，profile/legacy 候选均不生成
			}
			comp := comparisons[PersonPair{TargetID: t.ID, CandidateID: candID}]
			if comp.Available {
				if comp.Score >= threshold {
					profileCands = append(profileCands, profileCandidate{candidateID: candID, score: comp.Score})
				}
				continue
			}
			// medoid 验证失败 → 该对回退 prototype 精确评分。
			protoScore := legacyPrototypePairScore(idx.personProtos[t.ID], idx.personProtos[candID])
			if protoScore >= threshold {
				s.updateBestCandidate(bestByCandidate, mergeSuggestionCandidate{
					targetID:     t.ID,
					candidateID:  candID,
					score:        protoScore,
					targetPerson: t,
					source:       model.PersonMergeMatchSourceLegacy,
					warning:      "",
				})
			}
		}

		if len(profileCands) == 0 {
			continue
		}

		// 同照片共现查询：仅画像候选参与（legacy 候选不写 warning）。
		candIDs := make([]uint, 0, len(profileCands))
		for _, pc := range profileCands {
			candIDs = append(candIDs, pc.candidateID)
		}
		sharing, err := bgFaceRepo.ListPersonIDsSharingPhotos(t.ID, candIDs)
		if err != nil {
			// 共现查询失败 → 守住精度，该目标画像候选回退 legacy。
			s.scoreLegacyTarget(t, idx, cannotLinkCache, threshold, bestByCandidate)
			continue
		}
		sharingSet := make(map[uint]struct{}, len(sharing))
		for _, id := range sharing {
			sharingSet[id] = struct{}{}
		}
		for _, pc := range profileCands {
			warning := ""
			if _, ok := sharingSet[pc.candidateID]; ok {
				warning = model.PersonMergeWarningSamePhotoCooccurrence
			}
			s.updateBestCandidate(bestByCandidate, mergeSuggestionCandidate{
				targetID:     t.ID,
				candidateID:  pc.candidateID,
				score:        pc.score,
				targetPerson: t,
				source:       model.PersonMergeMatchSourceIdentityProfile,
				warning:      warning,
			})
		}
	}

	return s.buildItemsFromBest(bestByCandidate, targets), nil
}

// scoreLegacyTarget 对单个目标执行 prototype ANN 召回 + DB 校验 + 精确评分，结果写入 bestByCandidate。
// 用于非 legacy 模式下逐目标/逐对 legacy 回退。所有候选 MatchSource=legacy、Warning=""。
func (s *personMergeSuggestionService) scoreLegacyTarget(t *model.Person, idx *annIndex, cannotLinkCache map[uint]map[uint]bool, threshold float64, bestByCandidate map[uint]mergeSuggestionCandidate) {
	bgPersonRepo, _, _, _ := s.bgRepos()
	targetProtos := idx.personProtos[t.ID]
	if len(targetProtos) == 0 {
		return
	}
	cands := idx.annCandidates(t.ID, targetProtos, annSearchK)
	if len(cands) == 0 {
		return
	}
	candIDs := make([]uint, 0, len(cands))
	for cid := range cands {
		candIDs = append(candIDs, cid)
	}
	validPeople, err := bgPersonRepo.ListByIDs(uniqueUintIDs(candIDs))
	if err != nil {
		return // 无法校验 → 该目标 fail-closed，不生成候选
	}
	validCandidates := make(map[uint]struct{}, len(validPeople))
	for _, p := range validPeople {
		if p != nil && p.FaceCount > 0 {
			validCandidates[p.ID] = struct{}{}
		}
	}
	for candidateID := range cands {
		if _, ok := validCandidates[candidateID]; !ok {
			continue
		}
		candidateProtos := idx.personProtos[candidateID]
		if len(candidateProtos) == 0 {
			continue
		}
		if cannotLinkBlocked(cannotLinkCache, t.ID, candidateID) {
			continue
		}
		score := legacyPrototypePairScore(targetProtos, candidateProtos)
		if score < threshold {
			continue
		}
		s.updateBestCandidate(bestByCandidate, mergeSuggestionCandidate{
			targetID:     t.ID,
			candidateID:  candidateID,
			score:        score,
			targetPerson: t,
			source:       model.PersonMergeMatchSourceLegacy,
			warning:      "",
		})
	}
}

// updateBestCandidate 按扩展确定性规则将候选写入全局 bestByCandidate：同一 candidate 只归属最佳 target。
func (s *personMergeSuggestionService) updateBestCandidate(bestByCandidate map[uint]mergeSuggestionCandidate, cand mergeSuggestionCandidate) {
	existing, ok := bestByCandidate[cand.candidateID]
	if !ok || mergeCandidateBetter(cand, existing) {
		bestByCandidate[cand.candidateID] = cand
	}
}

// mergeCandidateBetter 实现候选归属的确定性比较：
//
//	finalScore DESC → 无 warning 优先于有 warning → profile 优先于 legacy → targetPersonID ASC
func mergeCandidateBetter(a, b mergeSuggestionCandidate) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	aWarn := a.warning != ""
	bWarn := b.warning != ""
	if aWarn != bWarn {
		return !aWarn
	}
	if a.source != b.source {
		return mergeSourceRank(a.source) < mergeSourceRank(b.source)
	}
	return a.targetID < b.targetID
}

// mergeSourceRank 给来源打确定性次序：identity_profile(0) 优先于 legacy(1)。
func mergeSourceRank(source string) int {
	if source == model.PersonMergeMatchSourceIdentityProfile {
		return 0
	}
	return 1
}

// buildItemsFromBest 将全局 bestByCandidate 按 target 聚合，分数降序、candidate ID 升序排序并赋予 rank。
// 仅返回 bestByCandidate 中出现的目标；调用方若需对「确实算完且无建议」的目标写空替换，
// 应自行保证这些目标以空 slice 出现在返回 map 中。
func (s *personMergeSuggestionService) buildItemsFromBest(bestByCandidate map[uint]mergeSuggestionCandidate, targets []*model.Person) map[uint][]model.PersonMergeSuggestionItem {
	assignments := make(map[uint][]model.PersonMergeSuggestionItem, len(targets))
	for _, assignment := range bestByCandidate {
		source := assignment.source
		if source == "" {
			source = model.PersonMergeMatchSourceLegacy
		}
		assignments[assignment.targetID] = append(assignments[assignment.targetID], model.PersonMergeSuggestionItem{
			CandidatePersonID:          assignment.candidateID,
			SimilarityScore:            assignment.score,
			Status:                     model.PersonMergeSuggestionItemStatusPending,
			MatchSource:                source,
			Warning:                    assignment.warning,
			Reason:                     assignment.reason,
			Margin:                     assignment.margin,
			CandidateProfileGeneration: assignment.profileGen,
			EngineVersion:              assignment.engineVersion,
			StrategyVersion:            assignment.strategyVersion,
			ConfigFingerprint:          assignment.configFingerprint,
			IndexGeneration:            assignment.indexGeneration,
			TargetProfileGeneration:    assignment.targetProfileGen,
		})
	}
	for _, target := range targets {
		if target == nil {
			continue
		}
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
	return assignments
}

// legacyPrototypePairScore 使用 prototype embedding 计算两个人物的双向平均最佳相似度，
// 与现有 legacy 评分一致。供 legacy 路径与画像 medoid 失败时的逐对回退复用。
func legacyPrototypePairScore(targetEmbeddings, candidateEmbeddings []faceWithEmbedding) float64 {
	if len(targetEmbeddings) == 0 || len(candidateEmbeddings) == 0 {
		return -1
	}
	score1 := averageBestSuggestionSimilarity(targetEmbeddings, candidateEmbeddings)
	score2 := averageBestSuggestionSimilarity(candidateEmbeddings, targetEmbeddings)
	return (score1 + score2) / 2
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
		writeQueue:          database.GetWriteQueue(),
		task: &model.PersonMergeSuggestionTask{
			Status:         model.TaskStatusIdle,
			CurrentMessage: "等待巡检",
		},
		backgroundLogs: make([]string, 0, 32),
	}
	svc.annBuildCond = sync.NewCond(&svc.annMu)
	_ = svc.loadState()

	// Always mark dirty on startup: the in-memory ANN index is lost on restart
	// and must be rebuilt, and data may have changed while the service was down.
	if !svc.state.Paused {
		svc.mu.Lock()
		svc.state.Dirty = true
		svc.state.CursorTargetID = 0
		svc.annMu.Lock()
		// 启动时推进 generation 并标记 dirty：内存 ANN 索引在重启后丢失，必须重建。
		svc.annGeneration++
		svc.annDirty = true
		svc.annMu.Unlock()
		_ = svc.saveStateLocked()
		svc.mu.Unlock()
	}

	return svc
}

// SetBackgroundDB sets a dedicated DB pool for background operations.
// When set, background slice work (RunBackgroundSlice) uses repos backed by this
// pool instead of the shared API pool, preventing long-running background work
// from starving API connections.
func (s *personMergeSuggestionService) SetBackgroundDB(bgDB *gorm.DB) {
	if bgDB == nil {
		return
	}
	s.bgDB = bgDB
	s.bgPersonRepo = repository.NewPersonRepository(bgDB)
	s.bgFaceRepo = repository.NewFaceRepository(bgDB)
	s.bgCannotLinkRepo = repository.NewCannotLinkRepository(bgDB)
	s.bgMergeSuggestionRepo = repository.NewPersonMergeSuggestionRepository(bgDB)
}

// executeWrite runs fn through WriteQueue if available, otherwise directly.
// This serializes SQLite writes to prevent lock contention between background
// merge-suggestion writes and foreground API writes.
func (s *personMergeSuggestionService) executeWrite(fn func() error) error {
	if s.writeQueue != nil {
		return s.writeQueue.Execute(fn)
	}
	return fn()
}

// SetWriteGateHook injects the write gate acquire function from peopleService.
func (s *personMergeSuggestionService) SetWriteGateHook(fn func() func()) {
	s.writeGateFn = fn
}

// SetBackgroundCoordinator 注入统一后台任务准入控制器（Task 12）。nil 时不 gating。
func (s *personMergeSuggestionService) SetBackgroundCoordinator(c *BackgroundTaskCoordinator) {
	s.backgroundCoordinator = c
}

// SetPostMergeHook injects the post-merge cleanup function from peopleService.
func (s *personMergeSuggestionService) SetPostMergeHook(fn func(targetPersonID uint, sourcePersonIDs []uint, affectedPhotoIDs []uint)) {
	s.postMergeFn = fn
}

// SetFeedbackEventRepo 注入反馈事件仓库。生产环境由 service.go 装配时注入；
// 测试可注入失败 stub 或 nil（nil 时反馈记录被静默跳过）。
func (s *personMergeSuggestionService) SetFeedbackEventRepo(repo repository.PeopleFeedbackEventRepository) {
	s.feedbackEventRepo = repo
}

// SetProfileSimilarityProvider 注入身份画像相似度 provider。仅非 legacy 模式注入；
// legacy 模式必须保持 nil，使合并建议完整走现有 prototype ANN 路径。测试通过 fake provider 注入。
func (s *personMergeSuggestionService) SetProfileSimilarityProvider(provider PersonProfileSimilarityProvider) {
	s.profileProvider = provider
}

// SetIdentityMatchingEngine 注入统一身份匹配引擎（primary 推荐路径）。
func (s *personMergeSuggestionService) SetIdentityMatchingEngine(engine *IdentityMatchingEngine) {
	s.identityMatchingEngine = engine
}

// SetIdentityProfileMode 注入当前身份画像模式，用于选择推荐决策路径。
func (s *personMergeSuggestionService) SetIdentityProfileMode(mode string) {
	s.identityProfileMode = mode
}

// SetIdentitySuggestStrategy 注入人工推荐策略。
func (s *personMergeSuggestionService) SetIdentitySuggestStrategy(strategy IdentityStrategy) {
	s.identitySuggestStrategy = strategy
}

// SetIdentityConfigFingerprint 注入策略配置指纹，写入新推荐记录。
func (s *personMergeSuggestionService) SetIdentityConfigFingerprint(fp string) {
	s.identityConfigFingerprint = fp
}

// primaryAssignments 使用统一引擎生成推荐，禁止任何 legacy 隐式回退。
// 硬冲突直接丢弃；unavailable 目标不进入返回 map（调用方不得对其空替换 pending）。
func (s *personMergeSuggestionService) primaryAssignments(targets []*model.Person, cannotLinkCache map[uint]map[uint]bool) (map[uint][]model.PersonMergeSuggestionItem, error) {
	strategy := s.identitySuggestStrategy
	if strategy.Name == "" && s.config != nil {
		strategy = NewIdentitySuggestStrategy(s.config.People)
	}
	threshold := strategy.ScoreThreshold
	if threshold <= 0 {
		threshold = s.mergeSuggestionThreshold()
	}

	targetIDs := make([]uint, 0, len(targets))
	targetByID := make(map[uint]*model.Person, len(targets))
	for _, t := range targets {
		if t == nil {
			continue
		}
		targetIDs = append(targetIDs, t.ID)
		targetByID[t.ID] = t
	}

	opts := IdentityRecallOptions{TopK: mergeSuggestionProfileK}.normalized()
	results := s.identityMatchingEngine.SimilarPeople(targetIDs, opts)

	bestByCandidate := make(map[uint]mergeSuggestionCandidate)
	completeTargets := make([]*model.Person, 0, len(targets))
	var unavailableTargets []uint
	unavailReasonByTarget := make(map[uint]string)
	indexGen := 0
	if s.identityMatchingEngine != nil {
		indexGen = s.identityMatchingEngine.IndexGeneration()
	}
	engineVersion := identityEngineVersion
	if s.identityMatchingEngine != nil {
		engineVersion = s.identityMatchingEngine.EngineVersion()
	}

	for _, tid := range targetIDs {
		res, ok := results[tid]
		if !ok {
			unavailableTargets = append(unavailableTargets, tid)
			continue
		}
		switch res.Status {
		case IdentityMatchStatusUnavailable, IdentityMatchStatusInvalid:
			unavailableTargets = append(unavailableTargets, tid)
			if res.BlockReason != "" {
				unavailReasonByTarget[tid] = res.BlockReason
			} else {
				unavailReasonByTarget[tid] = string(res.Status)
			}
			continue
		case IdentityMatchStatusNoCandidate:
			if p := targetByID[tid]; p != nil {
				completeTargets = append(completeTargets, p)
			}
			continue
		case IdentityMatchStatusHardConflict:
			// 最佳候选硬冲突：本目标仍算计算完成（不写该候选），继续看 Candidates 列表。
		}

		pairs := make([]PersonPair, 0, len(res.Candidates))
		for _, c := range res.Candidates {
			if c.PersonID == 0 || cannotLinkBlocked(cannotLinkCache, tid, c.PersonID) {
				continue
			}
			pairs = append(pairs, PersonPair{TargetID: tid, CandidateID: c.PersonID})
		}
		comparisons := s.identityMatchingEngine.ComparePeople(pairs)

		pairUnavailable := res.IncompleteEvidence
		if res.IncompleteEvidence {
			unavailReasonByTarget[tid] = blockProfileUnavailable
		}
		acceptedForTarget := false
		for _, pr := range pairs {
			cmp := comparisons[pr]
			if cmp.Status == IdentityMatchStatusUnavailable || cmp.Status == IdentityMatchStatusInvalid {
				pairUnavailable = true
				continue
			}
			if cmp.Status == IdentityMatchStatusHardConflict {
				continue
			}
			if cmp.Best == nil || cmp.Best.PersonID == 0 {
				continue
			}
			dec := ApplyIdentityStrategy(cmp, strategy)
			if !dec.Accepted {
				continue
			}
			if dec.Score < threshold {
				continue
			}
			var margin *float64
			if cmp.MarginApplicable {
				m := cmp.Margin
				margin = &m
			}
			acceptedForTarget = true
			candGen := 0
			if cmp.Best != nil {
				candGen = cmp.Best.ProfileGeneration
			}
			s.updateBestCandidate(bestByCandidate, mergeSuggestionCandidate{
				targetID:          tid,
				candidateID:       dec.PersonID,
				score:             dec.Score,
				targetPerson:      targetByID[tid],
				source:            model.PersonMergeMatchSourceIdentityProfile,
				warning:           "",
				reason:            cmp.BlockReason,
				margin:            margin,
				profileGen:        candGen,
				engineVersion:     engineVersion,
				strategyVersion:   strategy.Version,
				configFingerprint: s.identityConfigFingerprint,
				indexGeneration:   indexGen,
				targetProfileGen:  cmp.TargetProfileGeneration,
			})
		}
		if pairUnavailable && !acceptedForTarget {
			// 必要配对不可用且无任何可写结果：禁止空替换。
			unavailableTargets = append(unavailableTargets, tid)
			continue
		}
		if pairUnavailable {
			// 有部分可写结果，仍记入重试，但允许写入已接受候选。
			unavailableTargets = append(unavailableTargets, tid)
		}
		if p := targetByID[tid]; p != nil {
			completeTargets = append(completeTargets, p)
		}
	}

	now := time.Now()
	s.mu.Lock()
	for _, tid := range unavailableTargets {
		reason := unavailReasonByTarget[tid]
		if reason == "" {
			reason = blockProfileUnavailable
		}
		s.state.RetryTargets = upsertMergeSuggestionRetry(s.state.RetryTargets, tid, reason, now)
	}
	if len(unavailableTargets) > 0 {
		msg := fmt.Sprintf("partial: %d targets waiting on identity engine", len(unavailableTargets))
		if s.task != nil {
			s.task.CurrentMessage = msg
		}
		s.appendBackgroundLogLocked(msg)
	}
	// 本批完整成功的目标从重试集合移除。
	if len(completeTargets) > 0 {
		done := make(map[uint]struct{}, len(completeTargets))
		unavail := make(map[uint]struct{}, len(unavailableTargets))
		for _, uid := range unavailableTargets {
			unavail[uid] = struct{}{}
		}
		for _, p := range completeTargets {
			if _, keep := unavail[p.ID]; !keep {
				done[p.ID] = struct{}{}
			}
		}
		s.state.RetryTargets = removeMergeSuggestionRetries(s.state.RetryTargets, done)
	}
	s.mu.Unlock()

	return s.buildItemsFromBest(bestByCandidate, completeTargets), nil
}

// recordFeedbackEvent 在核心人物变更已提交后单独写入一条反馈事件。必须在任何
// executeWrite 回调之外调用（由各业务方法在核心写入完成后调用），避免 WriteQueue
// 重入死锁。写入失败仅记录脱敏 warning，不影响已成功的业务结果。
func (s *personMergeSuggestionService) recordFeedbackEvent(event *model.PeopleFeedbackEvent) {
	emitFeedbackEvent(s.feedbackEventRepo, s.executeWrite, event)
}

func (s *personMergeSuggestionService) GetTask() *model.PersonMergeSuggestionTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cloned := clonePersonMergeSuggestionTask(s.task)
	if cloned == nil {
		return nil
	}
	now := time.Now()
	due, deferred, _ := classifyMergeSuggestionRetries(s.state.RetryTargets, now)
	cloned.RetryDueCount = len(due)
	cloned.RetryDeferredCount = len(deferred)
	cloned.RetryTotalCount = len(s.state.RetryTargets)
	cloned.Partial = cloned.RetryTotalCount > 0
	return cloned
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

	if err := s.executeWrite(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.PersonMergeSuggestion{}).
				Where("status = ?", model.PersonMergeSuggestionStatusPending).
				Update("status", model.PersonMergeSuggestionStatusObsolete).Error; err != nil {
				return err
			}
			return tx.Model(&model.PersonMergeSuggestionItem{}).
				Where("status = ?", model.PersonMergeSuggestionItemStatusPending).
				Update("status", model.PersonMergeSuggestionItemStatusObsolete).Error
		})
	}); err != nil {
		return err
	}

	s.state.Dirty = true
	s.state.CursorTargetID = 0
	s.state.RetryOnly = false
	s.annMu.Lock()
	s.annIdx = nil
	// Rebuild 推进 generation 并标记 dirty：下一次 ensureANNIndex 必须重新建图。
	s.annGeneration++
	s.annDirty = true
	s.annBuilding = false
	s.annMu.Unlock()
	s.task.Status = model.TaskStatusIdle
	s.task.CurrentMessage = "等待重建巡检"
	s.appendBackgroundLogLocked("人物合并建议已标记重建")
	return s.saveStateLocked()
}

func (s *personMergeSuggestionService) MarkDirty(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	alreadyDirty := s.state.Dirty && s.state.CursorTargetID == 0

	s.state.Dirty = true
	s.state.CursorTargetID = 0
	s.state.RetryOnly = false
	s.state.DirtyGeneration++
	// 推进 annGeneration 并标记 dirty。即使当前有 rebuild 正在进行，旧 rebuild 完成时
	// 会发现 targetGeneration != annGeneration 而保持 pending，新 dirty 不会被清除。
	s.annMu.Lock()
	s.annGeneration++
	s.annDirty = true
	s.annMu.Unlock()

	if alreadyDirty {
		return nil // already pending — skip log spam and redundant state save
	}

	if reason != "" {
		s.appendBackgroundLogLocked("合并建议待更新: " + reason)
	}
	return s.saveStateLocked()
}

// bgRepos returns the background-dedicated repos if a background DB pool is set,
// otherwise falls back to the shared repos.
func (s *personMergeSuggestionService) bgRepos() (personRepo repository.PersonRepository, faceRepo repository.FaceRepository, cannotLinkRepo repository.CannotLinkRepository, mergeSuggestionRepo repository.PersonMergeSuggestionRepository) {
	if s.bgDB != nil {
		return s.bgPersonRepo, s.bgFaceRepo, s.bgCannotLinkRepo, s.bgMergeSuggestionRepo
	}
	return s.personRepo, s.faceRepo, s.cannotLinkRepo, s.mergeSuggestionRepo
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

	// 兜底重跑：距上次巡检超过配置时间自动标记 dirty；到期技术重试单独激活 RetryOnly。
	if !s.state.Dirty {
		now := time.Now()
		dueIDs := dueMergeSuggestionRetryIDs(s.state.RetryTargets, now)
		if len(dueIDs) > 0 {
			s.state.Dirty = true
			s.state.RetryOnly = true
			s.state.CursorTargetID = 0
			s.appendBackgroundLogLocked(fmt.Sprintf("到期重试 %d 个不可用目标", len(dueIDs)))
			_ = s.saveStateLocked()
		} else {
			staleSeconds := s.config.People.MergeSuggestionStaleSeconds
			if staleSeconds <= 0 {
				staleSeconds = 86400
			}
			if !s.state.LastRunAt.IsZero() && time.Since(s.state.LastRunAt) > time.Duration(staleSeconds)*time.Second {
				s.state.Dirty = true
				s.state.RetryOnly = false
				s.annMu.Lock()
				s.annGeneration++
				s.annDirty = true
				s.annMu.Unlock()
				s.appendBackgroundLogLocked(fmt.Sprintf("自动重跑: 距上次巡检超过 %d 秒", staleSeconds))
				_ = s.saveStateLocked()
			} else {
				s.mu.Unlock()
				return nil
			}
		}
	}

	// 读取状态后释放锁
	cursor := s.state.CursorTargetID
	dirtyGen := s.state.DirtyGeneration
	retryOnly := s.state.RetryOnly
	s.mu.Unlock()

	// Task 12：heavy work 前请求 BackgroundTaskMergeSuggestion 准入。被拒绝（foreground
	// active / cooldown）则记录 skip 并 return nil——不 mark clean、不 advance cursor，
	// dirty/cursor 状态保持，下次 slice 再试。轻量 stale/dirty detection 已在上面完成
	// （低频状态更新，属既有行为），此处只 gate 重工作（listSuggestionTargets /
	// buildAssignments / suggestion writes）。
	if s.backgroundCoordinator != nil {
		release, decision, ok := s.backgroundCoordinator.Begin(BackgroundTaskRequest{
			Class:    BackgroundTaskMergeSuggestion,
			Priority: BackgroundPriorityAutomatic,
		})
		if !ok {
			s.mu.Lock()
			s.appendBackgroundLogLocked(fmt.Sprintf("跳过合并建议巡检: 后台准入被拒 (%s)", decision.Reason))
			s.mu.Unlock()
			return nil
		}
		defer release()
	}

	// Use background-dedicated repos for the heavy work in this slice.
	_, _, _, bgMergeSuggestionRepo := s.bgRepos()

	// 用 SQL cursor 分页获取目标人物（不再全量加载）；RetryOnly 只拉到期重试目标。
	var targets []*model.Person
	var err error
	var droppedRetryIDs []uint
	if retryOnly {
		targets, droppedRetryIDs, err = s.listDueRetryTargets()
	} else {
		targets, err = s.listSuggestionTargets(cursor)
	}
	if err != nil {
		return err
	}
	if len(droppedRetryIDs) > 0 {
		s.mu.Lock()
		dropSet := make(map[uint]struct{}, len(droppedRetryIDs))
		for _, id := range droppedRetryIDs {
			dropSet[id] = struct{}{}
		}
		s.state.RetryTargets = removeMergeSuggestionRetries(s.state.RetryTargets, dropSet)
		s.appendBackgroundLogLocked(fmt.Sprintf("移除 %d 个不再合格的重试目标", len(droppedRetryIDs)))
		_ = s.saveStateLocked()
		s.mu.Unlock()
	}
	if len(targets) == 0 {
		s.mu.Lock()
		now := time.Now()
		due, remaining, exhausted := classifyMergeSuggestionRetries(s.state.RetryTargets, now)
		if len(exhausted) > 0 {
			exIDs := make(map[uint]struct{}, len(exhausted))
			for _, e := range exhausted {
				exIDs[e.TargetID] = struct{}{}
			}
			s.state.RetryTargets = removeMergeSuggestionRetries(s.state.RetryTargets, exIDs)
			s.appendBackgroundLogLocked(fmt.Sprintf("放弃 %d 个耗尽重试预算的目标", len(exhausted)))
			s.markExhaustedRetriesNeedsRevalidationLocked(exhausted)
		}
		s.state.RetryTargets = remaining
		dirty, retryOnlyNext := mergeSuggestionEndOfPassFlags(due, remaining)
		s.state.CursorTargetID = 0
		s.state.Dirty = dirty
		s.state.RetryOnly = retryOnlyNext
		s.state.LastRunAt = now
		s.task.Status = model.TaskStatusIdle
		s.task.StoppedAt = &now
		switch {
		case dirty && retryOnlyNext:
			s.task.CurrentMessage = fmt.Sprintf("partial: %d targets due for identity retry", len(due))
		case len(remaining) > 0:
			s.task.CurrentMessage = fmt.Sprintf("partial: %d targets deferred for identity retry", len(remaining))
			s.appendBackgroundLogLocked(s.task.CurrentMessage)
		default:
			s.task.CurrentMessage = "本轮巡检完成"
		}
		err := s.saveStateLocked()
		s.mu.Unlock()
		return err
	}

	// 最耗时的操作：计算相似度（只读，不需要写门）
	assignments, err := s.buildAssignments(targets)
	if err != nil {
		return err
	}

	// Acquire write gate before writing to serialize with ApplySuggestion/MergePeople
	if s.writeGateFn != nil {
		gateRelease := s.writeGateFn()
		defer gateRelease()
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
	skippedReplace := 0
	writeErr := s.executeWrite(func() error {
		for _, target := range targets {
			items, ok := assignments[target.ID]
			if !ok {
				// primary 不可用目标：禁止空替换已有 pending。
				skippedReplace++
				continue
			}
			same, err := s.pendingSuggestionUnchanged(bgMergeSuggestionRepo, target.ID, items)
			if err != nil {
				return err
			}
			if same {
				continue
			}
			if err := bgMergeSuggestionRepo.ReplacePendingForTarget(target.ID, target.Category, items); err != nil {
				return err
			}
			processedPairs += len(items)
		}
		return nil
	})
	if writeErr != nil {
		return writeErr
	}

	s.task.ProcessedPairs += int64(processedPairs)
	// Only advance cursor if MarkDirty was not called during this slice.
	// A concurrent MarkDirty resets cursor to 0 and bumps DirtyGeneration; if we
	// detect the bump, keep cursor at 0 so the next run re-scans from scratch with
	// a fresh ANN index built after the concurrent write.
	if s.state.DirtyGeneration == dirtyGen {
		if retryOnly {
			// RetryOnly 批次不得落入全库扫描：按最新重试预算决定是否保持 Dirty。
			s.state.CursorTargetID = 0
			due, remaining, exhausted := classifyMergeSuggestionRetries(s.state.RetryTargets, now)
			if len(exhausted) > 0 {
				exIDs := make(map[uint]struct{}, len(exhausted))
				for _, e := range exhausted {
					exIDs[e.TargetID] = struct{}{}
				}
				s.state.RetryTargets = removeMergeSuggestionRetries(s.state.RetryTargets, exIDs)
				s.appendBackgroundLogLocked(fmt.Sprintf("放弃 %d 个耗尽重试预算的目标", len(exhausted)))
				s.markExhaustedRetriesNeedsRevalidationLocked(exhausted)
				due, remaining, _ = classifyMergeSuggestionRetries(s.state.RetryTargets, now)
			}
			s.state.RetryTargets = remaining
			dirty, ro := mergeSuggestionEndOfPassFlags(due, remaining)
			s.state.Dirty = dirty
			s.state.RetryOnly = ro
		} else {
			s.state.CursorTargetID = targets[len(targets)-1].ID
		}
	}
	msg := fmt.Sprintf("完成 %d 个目标人物巡检", len(targets))
	if skippedReplace > 0 {
		msg = fmt.Sprintf("完成 %d 个目标人物巡检（跳过 %d 个不可用目标的空替换）", len(targets), skippedReplace)
	}
	if retryOnly {
		if s.state.Dirty && s.state.RetryOnly {
			msg = fmt.Sprintf("partial: retry batch done, %d targets still due", len(dueMergeSuggestionRetryIDs(s.state.RetryTargets, now)))
		} else if len(s.state.RetryTargets) > 0 {
			msg = fmt.Sprintf("partial: retry batch done, %d targets deferred", len(s.state.RetryTargets))
		}
	}
	s.finishSliceLocked(now, processedPairs, msg)
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

	// Fail-closed: re-read person state before excluding. Reject if target or
	// any candidate is hidden.
	if err := s.rejectIfHidden(suggestion.TargetPersonID, candidateIDs); err != nil {
		return err
	}

	// 仅记录仍为 pending、实际被剔除的候选；已经 excluded 的重复请求不重复产生反馈。
	// 在写入前查询 pending 项，交集即为本次真正发生状态转换的候选。
	actuallyExcluded := s.pendingCandidatesInSuggestion(suggestionID, candidateIDs)
	snapshot := s.candidateScoreSnapshotForCandidates(suggestionID, actuallyExcluded)

	if err := s.executeWrite(func() error {
		for _, candidateID := range candidateIDs {
			if candidateID == 0 {
				continue
			}
			if err := s.cannotLinkRepo.Create(suggestion.TargetPersonID, candidateID); err != nil {
				return err
			}
		}
		return s.mergeSuggestionRepo.MarkItemsStatus(suggestionID, candidateIDs, model.PersonMergeSuggestionItemStatusExcluded)
	}); err != nil {
		return err
	}

	// 核心写入已提交：一次 API 操作只记录一条 merge_rejected 事件，不按候选逐条写。
	if len(actuallyExcluded) > 0 {
		s.recordFeedbackEvent(buildFeedbackEvent(
			repository.PeopleFeedbackEventMergeRejected,
			suggestion.TargetPersonID,
			actuallyExcluded,
			nil,
			peopleMergeSuggestionAlgorithmVersion,
			snapshot,
		))
	}
	return s.MarkDirty("exclude merge suggestion candidates")
}

// pendingCandidatesInSuggestion 返回指定建议中仍为 pending 且在 candidateIDs 列表内的候选。
// 用于 ExcludeCandidates 判定本次真正被剔除的候选，避免对已 excluded 的重复请求重复产生反馈。
func (s *personMergeSuggestionService) pendingCandidatesInSuggestion(suggestionID uint, candidateIDs []uint) []uint {
	items, err := s.mergeSuggestionRepo.GetItems(suggestionID, model.PersonMergeSuggestionItemStatusPending)
	if err != nil || len(items) == 0 {
		return nil
	}
	want := make(map[uint]struct{}, len(candidateIDs))
	for _, id := range candidateIDs {
		if id != 0 {
			want[id] = struct{}{}
		}
	}
	var out []uint
	for _, item := range items {
		if item == nil {
			continue
		}
		if _, ok := want[item.CandidatePersonID]; ok {
			out = append(out, item.CandidatePersonID)
		}
	}
	return out
}

// candidateScoreSnapshotForCandidates 从建议项中提取指定候选的相似度分数快照。
// 复用建议已有的分数，绝不现场重新计算。
func (s *personMergeSuggestionService) candidateScoreSnapshotForCandidates(suggestionID uint, candidateIDs []uint) map[string]interface{} {
	if len(candidateIDs) == 0 {
		return nil
	}
	items, err := s.mergeSuggestionRepo.GetItems(suggestionID, "")
	if err != nil {
		return nil
	}
	return candidateScoreSnapshot(items, candidateIDs)
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
	if suggestion.Status != model.PersonMergeSuggestionStatusPending {
		return fmt.Errorf("merge suggestion %d is not pending", suggestionID)
	}
	// 证据过期/重试耗尽等：禁止无条件接受，须先重跑巡检刷新建议。
	if suggestion.StaleReason != "" {
		return fmt.Errorf("merge suggestion %d needs revalidation (stale_reason=%s); rebuild merge suggestions before applying", suggestionID, suggestion.StaleReason)
	}

	// Fail-closed: re-read person state before applying. Reject if target or
	// any candidate is hidden.
	if err := s.rejectIfHidden(suggestion.TargetPersonID, candidateIDs); err != nil {
		return err
	}

	// Acquire write gate to exclude background clustering worker from
	// writing faces/people tables during the merge, preventing SQLite lock contention.
	if s.writeGateFn != nil {
		release := s.writeGateFn()
		defer release()
	}

	// 建议项已有的分数快照，复用不现场计算；合并后建议项状态变化，需在写入前提取。
	snapshot := s.candidateScoreSnapshotForCandidates(suggestionID, candidateIDs)

	// coreCommitted 标记核心人物合并已落库；后续 postMergeFn/MarkDirty 失败仍记录事件。
	coreCommitted := false
	defer func() {
		if coreCommitted {
			// 异步合并最终不会走到 ApplySuggestion（异步走 MergePeople），此处与
			// MergePeople 各自只记录一条 merge_confirmed，不重复。
			s.recordFeedbackEvent(buildFeedbackEvent(
				repository.PeopleFeedbackEventMergeConfirmed,
				suggestion.TargetPersonID,
				candidateIDs,
				nil,
				peopleMergeSuggestionAlgorithmVersion,
				snapshot,
			))
		}
	}()
	var affectedPhotoIDs []uint
	if err := s.executeWrite(func() error {
		var err error
		affectedPhotoIDs, err = s.personRepo.MergeInto(suggestion.TargetPersonID, candidateIDs)
		if err != nil {
			return err
		}
		return s.mergeSuggestionRepo.MarkItemsStatus(suggestionID, candidateIDs, model.PersonMergeSuggestionItemStatusMerged)
	}); err != nil {
		return err
	}
	coreCommitted = true

	// Post-merge cleanup (syncPersonState, cannot-link cleanup, RecomputeTopPersonCategory,
	// feedback recluster) — mirrors the cleanup in MergePeople.
	if s.postMergeFn != nil {
		s.postMergeFn(suggestion.TargetPersonID, candidateIDs, affectedPhotoIDs)
	}

	return s.MarkDirty("apply merge suggestion candidates")
}

func (s *personMergeSuggestionService) ListPending(page, pageSize int) ([]model.PersonMergeSuggestionResponse, int64, error) {
	suggestions, total, err := s.mergeSuggestionRepo.ListPending(page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	// Filter out suggestions whose target or any candidate is hidden.
	suggestions = s.filterHiddenSuggestions(suggestions)
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
	// Filter out suggestions whose target or any candidate is hidden.
	filtered := s.filterHiddenSuggestions([]*model.PersonMergeSuggestion{suggestion})
	if len(filtered) == 0 {
		return nil, nil
	}
	suggestion = filtered[0]
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

func (s *personMergeSuggestionService) listSuggestionTargets(cursorID uint) ([]*model.Person, error) {
	batchSize, err := s.currentBatchSize()
	if err != nil {
		return nil, err
	}
	bgPersonRepo, _, _, _ := s.bgRepos()
	return bgPersonRepo.ListMergeSuggestionTargets(cursorID, batchSize)
}

// listDueRetryTargets 仅返回到期且仍在预算内的重试目标（有界，不做全库扫描）。
// dropped 是 due 但已删除/隐藏/无脸/分类不合格的 ID，调用方必须移出 RetryTargets，避免永久忙循环。
func (s *personMergeSuggestionService) listDueRetryTargets() (targets []*model.Person, dropped []uint, err error) {
	s.mu.RLock()
	ids := dueMergeSuggestionRetryIDs(s.state.RetryTargets, time.Now())
	s.mu.RUnlock()
	if len(ids) == 0 {
		return nil, nil, nil
	}
	bgPersonRepo, _, _, _ := s.bgRepos()
	people, err := bgPersonRepo.ListByIDs(ids)
	if err != nil {
		return nil, nil, err
	}
	found := make(map[uint]*model.Person, len(people))
	for _, p := range people {
		if p != nil {
			found[p.ID] = p
		}
	}
	out := make([]*model.Person, 0, len(ids))
	for _, id := range ids {
		p := found[id]
		if p == nil || p.Hidden || p.FaceCount <= 0 {
			dropped = append(dropped, id)
			continue
		}
		switch p.Category {
		case model.PersonCategoryFamily, model.PersonCategoryFriend, model.PersonCategoryAcquaintance:
			out = append(out, p)
		default:
			dropped = append(dropped, id)
		}
	}
	return out, dropped, nil
}

// pendingSuggestionUnchanged 判断新计算结果与当前 pending 是否等价，避免无意义 obsolete+重建。
func (s *personMergeSuggestionService) pendingSuggestionUnchanged(
	repo repository.PersonMergeSuggestionRepository,
	targetID uint,
	items []model.PersonMergeSuggestionItem,
) (bool, error) {
	existing, err := repo.FindPendingByTarget(targetID)
	if err != nil {
		return false, err
	}
	if existing == nil {
		return len(items) == 0, nil
	}
	dbItems, err := repo.GetItems(existing.ID, model.PersonMergeSuggestionItemStatusPending)
	if err != nil {
		return false, err
	}
	a := make([]modelPersonMergeItemView, 0, len(dbItems))
	for _, it := range dbItems {
		if it == nil {
			continue
		}
		a = append(a, modelPersonMergeItemView{
			CandidateID: it.CandidatePersonID,
			Score:       it.SimilarityScore,
			MatchSource: it.MatchSource,
			Reason:      it.Reason,
			ProfileGen:  it.CandidateProfileGeneration,
		})
	}
	b := make([]modelPersonMergeItemView, 0, len(items))
	for _, it := range items {
		b = append(b, modelPersonMergeItemView{
			CandidateID: it.CandidatePersonID,
			Score:       it.SimilarityScore,
			MatchSource: it.MatchSource,
			Reason:      it.Reason,
			ProfileGen:  it.CandidateProfileGeneration,
		})
	}
	return pendingSuggestionItemsEquivalent(a, b), nil
}

// rejectIfHidden re-reads target and candidate person state from the database
// and returns ErrPersonHidden if any are hidden. This is a fail-closed check
// performed before any write operation on merge suggestions.
func (s *personMergeSuggestionService) rejectIfHidden(targetPersonID uint, candidateIDs []uint) error {
	allIDs := make([]uint, 0, 1+len(candidateIDs))
	allIDs = append(allIDs, targetPersonID)
	allIDs = append(allIDs, candidateIDs...)
	people, err := s.personRepo.ListByIDs(allIDs)
	if err != nil {
		return fmt.Errorf("check person hidden state: %w", err)
	}
	for _, p := range people {
		if p != nil && p.Hidden {
			return ErrPersonHidden
		}
	}
	return nil
}

// filterHiddenSuggestions removes suggestions whose target person is hidden.
// Candidate-level hidden filtering is handled by the item-level check in
// buildSuggestionResponses via ListByIDs; here we only do a fast target-level
// filter to avoid loading items for hidden-target suggestions.
func (s *personMergeSuggestionService) filterHiddenSuggestions(suggestions []*model.PersonMergeSuggestion) []*model.PersonMergeSuggestion {
	if len(suggestions) == 0 {
		return suggestions
	}
	targetIDs := make([]uint, 0, len(suggestions))
	for _, sug := range suggestions {
		if sug != nil && sug.TargetPersonID != 0 {
			targetIDs = append(targetIDs, sug.TargetPersonID)
		}
	}
	people, err := s.personRepo.ListByIDs(targetIDs)
	if err != nil {
		// On error, fail closed: filter out all suggestions to avoid showing
		// potentially stale suggestions with hidden targets.
		return nil
	}
	hiddenTargets := make(map[uint]bool, len(people))
	for _, p := range people {
		if p != nil && p.Hidden {
			hiddenTargets[p.ID] = true
		}
	}
	out := make([]*model.PersonMergeSuggestion, 0, len(suggestions))
	for _, sug := range suggestions {
		if sug == nil || !hiddenTargets[sug.TargetPersonID] {
			out = append(out, sug)
		}
	}
	return out
}

func (s *personMergeSuggestionService) currentBatchSize() (int, error) {
	batchSize := 100
	if s.config != nil && s.config.People.MergeSuggestionBatchSize > 0 {
		batchSize = s.config.People.MergeSuggestionBatchSize
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
	return 0.55
}

// MergeSuggestionThreshold 返回合并建议阈值（公开方法）
func (s *personMergeSuggestionService) MergeSuggestionThreshold() float64 {
	return s.mergeSuggestionThreshold()
}

// AttachThreshold 返回附加阈值（公开方法）
func (s *personMergeSuggestionService) AttachThreshold() float64 {
	return s.attachThreshold()
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
		// Skip items whose candidate person is hidden.
		if p := peopleByID[item.CandidatePersonID]; p != nil && p.Hidden {
			continue
		}
		itemsBySuggestion[item.SuggestionID] = append(itemsBySuggestion[item.SuggestionID], model.PersonMergeSuggestionItemResponse{
			ID:                item.ID,
			SuggestionID:      item.SuggestionID,
			CandidatePersonID: item.CandidatePersonID,
			SimilarityScore:   item.SimilarityScore,
			Rank:              item.Rank,
			Status:            item.Status,
			MatchSource:       item.MatchSource,
			Warning:           item.Warning,
			CandidatePerson:   toSuggestionPersonResponse(peopleByID[item.CandidatePersonID]),
		})
	}

	responses := make([]model.PersonMergeSuggestionResponse, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if suggestion == nil {
			continue
		}
		// Skip suggestions whose target is hidden (defensive: filterHiddenSuggestions
		// should already catch this, but fail-closed here too).
		if p := peopleByID[suggestion.TargetPersonID]; p != nil && p.Hidden {
			continue
		}
		// Skip suggestions that have no visible items after candidate filtering.
		if len(itemsBySuggestion[suggestion.ID]) == 0 {
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
	return s.executeWrite(func() error {
		return upsertMergeSuggestionState(s.db, string(payload))
	})
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
	entry := fmt.Sprintf("%s %s", time.Now().Format("2006-01-02 15:04:05"), message)
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

// markExhaustedRetriesNeedsRevalidationLocked 在已持锁时调用：给耗尽重试目标的 pending 写 stale_reason。
// 失败只记日志，不回滚重试移除（避免因标记失败再次制造忙循环）。
func (s *personMergeSuggestionService) markExhaustedRetriesNeedsRevalidationLocked(exhausted []mergeSuggestionRetryEntry) {
	if len(exhausted) == 0 || s.mergeSuggestionRepo == nil {
		return
	}
	ids := make([]uint, 0, len(exhausted))
	for _, e := range exhausted {
		if e.TargetID != 0 {
			ids = append(ids, e.TargetID)
		}
	}
	n, err := s.mergeSuggestionRepo.MarkPendingStaleReason(ids, model.PersonMergeStaleReasonRetryExhausted)
	if err != nil {
		s.appendBackgroundLogLocked(fmt.Sprintf("标记耗尽重试目标需重验失败: %v", err))
		return
	}
	if n > 0 {
		s.appendBackgroundLogLocked(fmt.Sprintf("已标记 %d 条 pending 建议需重验（重试预算耗尽）", n))
	}
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

// CalculateSimilarity 计算两个人物间的相似度（使用与合并建议相同的方法）
func (s *personMergeSuggestionService) CalculateSimilarity(personID1, personID2 uint) (float64, error) {
	// 获取两个人的原型人脸
	person1Faces, err := s.faceRepo.ListPrototypeEmbeddings([]uint{personID1}, peoplePrototypeCandidates)
	if err != nil {
		return 0, fmt.Errorf("failed to get faces for person %d: %w", personID1, err)
	}
	person2Faces, err := s.faceRepo.ListPrototypeEmbeddings([]uint{personID2}, peoplePrototypeCandidates)
	if err != nil {
		return 0, fmt.Errorf("failed to get faces for person %d: %w", personID2, err)
	}

	// 选择原型人脸
	protos1 := selectPersonPrototypesStatic(person1Faces, peoplePrototypeCount)
	protos2 := selectPersonPrototypesStatic(person2Faces, peoplePrototypeCount)

	// 解码嵌入向量
	emb1 := decodeFacesWithEmbeddings(protos1[personID1])
	emb2 := decodeFacesWithEmbeddings(protos2[personID2])

	if len(emb1) == 0 || len(emb2) == 0 {
		return -1, nil // 没有有效嵌入
	}

	// 计算双向相似度并取平均
	score1 := averageBestSuggestionSimilarity(emb1, emb2)
	score2 := averageBestSuggestionSimilarity(emb2, emb1)

	return (score1 + score2) / 2, nil
}
