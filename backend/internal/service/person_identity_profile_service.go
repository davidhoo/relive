package service

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// RunBackgroundSlice 执行一次有界后台工作：推进一小批 backfill 游标 + 构建最多一个
	// dirty 人物画像 + 清理其过期 generation。受 cooldown 约束，禁止内部 time.Sleep。
	RunBackgroundSlice() error
	// GetActive 读取人物活动 generation 的完整构建。legacy 返回 nil 且不访问 Repository。
	GetActive(personID uint) (*model.PersonIdentityProfileBuild, error)
	// GetStats 返回画像运行统计。legacy 返回零值结构且不访问 Repository。
	GetStats() (*model.PersonIdentityProfileStats, error)
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

	// nowFn 注入时钟，测试无需真实 sleep。
	nowFn func() time.Time

	mu        sync.Mutex
	lastRunAt time.Time
	// stateLoaded 标记 backfill 状态是否已从持久化加载（legacy 模式永不加载）。
	stateLoaded bool
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
	embeddingModel := identityProfileDefaultEmbeddingModel
	var builderCfg identityProfileBuilderConfig
	if cfg != nil {
		mode = cfg.People.IdentityProfileMode
		batchSize = cfg.People.IdentityProfileBatchSize
		cooldownMs = cfg.People.IdentityProfileCooldownMs
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

	svc := &personIdentityProfileService{
		mode:             mode,
		batchSize:        batchSize,
		cooldownMs:       cooldownMs,
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
		// 加载并校验 backfill 状态；版本不一致时重置。
		svc.loadAndAlignBackfillState()
	} else if mode != identityProfileModeLegacy && bgDB == nil {
		logger.Warnf("Identity profile background DB unavailable; background building disabled (mode=%s)", mode)
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
	if !s.stateLoaded {
		s.loadAndAlignBackfillStateLocked()
	}
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

	// 2. 构建最多一个 dirty 人物画像。
	s.buildOneDirtyPerson()

	return nil
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

// buildOneDirtyPerson 取一个 dirty 人物完成构建、激活与清理。
func (s *personIdentityProfileService) buildOneDirtyPerson() {
	dirty, err := s.bgRepo.ListDirty(0, 1)
	if err != nil {
		logger.Warnf("Identity profile: list dirty failed: %v", err)
		return
	}
	if len(dirty) == 0 {
		return
	}
	personID := dirty[0].PersonID

	// 读取轻量人脸（embedding）。
	faces, err := s.bgFaceRepo.ListProfileFaces(personID)
	if err != nil {
		s.markFailed(personID, fmt.Sprintf("list profile faces: %v", err))
		return
	}

	// 调用纯 Builder。
	build, err := s.builder.Build(personID, faces)
	if err != nil {
		s.markFailed(personID, err.Error())
		return
	}
	build.Profile.EmbeddingModel = s.embeddingModel

	// WriteQueue 串行化写入 + 原子激活新 generation。
	writeErr := s.executeWrite(func() error {
		return s.bgRepo.ReplaceGeneration(personID, build)
	})
	if writeErr != nil {
		if errors.Is(writeErr, repository.ErrPersonNotFound) {
			// 人物在 backfill 查询后、构建前被删除：清理派生画像，不计为系统失败。
			s.cleanupDeletedPerson(personID)
			return
		}
		// 写入失败：ReplaceGeneration 已原子回滚，旧 generation 保留。MarkFailed 记录原因。
		s.markFailed(personID, fmt.Sprintf("replace generation: %v", writeErr))
		return
	}

	// 成功：清理过期 generation，保留 active + 最近一个历史 generation。
	// 清理失败只记录警告，不回滚已激活的新画像。
	if err := s.executeWrite(func() error {
		return s.bgRepo.DeleteInactiveGenerations(personID, 1)
	}); err != nil {
		logger.Warnf("Identity profile: cleanup inactive generations for person %d failed: %v", personID, err)
	}
}

// cleanupDeletedPerson 清理被删除人物的派生画像数据。
func (s *personIdentityProfileService) cleanupDeletedPerson(personID uint) {
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
