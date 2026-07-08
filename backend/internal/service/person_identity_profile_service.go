package service

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// identityProfileBackfillStateKey 是 app_config 中持久化 backfill 游标的键。
const identityProfileBackfillStateKey = "people.identity_profile.backfill_state"

// identityProfileLegacyMode / 允许后台构建的模式常量。
const (
	identityProfileModeLegacy  = "legacy"
	identityProfileModeShadow  = "shadow"
	identityProfileModeRescue  = "rescue"
	identityProfileModePrimary = "primary"
)

// identityProfileErrMsgMax 限制写入数据库的错误信息长度，避免异常大内容落库。
const identityProfileErrMsgMax = 500

// IdentityProfileInvalidation 是一次身份画像统一失效请求。所有改变 faces.person_id、
// 人物成员组成或人物存续状态的业务路径都应构造该请求并通过 PeopleService 统一 hook 触发，
// 禁止直接、零散地操作画像 Repository。
//
// dirty 与 deleted 同时存在某人物时以 deleted 为准；ResetAll=true 时忽略 ID 列表，
// 清空全部派生画像。Reason 必须取自稳定白名单，不接受任意业务文本。
type IdentityProfileInvalidation struct {
	DirtyPersonIDs   []uint
	DeletedPersonIDs []uint
	ResetAll         bool
	Reason           string
}

// Invalidate 在非 legacy 模式下原子应用一次身份画像失效：
//
//  1. 清洗请求（去重/过滤 0/升序，deleted 优先，reason 白名单校验）。
//  2. dirty/deleted 人物立即调用 ANN InvalidatePerson；ResetAll 时调用 ANN InvalidateAll。
//  3. 通过 WriteQueue 调用 Repository 原子失效。
//  4. 成功后请求 ANN 后台重建。
//
// ANN 先失效实现 fail closed：即使 SQLite 写入暂时失败，当前进程也不能继续返回陈旧人物中心。
// 持久化失败时 ANN 保持不可用或对应人物失效，不恢复旧中心，不回滚已成功的业务身份变更。
// 返回持久化错误供调用方记录脱敏日志（不重试、不伪装业务回滚）。
//
// legacy 模式直接返回 nil：不访问 Repository、不操作 ANN、不分配大型 map、不请求重建、不产生日志。
func (s *personIdentityProfileService) Invalidate(invalidation IdentityProfileInvalidation) error {
	if s.mode == identityProfileModeLegacy {
		return nil
	}
	reason := identityProfileInvalidationReason(invalidation.Reason)
	req := repository.IdentityProfileInvalidationRequest{
		DirtyPersonIDs:   invalidation.DirtyPersonIDs,
		DeletedPersonIDs: invalidation.DeletedPersonIDs,
		ResetAll:         invalidation.ResetAll,
		Reason:           reason,
	}
	normalized, needApply := normalizeInvalidationRequestForService(req)
	if !needApply {
		return nil
	}

	// 1. ANN 先失效（fail closed）。
	if s.ann != nil {
		if normalized.ResetAll {
			s.ann.InvalidateAll()
		} else {
			for _, pid := range normalized.DeletedPersonIDs {
				s.ann.InvalidatePerson(pid)
			}
			for _, pid := range normalized.DirtyPersonIDs {
				s.ann.InvalidatePerson(pid)
			}
		}
	}

	// 2. 通过 WriteQueue 串行化 Repository 原子失效。
	writeErr := s.executeWrite(func() error {
		return s.repo.ApplyInvalidation(normalized)
	})
	if writeErr != nil {
		// 持久化失败：ANN 保持失效/不可用，不恢复旧中心，记录脱敏错误类别（不含 ID）。
		logger.Warnf("identity profile invalidate failed: reason=%s dirty=%d deleted=%d reset=%v err_category=%T",
			normalized.Reason, len(normalized.DirtyPersonIDs), len(normalized.DeletedPersonIDs), normalized.ResetAll, writeErr)
		return writeErr
	}

	// 3. 成功后请求 ANN 后台重建（下一个后台切片从数据库活动中心重建）。
	if s.ann != nil {
		s.ann.RequestRebuild()
	}
	return nil
}

// identityProfileInvalidationReason 将任意 reason 字符串映射为 repository 稳定枚举。
// 未知/空 reason 返回空枚举（不落库 dirty_reason，但仍执行失效）。
func identityProfileInvalidationReason(reason string) repository.IdentityProfileInvalidationReason {
	r := repository.IdentityProfileInvalidationReason(reason)
	return r
}

// normalizeInvalidationRequestForService 复用 repository 的清洗逻辑，使 service 与
// repository 看到一致的请求语义。返回清洗后的请求与是否需要执行。
func normalizeInvalidationRequestForService(req repository.IdentityProfileInvalidationRequest) (repository.IdentityProfileInvalidationRequest, bool) {
	// 复用 repository 包的清洗（去重/过滤 0/升序/deleted 优先/reason 白名单/截断）。
	// 通过构造等价请求调用同一函数，避免逻辑重复。
	return repository.NormalizeInvalidationRequest(req)
}

// identityProfileDefaultEmbeddingModel 是未配置 ML 端点时使用的占位 embedding 标识。
// embedding model 标识用于检测 embedding 来源变化并触发 backfill 重置。
const identityProfileDefaultEmbeddingModel = "default"

// PersonIdentityProfileService 维护人物多中心身份画像的后台构建、backfill 游标与读取。
//
// legacy 模式下完全 no-op：不启动后台 goroutine、不读写 backfill 状态、不创建 dirty
// profile、不构建画像、不访问任何 Repository、不打开专用后台数据库连接。MarkDirty 直接
// 返回成功但不写库；GetActive 不进入现有聚类调用链。
//
// shadow/rescue/primary 模式下提供低速、可恢复、单批有界的后台构建：
//   - 分批发现尚未构建画像的人物并标记 dirty（持久化游标，重启可续）；
//   - 一次 slice 最多构建一个 dirty 人物画像；
//   - 原子激活新 generation，保留 active + 最近一个历史 generation；
//   - 失败时 MarkFailed 但保留旧画像，不阻塞后续人物；
//   - 人物中途被删除时清理派生画像，不视为系统级失败。
type PersonIdentityProfileService interface {
	// MarkDirty 标记人物画像待重建。legacy 直接返回不写库；其他模式去重后调用 Repository。
	// 本任务只实现服务能力，暂不接入人物操作。
	MarkDirty(personIDs []uint, reason string) error
	// Invalidate 统一应用一次身份画像失效。legacy 直接返回不写库不操作 ANN。
	// 非 legacy 模式按 fail-closed 顺序执行（ANN 先失效 → Repository 原子失效 → 请求重建）。
	Invalidate(invalidation IdentityProfileInvalidation) error
	// RunBackgroundSlice 执行一次有界后台工作：推进一小批 backfill 游标 + 构建最多一个
	// dirty 人物画像 + 清理其过期 generation。受 cooldown 约束，禁止内部 time.Sleep。
	RunBackgroundSlice() error
	// GetActive 读取人物活动 generation 的完整构建。legacy 返回 nil 且不访问 Repository。
	GetActive(personID uint) (*model.PersonIdentityProfileBuild, error)
	// GetStats 返回画像运行统计。legacy 返回零值结构且不访问 Repository。
	GetStats() (*model.PersonIdentityProfileStats, error)
	// GetOperationalStats 返回完整运行状态视图（profile/center/member/backfill/ANN/decision）。
	// legacy 仅返回 mode 与零值运行状态，不访问 Repository/ANN/AppConfig/decision 仓库。
	// decisionRepo 为 nil 时不查询决策汇总（仍返回零值 decisions 字段）。
	GetOperationalStats(decisionRepo repository.PeopleIdentityDecisionRepository) (*model.IdentityProfileOperationalStatsResponse, error)
	// ListRecentDecisions 将最近 limit 条决策遥测转为只读 DTO（过滤敏感字段）。
	// limit 由 handler 限制为 1–200；service 不再二次截断。
	ListRecentDecisions(limit int, decisionRepo repository.PeopleIdentityDecisionRepository) ([]model.IdentityDecisionResponse, error)
	// Mode 返回当前身份画像模式（legacy/shadow/rescue/primary）。
	Mode() string
}

// identityProfileBuilderIface 是 builder 的可注入接口，便于测试替换。
// 生产实现为 *identityProfileBuilder（纯函数，无副作用）。
type identityProfileBuilderIface interface {
	Build(personID uint, faces []*model.Face) (*model.PersonIdentityProfileBuild, error)
}

// identityProfileBackfillState 是持久化到 app_config 的 backfill 游标状态。
type identityProfileBackfillState struct {
	CursorPersonID   uint      `json:"cursor_person_id"`
	Completed        bool      `json:"completed"`
	AlgorithmVersion string    `json:"algorithm_version"`
	EmbeddingModel   string    `json:"embedding_model"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type personIdentityProfileService struct {
	mode           string
	batchSize      int
	cooldownMs     int
	workers        int
	dirtyBatch     int
	sliceBudgetMs  int
	annDeltaThresh float64
	embeddingModel string
	// algorithmVersion 与 builder 内部常量保持一致，用于 backfill 版本比对。
	algorithmVersion string

	repo          repository.PersonIdentityProfileRepository // 主库：前台 MarkDirty/GetActive/GetStats
	configService ConfigService
	writeQueue    *database.WriteQueue
	builder       identityProfileBuilderIface

	// background 专用连接池（shadow/rescue/primary）。仅在 enabled 时可用；
	// 创建失败则 enabled=false，禁止退回共享 API 连接池执行重型 backfill。
	bgDB       *gorm.DB
	bgRepo     repository.PersonIdentityProfileRepository
	bgFaceRepo repository.FaceRepository
	enabled    bool

	// ann 是身份中心 ANN 缓存。仅非 legacy 模式初始化；首次/按需重建只在后台切片中执行。
	// legacy 模式下为 nil，保证对 ANN、Repository 和现有聚类调用链完全 no-op。
	ann *identityProfileANN

	// coordinator 是后台构建轻量调度器（有界并发 + 串行写入 + 批末 ANN 合并）。
	// legacy 模式下为 nil；RunBackgroundSlice 通过它执行有界 slice。
	coordinator *identityProfileCoordinator

	// nowFn 注入时钟，测试无需真实 sleep。
	nowFn func() time.Time

	mu        sync.Mutex
	lastRunAt time.Time
	// stateLoaded 标记 backfill 状态是否已从持久化加载（legacy 模式永不加载）。
	stateLoaded bool

	// ANN 最近构建状态（只读运行状态接口）。读取 stats 不触发 rebuild。
	// 错误只保存脱敏类别，不保存路径、SQL、endpoint 或 embedding 内容。
	// 时间戳与耗时统一使用 nowFn，便于测试注入确定值。
	annStatsMu            sync.RWMutex
	lastANNBuildStatus    string // success / failed / never（空值在 buildANNStatsResponse 归一为 never）
	lastANNBuildStartedAt *time.Time
	lastANNBuildEndedAt   *time.Time
	lastANNBuildDuration  time.Duration
	lastANNBuildError     string
	lastANNBuildCenters   int
}

// NewPersonIdentityProfileService 构造身份画像服务。
//
// bgDB 为专用后台连接池；为 nil 时（创建失败）服务仍可前台 MarkDirty/GetActive/GetStats，
// 但禁用后台构建。embedding model 标识从 cfg.People.MLEndpoint 派生（embedding 来源），
// 未配置时使用占位值；测试可直接构造结构体以注入确定值。
func NewPersonIdentityProfileService(
	repo repository.PersonIdentityProfileRepository,
	configService ConfigService,
	cfg *config.Config,
	bgDB *gorm.DB,
	writeQueue *database.WriteQueue,
) PersonIdentityProfileService {
	mode := identityProfileModeLegacy
	batchSize := 0
	cooldownMs := 0
	workers := 0
	dirtyBatch := 0
	sliceBudgetMs := 0
	annDeltaThresh := 0.0
	embeddingModel := identityProfileDefaultEmbeddingModel
	var builderCfg identityProfileBuilderConfig
	if cfg != nil {
		mode = cfg.People.IdentityProfileMode
		batchSize = cfg.People.IdentityProfileBatchSize
		cooldownMs = cfg.People.IdentityProfileCooldownMs
		workers = cfg.People.IdentityProfileBuildWorkers
		dirtyBatch = cfg.People.IdentityProfileDirtyBatchSize
		sliceBudgetMs = cfg.People.IdentityProfileSliceBudgetMs
		annDeltaThresh = cfg.People.IdentityProfileAnnRebuildDeltaThreshold
		if cfg.People.MLEndpoint != "" {
			embeddingModel = cfg.People.MLEndpoint
		}
		builderCfg = identityProfileBuilderConfig{
			MaxCenters:      cfg.People.IdentityProfileMaxCenters,
			MinCenterFaces:  cfg.People.IdentityProfileMinCenterFaces,
			MinCenterPhotos: cfg.People.IdentityProfileMinCenterPhotos,
		}
	}
	if batchSize <= 0 {
		batchSize = 25
	}
	if cooldownMs <= 0 {
		cooldownMs = 500
	}
	if workers <= 0 {
		workers = 2
	}
	if dirtyBatch <= 0 {
		dirtyBatch = 10
	}
	if sliceBudgetMs <= 0 {
		sliceBudgetMs = 5000
	}
	if annDeltaThresh <= 0 || annDeltaThresh > 1 {
		annDeltaThresh = 0.75
	}

	svc := &personIdentityProfileService{
		mode:             mode,
		batchSize:        batchSize,
		cooldownMs:       cooldownMs,
		workers:          workers,
		dirtyBatch:       dirtyBatch,
		sliceBudgetMs:    sliceBudgetMs,
		annDeltaThresh:   annDeltaThresh,
		embeddingModel:   embeddingModel,
		algorithmVersion: identityProfileAlgorithmVersion,
		repo:             repo,
		configService:    configService,
		writeQueue:       writeQueue,
		builder:          NewIdentityProfileBuilder(builderCfg),
		nowFn:            time.Now,
	}

	if mode != identityProfileModeLegacy && bgDB != nil {
		svc.bgDB = bgDB
		svc.bgRepo = repository.NewPersonIdentityProfileRepository(bgDB)
		svc.bgFaceRepo = repository.NewFaceRepository(bgDB)
		svc.enabled = true
		// ANN 仅在非 legacy 模式初始化；首次构建延迟到后台切片，不阻塞构造与 HTTP 请求。
		svc.ann = newIdentityProfileANN(embeddingModel)
		svc.ann.rebuildRequested.Store(true) // 标记需要首次构建
		// 协调器在后台 DB 可用时构造；foregroundBusyFn 由 service.go 装配时注入。
		svc.coordinator = newIdentityProfileCoordinator(svc, workers, dirtyBatch, sliceBudgetMs, annDeltaThresh)
		// 加载并校验 backfill 状态；版本不一致时重置。
		svc.loadAndAlignBackfillState()
	} else if mode != identityProfileModeLegacy && bgDB == nil {
		logger.Warnf("Identity profile background DB unavailable; background building disabled (mode=%s)", mode)
		// 即使后台 DB 不可用，ANN 仍初始化（始终 ready=false），以便模式判断一致；
		// 不执行任何后台重建。
		svc.ann = newIdentityProfileANN(embeddingModel)
		svc.ann.rebuildRequested.Store(true)
	}

	return svc
}

// SetBackgroundDB 在服务构造后注入专用后台连接池并启用后台构建。
// 仅供 service.go 在 bgDB 创建成功后调用；重复调用以最新为准。
func (s *personIdentityProfileService) SetBackgroundDB(bgDB *gorm.DB) {
	if bgDB == nil || s.mode == identityProfileModeLegacy {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bgDB = bgDB
	s.bgRepo = repository.NewPersonIdentityProfileRepository(bgDB)
	s.bgFaceRepo = repository.NewFaceRepository(bgDB)
	s.enabled = true
	if s.ann == nil {
		s.ann = newIdentityProfileANN(s.embeddingModel)
		s.ann.rebuildRequested.Store(true)
	}
	if !s.stateLoaded {
		s.loadAndAlignBackfillStateLocked()
	}
	// 后台 DB 注入成功后构造协调器（若尚未构造）。
	if s.coordinator == nil {
		s.coordinator = newIdentityProfileCoordinator(s, s.workers, s.dirtyBatch, s.sliceBudgetMs, s.annDeltaThresh)
	}
}

// SetForegroundBusyFn 注入前台让路判定函数。非 legacy 模式下由 service.go 装配时调用一次，
// 将 peopleService 的前台写状态接入身份画像协调器，避免后台构建与前台 merge/split/move 争抢资源。
// legacy 模式下协调器为 nil，直接返回。
func (s *personIdentityProfileService) SetForegroundBusyFn(fn func() bool) {
	if s.coordinator == nil {
		return
	}
	s.coordinator.setForegroundBusyFn(fn)
}

// loadAndAlignBackfillState 加载持久化 backfill 状态，并在算法/embedding 版本变化时重置。
func (s *personIdentityProfileService) loadAndAlignBackfillState() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadAndAlignBackfillStateLocked()
}

func (s *personIdentityProfileService) loadAndAlignBackfillStateLocked() {
	if s.configService == nil {
		s.stateLoaded = true
		return
	}
	raw, err := s.configService.GetWithDefault(identityProfileBackfillStateKey, "")
	if err != nil {
		logger.Warnf("Failed to load identity profile backfill state: %v", err)
		s.stateLoaded = true
		return
	}
	s.stateLoaded = true
	if raw == "" {
		return
	}
	var st identityProfileBackfillState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		logger.Warnf("Failed to parse identity profile backfill state: %v", err)
		return
	}
	// 算法版本或 embedding model 变化 → 重置 backfill。
	if st.AlgorithmVersion != s.algorithmVersion || st.EmbeddingModel != s.embeddingModel {
		_ = s.saveBackfillStateLocked(identityProfileBackfillState{
			AlgorithmVersion: s.algorithmVersion,
			EmbeddingModel:   s.embeddingModel,
			UpdatedAt:        s.nowFn(),
		})
		return
	}
}

func (s *personIdentityProfileService) saveBackfillStateLocked(st identityProfileBackfillState) error {
	if s.configService == nil {
		return nil
	}
	payload, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.configService.Set(identityProfileBackfillStateKey, string(payload))
}

// Mode 返回当前身份画像模式。
func (s *personIdentityProfileService) Mode() string {
	return s.mode
}

// ANN 返回身份中心 ANN 缓存与当前 embedding 模型签名。legacy 模式下 ANN 为 nil。
// 仅供 Task 10 合并建议服务在非 legacy 模式下装配 profile 相似度 provider 使用；
// 调用方不得直接驱动 ANN 重建或修改状态。返回的 (nil, "") 表示画像不可用，调用方须回退 legacy。
func (s *personIdentityProfileService) ANN() (*identityProfileANN, string) {
	if s.mode == identityProfileModeLegacy {
		return nil, ""
	}
	return s.ann, s.embeddingModel
}

// MarkDirty 标记人物画像待重建。
func (s *personIdentityProfileService) MarkDirty(personIDs []uint, reason string) error {
	if s.mode == identityProfileModeLegacy {
		return nil // no-op：不写数据库
	}
	ids := dedupPersonIDs(personIDs)
	if len(ids) == 0 {
		return nil
	}
	return s.executeWrite(func() error {
		return s.repo.MarkDirty(ids, reason)
	})
}

// RunBackgroundSlice 执行一次有界后台工作。
func (s *personIdentityProfileService) RunBackgroundSlice() error {
	if s.mode == identityProfileModeLegacy {
		return nil
	}
	if !s.enabled {
		return nil // 后台 DB 不可用，禁用构建
	}

	now := s.nowFn()

	// cooldown：未到最小间隔直接返回。lastRunAt 在通过检查后立即更新，
	// 保证失败后也不会进入紧密重试循环。
	s.mu.Lock()
	if !s.lastRunAt.IsZero() && now.Sub(s.lastRunAt) < time.Duration(s.cooldownMs)*time.Millisecond {
		s.mu.Unlock()
		return nil
	}
	s.lastRunAt = now
	s.mu.Unlock()

	// 1. 推进一小批 backfill 游标。
	if err := s.advanceBackfill(now); err != nil {
		return err
	}

	// 2. 通过协调器执行一个有界构建 slice（有界并发构建 + 串行写入 + 批末 ANN 合并）。
	//    协调器在启动新批次前检查前台让路；已开始的小批次允许完成。
	if s.coordinator != nil {
		s.coordinator.runSlice()
	}

	return nil
}

// rebuildANNFromCoordinator 由协调器统一触发的一次 full rebuild。
// 复用现有 rebuildANNIfNeeded 逻辑，确保 full rebuild 只由协调器触发，避免多路径重复请求。
// 重建成功/失败状态由 rebuildANNIfNeeded 内部记录到 annStats（脱敏），协调器据此暴露 stats。
func (s *personIdentityProfileService) rebuildANNFromCoordinator() {
	s.rebuildANNIfNeeded()
}

// rebuildANNIfNeeded 在 rebuildRequested 时从活动中心重建 ANN snapshot。
// 使用专用后台仓库查询，仅返回当前模型签名的活动中心。重建失败只标记不可用并请求重试，
// 不影响画像 generation 与现有聚类。同时记录脱敏构建状态供只读运行状态接口暴露。
func (s *personIdentityProfileService) rebuildANNIfNeeded() {
	if s.ann == nil || !s.ann.RebuildRequested() {
		return
	}
	if s.bgRepo == nil {
		return
	}
	start := s.nowFn()
	centers, err := s.bgRepo.ListAllActiveCenters(s.embeddingModel)
	if err != nil {
		logger.Warnf("Identity profile ANN: list active centers failed: %v", err)
		// 查询失败：保持 rebuildRequested=true，下次切片重试。计为构建失败。
		s.recordANNBuildFailure(start, annBuildErrListActiveCentersFailed)
		s.ann.RequestRebuild()
		return
	}
	if err := s.ann.Rebuild(centers, s.embeddingModel); err != nil {
		category := classifyANNBuildError(err)
		logger.Warnf("Identity profile ANN: rebuild failed category=%s elapsed=%dms",
			category, s.nowFn().Sub(start).Milliseconds())
		// Rebuild 内部已标记 unavailable + rebuildRequested。
		s.recordANNBuildFailure(start, category)
		return
	}
	s.recordANNBuildSuccess(start, len(centers))
	logger.Infof("Identity profile ANN: rebuild succeeded centers=%d generation=%d elapsed=%dms",
		len(centers), s.ann.snapshotGeneration.Load(), s.nowFn().Sub(start).Milliseconds())
}

// annBuildErr* 是脱敏的 ANN 构建失败类别，不暴露原始错误、路径或 SQL。
const (
	annBuildErrListActiveCentersFailed = "list_active_centers_failed"
	annBuildErrModelMismatch           = "model_mismatch"
	annBuildErrInvalidCenter           = "invalid_center"
	annBuildErrEmbeddingDecodeFailed   = "embedding_decode_failed"
	annBuildErrDimensionMismatch       = "dimension_mismatch"
	annBuildErrAnnBuildFailed          = "ann_build_failed"
)

// identityProfileAnnErrMaxLen 限制暴露到 API 的脱敏错误类别长度。
const identityProfileAnnErrMaxLen = 200

// classifyANNBuildError 将 ANN Rebuild 错误映射为脱敏类别。未知错误统一归为 ann_build_failed，
// 不暴露原始错误内容。ListAllActiveCenters 返回的查询错误单独归为 list_active_centers_failed。
func classifyANNBuildError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, errANNModelMismatch):
		return annBuildErrModelMismatch
	case errors.Is(err, errANNInvalidCenter):
		return annBuildErrInvalidCenter
	default:
		// 进一步根据错误消息判定 embedding/dimension 类别（buildIndex 返回非哨兵错误）。
		msg := err.Error()
		if strings.Contains(msg, "embedding decode failed") {
			return annBuildErrEmbeddingDecodeFailed
		}
		if strings.Contains(msg, "dim mismatch") {
			return annBuildErrDimensionMismatch
		}
		return annBuildErrAnnBuildFailed
	}
}

// recordANNBuildSuccess 清空错误并记录成功构建状态/时间/耗时/中心数。
func (s *personIdentityProfileService) recordANNBuildSuccess(start time.Time, centers int) {
	duration := s.nowFn().Sub(start)
	if duration < 0 {
		duration = 0
	}
	end := s.nowFn()
	s.annStatsMu.Lock()
	s.lastANNBuildStatus = annBuildStatusSuccess
	s.lastANNBuildStartedAt = &start
	s.lastANNBuildEndedAt = &end
	s.lastANNBuildDuration = duration
	s.lastANNBuildError = ""
	s.lastANNBuildCenters = centers
	s.annStatsMu.Unlock()
}

// recordANNBuildFailure 记录失败耗时与脱敏错误类别（最长 200 字符），不清空上次成功时间。
func (s *personIdentityProfileService) recordANNBuildFailure(start time.Time, category string) {
	duration := s.nowFn().Sub(start)
	if duration < 0 {
		duration = 0
	}
	if len(category) > identityProfileAnnErrMaxLen {
		category = category[:identityProfileAnnErrMaxLen]
	}
	end := s.nowFn()
	s.annStatsMu.Lock()
	s.lastANNBuildStatus = annBuildStatusFailed
	s.lastANNBuildStartedAt = &start
	s.lastANNBuildEndedAt = &end
	s.lastANNBuildDuration = duration
	s.lastANNBuildError = category
	s.annStatsMu.Unlock()
}

// advanceBackfill 发现一批尚未构建画像的人物并标记 dirty，推进持久化游标。
// 只有 MarkDirty 成功后才推进游标；批次失败时游标保持不变。
func (s *personIdentityProfileService) advanceBackfill(now time.Time) error {
	st := s.currentBackfillState()

	// 版本不一致 → 重置（防御性，构造时已对齐，这里覆盖运行期理论上的配置漂移）。
	versionChanged := st.AlgorithmVersion != s.algorithmVersion || st.EmbeddingModel != s.embeddingModel
	if versionChanged {
		st = identityProfileBackfillState{
			AlgorithmVersion: s.algorithmVersion,
			EmbeddingModel:   s.embeddingModel,
			UpdatedAt:        now,
		}
		if err := s.persistBackfillState(st); err != nil {
			return err
		}
	}

	if st.Completed {
		return nil // 已完成且版本一致，不重复全量扫描
	}

	ids, err := s.bgRepo.ListBackfillPersonIDs(st.CursorPersonID, s.batchSize)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		// 无更多待 backfill 人物 → 标记完成。
		st.Completed = true
		st.UpdatedAt = now
		return s.persistBackfillState(st)
	}

	// 标记 dirty；失败则游标保持不变。
	if err := s.executeWrite(func() error {
		return s.bgRepo.MarkDirty(ids, "backfill")
	}); err != nil {
		return err
	}

	// 成功后才推进游标。
	st.CursorPersonID = ids[len(ids)-1]
	st.AlgorithmVersion = s.algorithmVersion
	st.EmbeddingModel = s.embeddingModel
	st.UpdatedAt = now
	return s.persistBackfillState(st)
}

// activateANN 从数据库读取人物活动 generation 的真实中心并接入 ANN delta。
// ANN 更新失败只标记 ANN 不可用并请求重建，不回滚已成功激活的数据库 generation。
func (s *personIdentityProfileService) activateANN(personID uint) {
	if s.ann == nil {
		return
	}
	build, err := s.bgRepo.GetActive(personID)
	if err != nil {
		logger.Warnf("Identity profile ANN: get active for person %d failed: %v", personID, err)
		s.ann.RequestRebuild()
		return
	}
	if build == nil || build.Profile == nil || build.Profile.ActiveGeneration == 0 {
		// 无活动 generation（如构建为空）→ 失效该人物的旧中心即可。
		s.ann.InvalidatePerson(personID)
		return
	}
	if err := s.ann.Activate(personID, build.Profile.ActiveGeneration, build.Centers); err != nil {
		// delta 更新失败（容量上限或非法中心）：标记不可用并请求完整重建。
		logger.Warnf("Identity profile ANN: activate person %d failed: %v", personID, err)
		s.ann.RequestRebuild()
		return
	}
}

// cleanupDeletedPerson 清理被删除人物的派生画像数据。
func (s *personIdentityProfileService) cleanupDeletedPerson(personID uint) {
	if s.ann != nil {
		s.ann.InvalidatePerson(personID)
	}
	if err := s.executeWrite(func() error {
		return s.bgRepo.DeleteByPersonIDs([]uint{personID})
	}); err != nil {
		logger.Warnf("Identity profile: cleanup deleted person %d failed: %v", personID, err)
	}
}

// markFailed 记录构建失败原因，截断长度，保留旧 active generation。
func (s *personIdentityProfileService) markFailed(personID uint, message string) {
	msg := truncateMessage(message, identityProfileErrMsgMax)
	if err := s.executeWrite(func() error {
		return s.bgRepo.MarkFailed(personID, msg)
	}); err != nil {
		logger.Warnf("Identity profile: mark failed for person %d: %v", personID, err)
	}
}

// GetActive 读取人物活动 generation 的完整构建。legacy 返回 nil 且不访问 Repository。
func (s *personIdentityProfileService) GetActive(personID uint) (*model.PersonIdentityProfileBuild, error) {
	if s.mode == identityProfileModeLegacy {
		return nil, nil
	}
	return s.repo.GetActive(personID)
}

// GetStats 返回画像运行统计。legacy 返回零值结构且不访问 Repository。
func (s *personIdentityProfileService) GetStats() (*model.PersonIdentityProfileStats, error) {
	if s.mode == identityProfileModeLegacy {
		return &model.PersonIdentityProfileStats{}, nil
	}
	stats, err := s.repo.GetStats()
	if err != nil {
		return nil, err
	}
	// 补充 backfill 游标与完成标志。
	st := s.currentBackfillState()
	stats.BackfillCursor = st.CursorPersonID
	stats.BackfillCompleted = st.Completed
	return stats, nil
}

// identityProfileDecisionWindowHours 是决策汇总的固定时间窗口（最近 24 小时）。
const identityProfileDecisionWindowHours = 24

// identityProfileDecisionCenterIDsMax 是单条决策响应返回的最多 center ID 数。
const identityProfileDecisionCenterIDsMax = 32

// GetOperationalStats 返回完整运行状态视图。legacy 仅返回 mode 与零值运行状态，
// 不访问 Repository/ANN/AppConfig/decision 仓库；显式调用 decisions 接口时允许读取
// 以前保留的 decision 历史（属于用户触发的只读查询，不属于后台画像负载）。
func (s *personIdentityProfileService) GetOperationalStats(decisionRepo repository.PeopleIdentityDecisionRepository) (*model.IdentityProfileOperationalStatsResponse, error) {
	if s.mode == identityProfileModeLegacy {
		return &model.IdentityProfileOperationalStatsResponse{
			Mode: identityProfileModeLegacy,
			ANN:  model.IdentityANNStats{LastBuildStatus: annBuildStatusNever},
		}, nil
	}

	stats, err := s.repo.GetStats()
	if err != nil {
		return nil, err
	}
	st := s.currentBackfillState()
	stats.BackfillCursor = st.CursorPersonID
	stats.BackfillCompleted = st.Completed

	resp := &model.IdentityProfileOperationalStatsResponse{
		Mode: s.mode,
		Profiles: model.IdentityProfileCountStats{
			Total:    stats.Total,
			Ready:    stats.Ready,
			Dirty:    stats.Dirty,
			Building: stats.Building,
			Failed:   stats.Failed,
		},
		Centers: model.IdentityCenterStats{
			Total:             stats.CenterTotal,
			Active:            stats.CenterActive,
			Confirmed:         stats.CenterConfirmed,
			AveragePerProfile: stats.CenterAvgPerProfile,
			MaxPerProfile:     stats.CenterMaxPerProfile,
		},
		Members: model.IdentityMemberStats{
			Total:     stats.MemberTotal,
			Accepted:  stats.MemberAccepted,
			Candidate: stats.MemberCandidate,
			Excluded:  stats.MemberExcluded,
		},
		Backfill: model.IdentityBackfillStats{
			TotalPeople: stats.TotalPeople,
			Cursor:      stats.BackfillCursor,
			Completed:   stats.BackfillCompleted,
		},
		ANN: s.buildANNStatsResponse(),
		Coordinator: s.coordinator.toStatsResponse(),
		Decisions: model.IdentityDecisionStats{
			WindowHours: identityProfileDecisionWindowHours,
		},
	}

	if decisionRepo != nil {
		since := s.nowFn().Add(-identityProfileDecisionWindowHours * time.Hour)
		summary, err := decisionRepo.GetSummarySince(since)
		if err != nil {
			return nil, err
		}
		resp.Decisions.Total = summary.Total
		resp.Decisions.Agree = summary.Agree
		resp.Decisions.Disagree = summary.Disagree
		resp.Decisions.LegacyMissProfileHit = summary.LegacyMissProfileHit
		resp.Decisions.LegacyMissProfileMiss = summary.LegacyMissProfileMiss
		resp.Decisions.ProfileMiss = summary.ProfileMiss
		resp.Decisions.ProfileUnavailable = summary.ProfileUnavailable
		resp.Decisions.ProfileBlocked = summary.ProfileBlocked
		resp.Decisions.RescueApplied = summary.RescueApplied
	}

	return resp, nil
}

// buildANNStatsResponse 从 ANN 只读快照与最近构建状态构造响应。读取 stats 不触发 rebuild。
func (s *personIdentityProfileService) buildANNStatsResponse() model.IdentityANNStats {
	if s.ann == nil {
		// ann 为 nil（legacy 或未启用后台 DB）：返回零值，状态显式为 never。
		return model.IdentityANNStats{LastBuildStatus: annBuildStatusNever}
	}
	snap := s.ann.Stats(s.embeddingModel)

	s.annStatsMu.RLock()
	status := s.lastANNBuildStatus
	startedAt := s.lastANNBuildStartedAt
	endedAt := s.lastANNBuildEndedAt
	lastDuration := s.lastANNBuildDuration
	lastErr := s.lastANNBuildError
	lastCenters := s.lastANNBuildCenters
	s.annStatsMu.RUnlock()

	if status == "" {
		status = annBuildStatusNever
	}

	resp := model.IdentityANNStats{
		Ready:             snap.Ready,
		RebuildRequested:  snap.RebuildRequested,
		Generation:        snap.Generation,
		SnapshotNodes:     snap.SnapshotNodes,
		DeltaNodes:        snap.DeltaNodes,
		DeltaMax:          snap.DeltaMax,
		InvalidNodes:      snap.InvalidNodes,
		ActiveGenerations: snap.ActiveGenerations,
		Unavailable:       snap.Unavailable,

		LastBuildStatus:    status,
		LastBuildError:     lastErr,
		LastBuildStartedAt: startedAt,
		LastBuildEndedAt:   endedAt,
		LastBuildCenters:   lastCenters,
	}
	if lastDuration > 0 {
		resp.LastBuildDurationMs = lastDuration.Milliseconds()
	}
	return resp
}

// ListRecentDecisions 返回最近 limit 条决策遥测（已转 DTO，过滤敏感字段）。
// legacy 模式下 decisionRepo 由 handler 注入；service 仅负责 DTO 转换与 CenterIDs 解析。
// limit 由调用方保证为 1–200；repository.ListRecent 内部会再次截断到 200。
func (s *personIdentityProfileService) ListRecentDecisions(limit int, decisionRepo repository.PeopleIdentityDecisionRepository) ([]model.IdentityDecisionResponse, error) {
	if decisionRepo == nil {
		return []model.IdentityDecisionResponse{}, nil
	}
	rows, err := decisionRepo.ListRecent(limit)
	if err != nil {
		return nil, err
	}
	out := make([]model.IdentityDecisionResponse, 0, len(rows))
	for _, d := range rows {
		out = append(out, toIdentityDecisionResponse(d))
	}
	return out, nil
}

// toIdentityDecisionResponse 将 GORM 模型转为只读 DTO。
// 明确不返回 ComponentFaceIDs、ComponentHash、DecisionKey、embedding、路径、人物名称。
// CenterIDs 从逗号字符串安全解析：过滤非法/0/重复、升序、最多 32 个；解析失败不 panic。
func toIdentityDecisionResponse(d *model.PeopleIdentityDecision) model.IdentityDecisionResponse {
	if d == nil {
		return model.IdentityDecisionResponse{}
	}
	return model.IdentityDecisionResponse{
		ID:                        d.ID,
		CreatedAt:                 d.CreatedAt,
		Mode:                      d.Mode,
		ComponentSize:             d.ComponentSize,
		ComponentFaceIDsTruncated: d.ComponentFaceIDsTruncated,
		LegacyTargetPersonID:      d.LegacyTargetPersonID,
		LegacyScore:               d.LegacyScore,
		ProfileBestPersonID:       d.ProfileBestPersonID,
		ProfileBestScore:          d.ProfileBestScore,
		ProfileSecondPersonID:     d.ProfileSecondPersonID,
		ProfileSecondScore:        d.ProfileSecondScore,
		Margin:                    d.Margin,
		CenterIDs:                 parseCenterIDsCSV(d.CenterIDs, identityProfileDecisionCenterIDsMax),
		Decision:                  d.Decision,
		Reason:                    d.Reason,
		ElapsedMilliseconds:       d.ElapsedMilliseconds,
		AlgorithmVersion:          d.AlgorithmVersion,
		IndexGeneration:           d.IndexGeneration,
	}
}

// parseCenterIDsCSV 安全解析逗号分隔的 center ID 字符串：过滤非法/0/重复，升序，最多 max 个。
// 解析失败跳过非法项，不 panic。空串返回空切片（非 nil）。
func parseCenterIDsCSV(csv string, max int) []uint {
	out := []uint{}
	if csv == "" {
		return out
	}
	seen := make(map[uint]struct{})
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			continue
		}
		if v == 0 {
			continue
		}
		id := uint(v)
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if max > 0 && len(out) >= max {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// currentBackfillState 从持久化读取当前 backfill 状态；读取失败或未初始化时返回零值。
func (s *personIdentityProfileService) currentBackfillState() identityProfileBackfillState {
	if s.configService == nil {
		return identityProfileBackfillState{}
	}
	raw, err := s.configService.GetWithDefault(identityProfileBackfillStateKey, "")
	if err != nil || raw == "" {
		return identityProfileBackfillState{}
	}
	var st identityProfileBackfillState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return identityProfileBackfillState{}
	}
	return st
}

// persistBackfillState 持久化 backfill 状态。
func (s *personIdentityProfileService) persistBackfillState(st identityProfileBackfillState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveBackfillStateLocked(st)
}

// executeWrite 通过全局 WriteQueue 串行化写入；WriteQueue 不可用时直接执行。
func (s *personIdentityProfileService) executeWrite(fn func() error) error {
	if s.writeQueue != nil {
		return s.writeQueue.Execute(fn)
	}
	return fn()
}

// truncateMessage 截断错误信息，避免异常大内容写入数据库。
func truncateMessage(msg string, max int) string {
	if max <= 0 {
		return ""
	}
	msg = strings.TrimSpace(msg)
	if len(msg) <= max {
		return msg
	}
	// 按 rune 截断以防多字节字符被切断。
	r := []rune(msg)
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}

// dedupPersonIDs 去重并忽略 0，保持首次出现顺序。
func dedupPersonIDs(ids []uint) []uint {
	seen := make(map[uint]struct{})
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
