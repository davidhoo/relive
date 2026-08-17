package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// FaceQualityRescoreService 历史重评分运行管理。
// 它绝不调用 ApplyDetectionResult / EnqueuePhoto(force=true) / 删除重建 Face / 全库聚类。
// 自动排除复用 faceQualityService 的排除事务逻辑，仅产生关联人物局部刷新。
type FaceQualityRescoreService interface {
	// CreateRun 创建运行。calibration 一律归一化为 shadow；full/enforce 需已完成 calibration。
	// photo_limit>0 时限制快照照片数（校准用）。返回运行 ID 与实际快照计数。
	// full/enforce 必须通过 calibrationRunID 指定服务端验证通过的合格校准 run（0 表示无）。
	// pipelineVersion: legacy_v1（v1 同源 score-known-faces）/ independent_v2（v2 独立验证器）。
	// ruleVersion: independent_v2 管线下的规则版本——face_quality_v3（目标框匹配）/ face_quality_v2（默认）。
	//   v3 可重新复核已有 v2 自动证据；v3 full/enforce 要求引用的校准 run rule_version=face_quality_v3。
	// faceIDs: 非空时为定点校准（仅 calibration+shadow+independent_v2），最多 50 个去重去零、必须存在。
	CreateRun(mode, applyMode string, photoLimit int, calibrationRunID uint, pipelineVersion, ruleVersion string, faceIDs []uint) (*model.FaceQualityRescoreRun, error)
	GetRun(id uint) (*model.FaceQualityRescoreRun, error)
	ListRuns(limit int) ([]*model.FaceQualityRescoreRun, error)
	Pause(id uint) error
	Resume(id uint) error
	Cancel(id uint) error
	// RestoreAuto 恢复某 run 产生的自动排除（rescore_run_id 匹配），不影响实时/人工/其他 run。
	RestoreAuto(runID uint, limit int) (*model.FaceQualityRestoreResult, error)
	// RetryRun 以来源 run 的当前失败事件创建新的 shadow calibration 重试运行。
	RetryRun(sourceRunID uint) (*model.FaceQualityRescoreRun, error)
	// IsEligibleForEnforce 报告某 run 是否为可放行 full/enforce 的合格校准（计划 §3.4 逐项验证）。
	IsEligibleForEnforce(runID uint) bool
	// Run 启动 worker 循环（非阻塞）。服务重启后从 items 进度继续。
	Run()
}

type faceQualityRescoreService struct {
	people      *peopleService
	repo        repository.FaceQualityRescoreRepository
	coordinator *BackgroundTaskCoordinator

	mu      sync.Mutex
	running bool
}

// NewFaceQualityRescoreService 构造历史重评分服务。people 必须非 nil。
func NewFaceQualityRescoreService(people *peopleService, repo repository.FaceQualityRescoreRepository, coordinator *BackgroundTaskCoordinator) FaceQualityRescoreService {
	return &faceQualityRescoreService{
		people:      people,
		repo:        repo,
		coordinator: coordinator,
	}
}

// transitionRunStatus 在 write gate 内调用 repo.TransitionRunStatus，返回是否命中。
// executeWrite 可能因 SQLite 锁重试，ok 取最后一次尝试结果。
func (s *faceQualityRescoreService) transitionRunStatus(runID uint, from []string, to string, completedAt *time.Time) (bool, error) {
	var ok bool
	err := s.people.executeWrite(func() error {
		var tErr error
		ok, tErr = s.repo.TransitionRunStatus(runID, from, to, completedAt)
		return tErr
	})
	return ok, err
}

// ensureV2VerifierReady 在创建/恢复/重试 v2 run 前校验 ML 验证器就绪。
// 仅 pipeline_version=independent_v2 调用 mlclient.Health；任何 503/非预期 identity/
// 解析错误/timeout/nil client 一律映射为 errV2VerifierUnavailable，不向浏览器暴露底层路径。
// 门禁必须在快照、item 状态重置与任意数据库写入之前发生，故置于 CreateRun/Resume/RetryRun 早期。
// legacy_v1 永远放行（v1 同源评分不依赖独立验证器）。
func (s *faceQualityRescoreService) ensureV2VerifierReady(pipelineVersion string) error {
	if pipelineVersion != model.FaceQualityRescorePipelineIndependentV2 {
		return nil
	}
	if s.people == nil || s.people.client == nil {
		return errV2VerifierUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.people.client.Health(ctx)
	if err != nil || res == nil || !res.Ready {
		return errV2VerifierUnavailable
	}
	return nil
}

// CreateRun 创建运行并快照目标 Face 集合。
// pipelineVersion=legacy_v1：从 historical_backfill+missing 事件选目标（v1 同源）。
// pipelineVersion=independent_v2：按 ruleVersion 选无 manual 结论、无同规则版本自动事件的 Face（v2 独立）。
// ruleVersion=face_quality_v3 时可重新复核已有 v2 自动证据；v3 full/enforce 要求校准 run rule_version=face_quality_v3。
// faceIDs 非空时为定点校准（仅 calibration+shadow+independent_v2），最多 50 个、必须存在。
// full/enforce 必须通过 calibrationRunID 指定服务端验证通过的合格校准 run（Task 4 门禁）。
func (s *faceQualityRescoreService) CreateRun(mode, applyMode string, photoLimit int, calibrationRunID uint, pipelineVersion, ruleVersion string, faceIDs []uint) (*model.FaceQualityRescoreRun, error) {
	if s.people == nil {
		return nil, fmt.Errorf("people service not available")
	}
	if !model.IsValidRescoreMode(mode) {
		return nil, fmt.Errorf("invalid mode: %s", mode)
	}
	if !model.IsValidRescorePipelineVersion(pipelineVersion) {
		return nil, fmt.Errorf("invalid pipeline_version: %s", pipelineVersion)
	}

	// face_ids 定点校准校验：仅 calibration（shadow）+ independent_v2 允许；full 拒绝；最多 50 个去重去零。
	// 存在性由 repo.ListIndependentSnapshotTargets 校验（返回 ErrRescoreFaceIDNotFound）。
	if len(faceIDs) > 0 {
		if mode != model.FaceQualityRescoreModeCalibration {
			return nil, ErrRescoreFaceIDsNotCalibration
		}
		if pipelineVersion != model.FaceQualityRescorePipelineIndependentV2 {
			return nil, ErrRescoreFaceIDsNotIndependentV2
		}
		seen := make(map[uint]struct{}, len(faceIDs))
		count := 0
		for _, id := range faceIDs {
			if id == 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			count++
		}
		if count > 50 {
			return nil, ErrRescoreTooManyFaceIDs
		}
	}

	// 校准一律归一化为 shadow，忽略调用方传入的 apply_mode。
	effectiveApplyMode := applyMode
	if mode == model.FaceQualityRescoreModeCalibration {
		effectiveApplyMode = model.FaceQualityRescoreApplyModeShadow
	} else if !model.IsValidRescoreApplyMode(effectiveApplyMode) {
		return nil, fmt.Errorf("invalid apply_mode: %s", effectiveApplyMode)
	}

	// v2 readiness 门禁（任务 §3）：independent_v2 run 在任何 DB 读写前校验 ML 验证器就绪，
	// 不可用直接拒绝，不创建 run/item。legacy_v1 放行。
	if err := s.ensureV2VerifierReady(pipelineVersion); err != nil {
		return nil, err
	}

	// 规则版本：legacy_v1 固定 v1（忽略 ruleVersion 入参，保持向后兼容）；
	// independent_v2 按 ruleVersion——face_quality_v3 启用目标框匹配，face_quality_v4 在其上叠加
	// YuNet 检测尺度归一化；空串推导为 v2（默认）；未知的非空值必须报参数错误，不得静默降级 v2，
	// 否则拼写错误会产出错误的 v2 run 与证据，污染校准来源。
	ruleVer := qualityRuleVersionFromEvidence(nil) // "v1"
	modelVer := qualityModelVersionFromEvidence(nil)
	if pipelineVersion == model.FaceQualityRescorePipelineIndependentV2 {
		switch ruleVersion {
		case "":
			ruleVer = model.FaceQualityRescoreRuleVersionV2
		case model.FaceQualityRescoreRuleVersionV2,
			model.FaceQualityRescoreRuleVersionV3,
			model.FaceQualityRescoreRuleVersionV4:
			ruleVer = ruleVersion
		default:
			return nil, ErrRescoreUnknownRuleVersion
		}
		modelVer = "yunet-v1"
	} else if modelVer == "" {
		// 与 ml-service FACE_QUALITY_MODEL_VERSION 对齐；测试环境可能为空，留 schema 默认。
		modelVer = "insightface-buffalo-sc-v1"
	}

	// full/enforce 门禁（计划 §3.4 + Task 4）：必须指定合格校准 run，且校准 run 的 rule_version
	// 必须等于本 run 的 ruleVer——v2 校准不能放行 v3/v4 enforce，v3 校准不能放行 v4 enforce。
	var calibrationRun *model.FaceQualityRescoreRun
	if mode == model.FaceQualityRescoreModeFull && effectiveApplyMode == model.FaceQualityRescoreApplyModeEnforce {
		if calibrationRunID == 0 {
			return nil, errCalibrationRequired
		}
		cal, ok, err := s.getEligibleCalibration(calibrationRunID, pipelineVersion, ruleVer)
		if err != nil {
			return nil, err
		}
		if !ok {
			// 区分两种失败：规则版本不匹配（v3/v4 full 引用了低版本校准）→ Mismatch；
			// 其他合格性失败（未完成/计数不闭合/空校准）→ errCalibrationRequired。
			// ruleVer==v2 时不做规则版本门禁，失败一律 calibration required。
			if ruleVer != model.FaceQualityRescoreRuleVersionV2 && cal != nil && cal.RuleVersion != ruleVer {
				return nil, ErrRescoreRuleVersionMismatch
			}
			return nil, errCalibrationRequired
		}
		calibrationRun = cal
	}

	// 单活跃 run 互斥：同时只允许一个 running 或 paused。
	active, err := s.repo.HasActiveRun()
	if err != nil {
		return nil, fmt.Errorf("check active run: %w", err)
	}
	if active {
		return nil, errRunConflict
	}

	// 快照目标 Face 集合：v1 走 historical_backfill+missing 事件；independent_v2 按 ruleVersion 选
	// 无 manual 结论、无同规则版本自动事件的 Face（v3 可重新复核 v2 自动证据）。
	targets, err := s.snapshotTargets(photoLimit, pipelineVersion, ruleVer, faceIDs)
	if err != nil {
		return nil, fmt.Errorf("snapshot targets: %w", err)
	}

	now := time.Now().UTC()

	targetScope := model.RescoreTargetScopeV1
	if pipelineVersion == model.FaceQualityRescorePipelineIndependentV2 {
		targetScope = model.RescoreTargetScopeV2
	}

	// 定点校准审计：face_ids 非空时记录 explicit_face_ids，便于复现。
	selectionPolicy := "oldest_by_photo_id"
	if len(faceIDs) > 0 {
		selectionPolicy = "explicit_face_ids"
	}

	run := &model.FaceQualityRescoreRun{
		Mode:             mode,
		ApplyMode:        effectiveApplyMode,
		Status:           model.FaceQualityRescoreStatusQueued,
		TargetPhotoCount: countDistinctPhotos(targets),
		TargetFaceCount:  len(targets),
		RuleVersion:      ruleVer,
		ModelVersion:     modelVer,
		SelectionPolicy:  selectionPolicy,
		PhotoLimit:       photoLimit,
		PipelineVersion:  pipelineVersion,
		TargetScope:      targetScope,
	}
	if calibrationRun != nil {
		run.CalibrationRunID = &calibrationRun.ID
	}

	if err := s.people.executeWrite(func() error {
		if err := s.repo.CreateRun(run); err != nil {
			return err
		}
		items := make([]*model.FaceQualityRescoreItem, 0, len(targets))
		for _, t := range targets {
			items = append(items, &model.FaceQualityRescoreItem{
				RunID:           run.ID,
				PhotoID:         t.PhotoID,
				FaceID:          t.FaceID,
				BBoxX:           t.BBoxX,
				BBoxY:           t.BBoxY,
				BBoxWidth:       t.BBoxWidth,
				BBoxHeight:      t.BBoxHeight,
				BaselineEventID: t.BaselineEventID,
				Status:          model.FaceQualityRescoreItemStatusPending,
			})
		}
		return s.repo.CreateItems(items)
	}); err != nil {
		return nil, err
	}

	run.StartedAt = &now
	run.Status = model.FaceQualityRescoreStatusRunning
	_ = s.people.executeWrite(func() error { return s.repo.UpdateRun(run) })
	return run, nil
}

// rescoreTarget 是创建 run 时快照的单个目标（事件维度）。
type rescoreTarget struct {
	PhotoID         uint
	FaceID          uint
	BBoxX, BBoxY    float64
	BBoxWidth       float64
	BBoxHeight      float64
	BaselineEventID uint
}

// snapshotTargets 快照目标 Face 集合。
// pipelineVersion=legacy_v1：当前 historical_backfill + missing 的 is_current 事件，按 photo 升序。
// pipelineVersion=independent_v2：按 ruleVersion 选无 manual 结论的 Face
// （repo.ListIndependentSnapshotTargets）——非定点运行去重同规则版本自动事件
// （v3 复核 v2 自动证据、v4 复核 v2/v3 自动证据）；定点运行（faceIDs 非空）豁免同规则去重，
// 允许对特定样本追加复核同规则版本。人工事件始终优先（repo 查询排除当前 manual 结论）。
func (s *faceQualityRescoreService) snapshotTargets(photoLimit int, pipelineVersion, ruleVersion string, faceIDs []uint) ([]rescoreTarget, error) {
	if pipelineVersion == model.FaceQualityRescorePipelineIndependentV2 {
		v2Targets, err := s.repo.ListIndependentSnapshotTargets(ruleVersion, photoLimit, faceIDs)
		if err != nil {
			return nil, err
		}
		targets := make([]rescoreTarget, 0, len(v2Targets))
		for _, t := range v2Targets {
			targets = append(targets, rescoreTarget{
				PhotoID:         t.PhotoID,
				FaceID:          t.FaceID,
				BBoxX:           t.BBoxX,
				BBoxY:           t.BBoxY,
				BBoxWidth:       t.BBoxWidth,
				BBoxHeight:      t.BBoxHeight,
				BaselineEventID: t.BaselineEventID,
			})
		}
		return targets, nil
	}

	// legacy_v1：historical_backfill + missing 事件。
	db := s.people.db
	type row struct {
		PhotoID    uint
		FaceID     *uint
		BBoxX      float64 `gorm:"column:bbox_x"`
		BBoxY      float64 `gorm:"column:bbox_y"`
		BBoxWidth  float64 `gorm:"column:bbox_width"`
		BBoxHeight float64 `gorm:"column:bbox_height"`
		ID         uint
	}
	baseWhere := db.Model(&model.FaceQualityEvent{}).
		Where("is_current = ? AND evidence_origin = ? AND evidence_state = ?",
			true,
			model.FaceQualityEvidenceOriginHistoricalBackfill,
			model.FaceQualityEvidenceStateMissing).
		Where("face_id IS NOT NULL")

	var rows []row
	q := baseWhere.Session(&gorm.Session{}).
		Select("photo_id, face_id, bbox_x, bbox_y, bbox_width, bbox_height, id").
		Order("photo_id ASC, id ASC")
	if photoLimit > 0 {
		var photoIDs []uint
		if err := baseWhere.Session(&gorm.Session{}).
			Select("DISTINCT photo_id").
			Order("photo_id ASC").
			Limit(photoLimit).
			Pluck("photo_id", &photoIDs).Error; err != nil {
			return nil, err
		}
		if len(photoIDs) == 0 {
			return nil, nil
		}
		q = q.Where("photo_id IN ?", photoIDs)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	targets := make([]rescoreTarget, 0, len(rows))
	for _, r := range rows {
		if r.FaceID == nil {
			continue
		}
		targets = append(targets, rescoreTarget{
			PhotoID:         r.PhotoID,
			FaceID:          *r.FaceID,
			BBoxX:           r.BBoxX,
			BBoxY:           r.BBoxY,
			BBoxWidth:       r.BBoxWidth,
			BBoxHeight:      r.BBoxHeight,
			BaselineEventID: r.ID,
		})
	}
	return targets, nil
}

func countDistinctPhotos(targets []rescoreTarget) int {
	seen := make(map[uint]struct{}, len(targets))
	for _, t := range targets {
		seen[t.PhotoID] = struct{}{}
	}
	return len(seen)
}

// isValidNormalizedBBox 校验归一化 BBox 合法：值有限，x/y∈[0,1]，width/height∈(0,1]，
// 且 x+width、y+height 不超过 1。零框（width/height=0）非法——这正是 #1 零框 bug 必须阻断的形态。
func isValidNormalizedBBox(x, y, w, h float64) bool {
	if !(mathIsFinite(x) && mathIsFinite(y) && mathIsFinite(w) && mathIsFinite(h)) {
		return false
	}
	if x < 0 || x > 1 || y < 0 || y > 1 {
		return false
	}
	if w <= 0 || w > 1 || h <= 0 || h > 1 {
		return false
	}
	if x+w > 1 || y+h > 1 {
		return false
	}
	return true
}

// mathIsFinite 报告 v 是否为有限值（非 NaN/Inf）。math 包别名便于潜在测试替换。
func mathIsFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func (s *faceQualityRescoreService) GetRun(id uint) (*model.FaceQualityRescoreRun, error) {
	return s.repo.GetRun(id)
}

func (s *faceQualityRescoreService) ListRuns(limit int) ([]*model.FaceQualityRescoreRun, error) {
	return s.repo.ListRuns(repository.FaceQualityRescoreRunQuery{Limit: limit})
}

func (s *faceQualityRescoreService) Pause(id uint) error {
	run, err := s.repo.GetRun(id)
	if err != nil {
		return err
	}
	if run.Status != model.FaceQualityRescoreStatusRunning && run.Status != model.FaceQualityRescoreStatusQueued {
		return fmt.Errorf("run %d not running/queued (status=%s)", id, run.Status)
	}
	// 条件转换：仅 running|queued -> paused。并发方（worker 完成判定）已改状态时返回 false，
	// 不重试、不覆盖。这样暂停是持久的——worker 后续的 refreshRunCounts 只写统计字段，
	// 不会把 paused 整行覆盖回 running。
	ok, err := s.transitionRunStatus(id,
		[]string{model.FaceQualityRescoreStatusRunning, model.FaceQualityRescoreStatusQueued},
		model.FaceQualityRescoreStatusPaused, nil)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("run %d not running/queued (status changed concurrently)", id)
	}
	return nil
}

func (s *faceQualityRescoreService) Resume(id uint) error {
	run, err := s.repo.GetRun(id)
	if err != nil {
		return err
	}
	if run.Status != model.FaceQualityRescoreStatusPaused {
		return fmt.Errorf("run %d not paused (status=%s)", id, run.Status)
	}
	// v2 readiness 门禁：验证器不可用时不允许恢复 v2 run，
	// 且不重置任何 processing item。legacy v1 无此门禁。
	if err := s.ensureV2VerifierReady(run.PipelineVersion); err != nil {
		return err
	}
	// 重启时把 processing item 回到 pending（不丢失进度）。
	if _, err := s.repo.ResetProcessingItems(id); err != nil {
		return fmt.Errorf("reset processing items: %w", err)
	}
	// 条件转换 paused -> running。并发取消/完成已改状态时返回 false，不覆盖。
	ok, err := s.transitionRunStatus(id,
		[]string{model.FaceQualityRescoreStatusPaused},
		model.FaceQualityRescoreStatusRunning, nil)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("run %d not paused (status changed concurrently)", id)
	}
	return nil
}

func (s *faceQualityRescoreService) Cancel(id uint) error {
	run, err := s.repo.GetRun(id)
	if err != nil {
		return err
	}
	if run.Status == model.FaceQualityRescoreStatusCompleted ||
		run.Status == model.FaceQualityRescoreStatusCompletedWithError ||
		run.Status == model.FaceQualityRescoreStatusCancelled ||
		run.Status == model.FaceQualityRescoreStatusFailed {
		return nil
	}
	now := time.Now().UTC()
	// 条件转换：除终态外的活动状态 -> cancelled。并发方已置终态时返回 false（视为已完成，幂等 nil）。
	from := []string{
		model.FaceQualityRescoreStatusQueued,
		model.FaceQualityRescoreStatusRunning,
		model.FaceQualityRescoreStatusPaused,
	}
	ok, err := s.transitionRunStatus(id, from, model.FaceQualityRescoreStatusCancelled, &now)
	if err != nil {
		return err
	}
	if !ok {
		// 已被并发方置为终态——取消是幂等的，不报错。
		return nil
	}
	return nil
}

// RestoreAuto 恢复某 run 产生的自动排除。复用 faceQualityService 的恢复事务逻辑，
// 但事件来源限定 rescore_run_id=runID，不影响实时/人工/其他 run。
//
// 任务 §6：restore-auto 只能恢复 pipeline_version=independent_v2 的 run 产生的自动事件。
// legacy_v1 run 的自动排除不得通过本接口恢复（v1 同源证据不得驱动 v2 自动隔离/恢复）。
func (s *faceQualityRescoreService) RestoreAuto(runID uint, limit int) (*model.FaceQualityRestoreResult, error) {
	if s.people == nil {
		return nil, fmt.Errorf("people service not available")
	}
	run, err := s.repo.GetRun(runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, errRunNotFound
	}
	if run.PipelineVersion != model.FaceQualityRescorePipelineIndependentV2 {
		return nil, errRestoreLegacyV1NotAllowed
	}
	if limit <= 0 {
		limit = 5000
	}
	if limit > 5000 {
		limit = 5000
	}
	records, err := s.repo.ListAutoExcludedByRun(runID, limit)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return &model.FaceQualityRestoreResult{Restored: 0}, nil
	}

	fqs := NewFaceQualityService(s.people).(*faceQualityService)
	restored, err := fqs.restoreAutoRecords(records, "face_quality_rescore_restore_auto")
	if err != nil {
		return nil, err
	}
	return &model.FaceQualityRestoreResult{Restored: restored}, nil
}

// RetryRun 以来源 run 的当前失败事件创建新的 shadow calibration 重试运行（计划 §3.3）。
// 仅允许来源为 calibration、已 completed/completed_with_errors、且有当前失败事件的 run。
// 空失败集合返回 409，不创建空 run。新 run 为 calibration+shadow，retry_of_run_id 指向来源。
func (s *faceQualityRescoreService) RetryRun(sourceRunID uint) (*model.FaceQualityRescoreRun, error) {
	if s.people == nil {
		return nil, fmt.Errorf("people service not available")
	}
	src, err := s.repo.GetRun(sourceRunID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errRunNotFound
		}
		return nil, err
	}
	if src == nil {
		return nil, errRunNotFound
	}
	if src.Mode != model.FaceQualityRescoreModeCalibration {
		return nil, errRetrySourceNotCalibration
	}
	if src.Status != model.FaceQualityRescoreStatusCompleted &&
		src.Status != model.FaceQualityRescoreStatusCompletedWithError {
		return nil, errRetrySourceNotTerminal
	}

	// v2 readiness 门禁：来源为 independent_v2 时，retry run 同样是 v2，必须在任何 DB 读写前
	// 校验验证器就绪。不可用则不创建 retry run。legacy_v1 来源放行。
	if err := s.ensureV2VerifierReady(src.PipelineVersion); err != nil {
		return nil, err
	}

	// 只快照该来源 run 的当前失败事件。
	retryTargets, err := s.repo.ListRetryableTargets(sourceRunID)
	if err != nil {
		return nil, fmt.Errorf("list retryable targets: %w", err)
	}
	if len(retryTargets) == 0 {
		return nil, errRetryNoTargets
	}

	// 单活跃 run 互斥。
	active, err := s.repo.HasActiveRun()
	if err != nil {
		return nil, fmt.Errorf("check active run: %w", err)
	}
	if active {
		return nil, errRunConflict
	}

	now := time.Now().UTC()
	ruleVer := qualityRuleVersionFromEvidence(nil)
	modelVer := qualityModelVersionFromEvidence(nil)
	// retry run 继承来源管线：v2 retry 仍为 v2（face_quality_v2 / yunet-v1），不回退 v1。
	pipelineVersion := src.PipelineVersion
	targetScope := src.TargetScope
	if pipelineVersion == model.FaceQualityRescorePipelineIndependentV2 {
		ruleVer = model.FaceQualityRescoreRuleVersionV2
		modelVer = "yunet-v1"
	} else if modelVer == "" {
		modelVer = "insightface-buffalo-sc-v1"
	}

	// 校验 retry 快照的目标 BBox 合法（复用 snapshotTargets 同一规则）；非法的也保留进 run，
	// 由 worker processOneBatch 在调 ML 前阻断为 retryable_error（保持审计链与计数一致）。
	photoSet := make(map[uint]struct{}, len(retryTargets))
	for _, t := range retryTargets {
		photoSet[t.PhotoID] = struct{}{}
	}

	run := &model.FaceQualityRescoreRun{
		Mode:             model.FaceQualityRescoreModeCalibration,
		ApplyMode:        model.FaceQualityRescoreApplyModeShadow,
		Status:           model.FaceQualityRescoreStatusQueued,
		TargetPhotoCount: len(photoSet),
		TargetFaceCount:  len(retryTargets),
		RuleVersion:      ruleVer,
		ModelVersion:     modelVer,
		SelectionPolicy:  fmt.Sprintf("retry_of_run_%d", sourceRunID),
		RetryOfRunID:     &sourceRunID,
		PipelineVersion:  pipelineVersion,
		TargetScope:      targetScope,
	}

	if err := s.people.executeWrite(func() error {
		if err := s.repo.CreateRun(run); err != nil {
			return err
		}
		items := make([]*model.FaceQualityRescoreItem, 0, len(retryTargets))
		for _, t := range retryTargets {
			items = append(items, &model.FaceQualityRescoreItem{
				RunID:           run.ID,
				PhotoID:         t.PhotoID,
				FaceID:          t.FaceID,
				BBoxX:           t.BBoxX,
				BBoxY:           t.BBoxY,
				BBoxWidth:       t.BBoxWidth,
				BBoxHeight:      t.BBoxHeight,
				BaselineEventID: t.BaselineEventID,
				Status:          model.FaceQualityRescoreItemStatusPending,
			})
		}
		return s.repo.CreateItems(items)
	}); err != nil {
		return nil, err
	}

	run.StartedAt = &now
	run.Status = model.FaceQualityRescoreStatusRunning
	_ = s.people.executeWrite(func() error { return s.repo.UpdateRun(run) })
	return run, nil
}

// getEligibleCalibration 执行计划 §3.4 的合格校准逐项验证（run 已由 repo 取回）。
// 返回 (run, eligible, error)。
//
// v2 门禁（任务 §3）：full/enforce run 必须引用 pipeline_version 与自身一致且合格的校准 run。
// v2 full→v2 calib、v1 full→v1 calib 均允许（v1 路径保留但不再作为 v2 校准来源）；
// 跨管线引用（v2 full→v1 calib）拒绝——v1 同源证据不得驱动 v2 自动隔离。
//
// 规则版本门禁（通用化）：fullRuleVersion 非空时，校准 run 的 rule_version 必须等于 fullRuleVersion。
// 这样 v3 full→必须 v3 calib、v4 full→必须 v4 calib，旧版本校准不能放行新版本 enforce。
// fullRuleVersion 为空（v2 默认）时不做规则版本检查（供 IsEligibleForEnforce 列表视图的
// 通用合格判定，不预设未来 full 用 v2 还是更高版本）。
func (s *faceQualityRescoreService) getEligibleCalibration(runID uint, fullPipeline, fullRuleVersion string) (*model.FaceQualityRescoreRun, bool, error) {
	run, err := s.repo.GetEligibleCalibration(runID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, false, errRunNotFound
		}
		return nil, false, err
	}
	if run.Mode != model.FaceQualityRescoreModeCalibration ||
		run.ApplyMode != model.FaceQualityRescoreApplyModeShadow ||
		run.Status != model.FaceQualityRescoreStatusCompleted {
		return run, false, nil
	}
	// 管线一致性：calibration 必须与 full run 同管线。v2 full 不得引用 v1 calib。
	if run.PipelineVersion != fullPipeline {
		return run, false, nil
	}
	// 规则版本一致性：fullRuleVersion 非空时校准 run 的 rule_version 必须匹配（v3 full→v3 calib）。
	if fullRuleVersion != "" && run.RuleVersion != fullRuleVersion {
		return run, false, nil
	}
	if run.TargetFaceCount <= 0 || run.RetryableCount != 0 {
		return run, false, nil
	}
	if run.ProcessedFaceCount+run.SupersededManualCount != run.TargetFaceCount {
		return run, false, nil
	}
	pendingOrProc, err := s.repo.CountPendingOrProcessing(runID)
	if err != nil {
		return run, false, err
	}
	if pendingOrProc > 0 {
		return run, false, nil
	}
	return run, true, nil
}

// IsEligibleForEnforce 报告某 run 是否为合格校准（供 handler 列表/详情填充 eligible_for_enforce）。
// 仅 independent_v2 校准可作为 v2 enforce 来源（任务 §3）。列表视图不做规则版本门禁——
// 它不知道未来 full 要用 v2 还是 v3，只报通用合格性（completed/无 retryable/计数闭合/同管线）。
// 真正的 v3 规则版本门禁在 CreateRun 的 full 路径（getEligibleCalibration 传 fullRuleVersion）做。
func (s *faceQualityRescoreService) IsEligibleForEnforce(runID uint) bool {
	_, ok, err := s.getEligibleCalibration(runID, model.FaceQualityRescorePipelineIndependentV2, "")
	return err == nil && ok
}

// Run 启动 worker 循环。按照片小批领取 item，调用 ScoreKnownFaces，写证据/审计/局部排除。
// 在 BackgroundTaskCoordinator 的 automatic 优先级下让步于前台/iowait/cooldown。
func (s *faceQualityRescoreService) Run() {
	go s.loop()
}

func (s *faceQualityRescoreService) loop() {
	for {
		run, err := s.currentRunnableRun()
		if err != nil {
			logger.Warnf("face_quality rescore: load runnable run: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		if run == nil || run.Status != model.FaceQualityRescoreStatusRunning {
			time.Sleep(10 * time.Second)
			continue
		}

		// coordinator 准入：automatic 优先级，受前台/cooldown/iowait 约束。
		if s.coordinator != nil {
			req := BackgroundTaskRequest{
				Class:     BackgroundTaskFaceQualityRescore,
				Priority:  BackgroundPriorityAutomatic,
				DedupeKey: fmt.Sprintf("rescore_run_%d", run.ID),
			}
			release, decision, ok := s.coordinator.Begin(req)
			if !ok {
				if decision.Reason == BackgroundDecisionCoalesced || decision.Reason == BackgroundDecisionAlreadyRunning {
					// 已有实例在跑，本轮让出。
					time.Sleep(5 * time.Second)
					continue
				}
				// foreground/cooldown/iowait/resource → 退避重试。
				time.Sleep(5 * time.Second)
				continue
			}
			processed := s.processOneBatch(run)
			release()
			if !processed {
				// 本轮无 item 可领或 run 已结束 → 退避。
				time.Sleep(5 * time.Second)
			}
		} else {
			if !s.processOneBatch(run) {
				time.Sleep(5 * time.Second)
			}
		}
	}
}

// currentRunnableRun 返回最近一个 queued/running/paused 的 run；paused 不处理（等 Resume）。
func (s *faceQualityRescoreService) currentRunnableRun() (*model.FaceQualityRescoreRun, error) {
	runs, err := s.repo.ListRuns(repository.FaceQualityRescoreRunQuery{Limit: 5})
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.Status == model.FaceQualityRescoreStatusRunning || r.Status == model.FaceQualityRescoreStatusQueued {
			return r, nil
		}
	}
	return nil, nil
}

// processOneBatch 领取并处理一个照片的 item 小批。返回是否有 item 被处理。
func (s *faceQualityRescoreService) processOneBatch(run *model.FaceQualityRescoreRun) bool {
	if run.PipelineVersion == model.FaceQualityRescorePipelineIndependentV2 {
		return s.processOneBatchV2(run)
	}
	return s.processOneBatchV1(run)
}

// processOneBatchV1 v1 同源链路：score-known-faces（historical_backfill+missing 目标）。
func (s *faceQualityRescoreService) processOneBatchV1(run *model.FaceQualityRescoreRun) bool {
	items, err := s.repo.ClaimNextPhotoItemsWhenRunning(run.ID)
	if err != nil {
		logger.Warnf("face_quality rescore run %d: claim items: %v", run.ID, err)
		return false
	}
	if len(items) == 0 {
		// 无 pending item：检查是否全部完成，标记 run completed。
		s.maybeCompleteRun(run)
		return false
	}

	// 该照片所有 item 共用同一张图。
	photoID := items[0].PhotoID
	photo, err := s.people.photoRepo.GetByID(photoID)
	if err != nil || photo == nil {
		s.failItems(run, items, fmt.Sprintf("load photo %d: %v", photoID, err), model.FaceQualityRescoreItemStatusRetryableError, model.FaceQualityEvidenceStateRetryableError)
		return true
	}

	imageBase64, imgErr := s.prepareScoreImageBase64(photo)
	if imgErr != nil || imageBase64 == "" {
		state := model.FaceQualityEvidenceStateRetryableError
		s.failItems(run, items, fmt.Sprintf("prepare image: %v", imgErr), model.FaceQualityRescoreItemStatusRetryableError, state)
		return true
	}

	// 构造目标列表（按 item 顺序）。先验证所有 item 的归一化 BBox 合法：
	// 任一非法（零框/越界）则该照片 item 整批写 retryable_error，事件转 historical_rescore + retryable_error，不调 ML。
	for _, it := range items {
		if !isValidNormalizedBBox(it.BBoxX, it.BBoxY, it.BBoxWidth, it.BBoxHeight) {
			s.failItems(run, items,
				fmt.Sprintf("invalid normalized bbox: x=%g y=%g w=%g h=%g", it.BBoxX, it.BBoxY, it.BBoxWidth, it.BBoxHeight),
				model.FaceQualityRescoreItemStatusRetryableError, model.FaceQualityEvidenceStateRetryableError)
			return true
		}
	}

	targets := make([]mlclient.ScoreKnownFaceTarget, 0, len(items))
	for _, it := range items {
		targets = append(targets, mlclient.ScoreKnownFaceTarget{
			FaceID: it.FaceID,
			BBox: mlclient.BoundingBox{
				X: it.BBoxX, Y: it.BBoxY, Width: it.BBoxWidth, Height: it.BBoxHeight,
			},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := s.people.client.ScoreKnownFaces(ctx, mlclient.ScoreKnownFacesRequest{
		ImageBase64: imageBase64,
		Targets:     targets,
	})
	if err != nil {
		s.failItems(run, items, fmt.Sprintf("ml score known faces: %v", err), model.FaceQualityRescoreItemStatusRetryableError, model.FaceQualityEvidenceStateRetryableError)
		return true
	}

	for i, it := range items {
		result := resp.Results[i]
		s.applyResult(run, it, result)
	}

	// 该照片批次完成，持久化 run 计数。
	s.refreshRunCounts(run)
	return true
}

// applyResult 把单个 target 的结果写入 item + Face + 审计事件。
func (s *faceQualityRescoreService) applyResult(run *model.FaceQualityRescoreRun, item *model.FaceQualityRescoreItem, result mlclient.ScoreKnownFaceResult) {
	// unmatched / error：写失败状态，不改 Face 快照，旧事件保持当前；当前事件标 rescore 失败态。
	if result.Status == "unmatched" {
		item.Status = model.FaceQualityRescoreItemStatusUnmatched
		if result.MatchedIoU != nil {
			item.MatchedIoU = result.MatchedIoU
		}
		_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(item) })
		s.markBaselineEventFailed(item, model.FaceQualityEvidenceStateUnmatched)
		return
	}
	if result.Status == "error" || result.Evidence == nil {
		item.Status = model.FaceQualityRescoreItemStatusRetryableError
		_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(item) })
		s.markBaselineEventFailed(item, model.FaceQualityEvidenceStateRetryableError)
		return
	}

	// matched + 有证据：检查人工结论优先（baseline 是否仍是当前结论）。
	superseded, err := s.isSupersededByManual(item)
	if err != nil {
		logger.Warnf("face_quality rescore item %d: check superseded: %v", item.ID, err)
	}
	if superseded {
		item.Status = model.FaceQualityRescoreItemStatusSupersededManual
		_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(item) })
		return
	}

	item.Status = model.FaceQualityRescoreItemStatusProcessed
	if result.MatchedIoU != nil {
		item.MatchedIoU = result.MatchedIoU
	}

	// 用 run.ApplyMode 跑策略引擎。shadow 永不排除（只产 review_required 候选）。
	applyMode := run.ApplyMode
	outcome := evaluateFaceQuality(mlEvidenceToModel(result.Evidence), applyMode)

	s.writeRescoreResult(run, item, result, outcome)
	_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(item) })
}

// isSupersededByManual 检查该 item 的 Face 当前事件是否已不是 baseline（被人工作出结论）。
func (s *faceQualityRescoreService) isSupersededByManual(item *model.FaceQualityRescoreItem) (bool, error) {
	events, err := s.people.faceQualityRepo.ListCurrentByPhotoID(item.PhotoID)
	if err != nil {
		return false, err
	}
	for _, e := range events {
		if e == nil || !e.IsCurrent || e.FaceID == nil || *e.FaceID != item.FaceID {
			continue
		}
		// 当前事件不是 baseline → 已被人工作出结论（或被其他 run 覆盖）。
		if e.ID != item.BaselineEventID {
			return true, nil
		}
		// 当前事件已是 manual → 跳过。
		if e.Source == model.FaceQualitySourceManual {
			return true, nil
		}
	}
	return false, nil
}

// ---- v2 独立复核 worker 路径 ----

// processOneBatchV2 领取并处理一个照片的 item 小批（v2 独立验证器链路）。
// 每张人脸单独裁剪原图上下文 → VerifyKnownFaceCrops → evaluateFaceQualityV2 → 写 v2 证据/审计/排除。
// 严禁调用 ProcessForAI(1024,85) 或 ScoreKnownFaces。
func (s *faceQualityRescoreService) processOneBatchV2(run *model.FaceQualityRescoreRun) bool {
	items, err := s.repo.ClaimNextPhotoItemsWhenRunning(run.ID)
	if err != nil {
		logger.Warnf("face_quality rescore v2 run %d: claim items: %v", run.ID, err)
		return false
	}
	if len(items) == 0 {
		s.maybeCompleteRun(run)
		return false
	}

	// 该照片所有 item 共用同一原图文件。
	photoID := items[0].PhotoID
	photo, err := s.people.photoRepo.GetByID(photoID)
	if err != nil || photo == nil {
		s.failItems(run, items, fmt.Sprintf("load photo %d: %v", photoID, err), model.FaceQualityRescoreItemStatusRetryableError, model.FaceQualityEvidenceStateRetryableError)
		return true
	}

	// 先验证所有 item 的归一化 BBox 合法：任一非法则整批 retryable_error。
	for _, it := range items {
		if !isValidNormalizedBBox(it.BBoxX, it.BBoxY, it.BBoxWidth, it.BBoxHeight) {
			s.failItems(run, items,
				fmt.Sprintf("invalid normalized bbox: x=%g y=%g w=%g h=%g", it.BBoxX, it.BBoxY, it.BBoxWidth, it.BBoxHeight),
				model.FaceQualityRescoreItemStatusRetryableError, model.FaceQualityEvidenceStateRetryableError)
			return true
		}
	}

	// 逐 item 裁剪原图上下文。裁剪失败（读图/EXIF/旋转）→ 该 item technical_error。
	type prepared struct {
		item  *model.FaceQualityRescoreItem
		crops *V2FaceCrops
	}
	preps := make([]prepared, 0, len(items))
	for _, it := range items {
		crops, cerr := PrepareV2FaceCrops(photo.FilePath, photo.ManualRotation, it.BBoxX, it.BBoxY, it.BBoxWidth, it.BBoxHeight)
		if cerr != nil || crops == nil || crops.ContextCropBase64 == "" {
			it.Status = model.FaceQualityRescoreItemStatusRetryableError
			it.LastError = truncateErr(fmt.Sprintf("prepare v2 crops: %v", cerr))
			it.AttemptCount++
			_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(it) })
			s.markBaselineEventFailed(it, model.FaceQualityEvidenceStateRetryableError)
			continue
		}
		preps = append(preps, prepared{item: it, crops: crops})
	}

	if len(preps) == 0 {
		s.refreshRunCounts(run)
		return true
	}

	// 构造 v2 验证目标（按 prep 顺序）。
	targets := make([]mlclient.VerifyKnownFaceCropTarget, 0, len(preps))
	for _, p := range preps {
		targets = append(targets, mlclient.VerifyKnownFaceCropTarget{
			FaceID:               p.item.FaceID,
			ContextCropBase64:    p.crops.ContextCropBase64,
			FaceBoxWidthPx:       p.crops.FaceBoxWidthPx,
			FaceBoxHeightPx:      p.crops.FaceBoxHeightPx,
			FaceBoxOffsetX:       p.crops.FaceBoxOffsetX,
			FaceBoxOffsetY:       p.crops.FaceBoxOffsetY,
			PrimaryDetectorScore: 0, // 历史样本主检测分：v2 从 Face.Confidence 取，下方填充
		})
	}

	// 填充主检测分：从 Face.Confidence 读（历史样本的原始检测置信度）。
	faceIDs := make([]uint, 0, len(preps))
	for _, p := range preps {
		faceIDs = append(faceIDs, p.item.FaceID)
	}
	confMap := s.loadFaceConfidences(faceIDs)
	for i := range targets {
		if c, ok := confMap[targets[i].FaceID]; ok {
			targets[i].PrimaryDetectorScore = c
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := s.people.client.VerifyKnownFaceCrops(ctx, mlclient.VerifyKnownFaceCropsRequest{Targets: targets})
	if err != nil {
		// 整批 ML 调用失败：所有已准备 item 标 retryable_error。
		for _, p := range preps {
			p.item.Status = model.FaceQualityRescoreItemStatusRetryableError
			p.item.LastError = truncateErr(fmt.Sprintf("ml verify known face crops: %v", err))
			p.item.AttemptCount++
			_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(p.item) })
			s.markBaselineEventFailed(p.item, model.FaceQualityEvidenceStateRetryableError)
		}
		s.refreshRunCounts(run)
		return true
	}

	for i, p := range preps {
		result := resp.Results[i]
		s.applyV2Result(run, p.item, p.crops, result)
	}
	s.refreshRunCounts(run)
	return true
}

// loadFaceConfidences 批量读取 Face.Confidence（主检测分）。
func (s *faceQualityRescoreService) loadFaceConfidences(faceIDs []uint) map[uint]float64 {
	out := make(map[uint]float64, len(faceIDs))
	if len(faceIDs) == 0 {
		return out
	}
	var faces []model.Face
	if err := s.people.db.Where("id IN ?", faceIDs).Find(&faces).Error; err != nil {
		logger.Warnf("load face confidences: %v", err)
		return out
	}
	for _, f := range faces {
		out[f.ID] = f.Confidence
	}
	return out
}

// applyV2Result 把单个 v2 验证结果写入 item + 审计事件 + 必要排除。
func (s *faceQualityRescoreService) applyV2Result(run *model.FaceQualityRescoreRun, item *model.FaceQualityRescoreItem, crops *V2FaceCrops, result mlclient.VerifyKnownFaceCropResult) {
	// 验证器 error → technical_error（不伪装判定）。
	if result.VerificationStatus == "error" {
		item.Status = model.FaceQualityRescoreItemStatusRetryableError
		item.LastError = truncateErr("verifier error: " + strings.Join(result.ReasonCodes, ","))
		_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(item) })
		s.markBaselineEventFailed(item, model.FaceQualityEvidenceStateRetryableError)
		return
	}

	// 人工结论优先：baseline 是否仍是当前结论。
	superseded, err := s.isSupersededByManual(item)
	if err != nil {
		logger.Warnf("face_quality rescore v2 item %d: check superseded: %v", item.ID, err)
	}
	if superseded {
		item.Status = model.FaceQualityRescoreItemStatusSupersededManual
		_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(item) })
		return
	}

	item.Status = model.FaceQualityRescoreItemStatusProcessed

	// 构建 v2 evidence（原图尺寸/裁剪尺寸由后端 crops 填充，ML 端 original_* 留 0）。
	ev := buildV2Evidence(crops, result, v2QualityOutcome{}, run.RuleVersion)

	// 阈值从 config 读取（D 轮：未配置时回退默认）。
	outcome := evaluateFaceQualityV2(ev, run.ApplyMode, s.v2Thresholds())

	// 把建议决策回填进 evidence（shadow 模式下保存系统建议，enforce 留空）。
	ev.SuggestedDecision = outcome.SuggestedDecision

	s.writeV2RescoreResult(run, item, ev, outcome)
	_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(item) })
}

// buildV2Evidence 把 crops + ML 验证结果 + 策略 outcome 合并为 FaceQualityEvidenceV2
// （原图尺寸由后端填充；shadow 模式下 SuggestedDecision 保存系统建议决策供校准抽样）。
// runRuleVersion 由调用方传入当前 run.RuleVersion，写入 evidence.rule_version 与事件行一致；
// 不得用 ML 响应或前端请求推导规则版本，亦不得硬编码 v2——否则同一审计事件会行/证据版本不符。
func buildV2Evidence(crops *V2FaceCrops, r mlclient.VerifyKnownFaceCropResult, outcome v2QualityOutcome, runRuleVersion string) *model.FaceQualityEvidenceV2 {
	// 证据 schema 版本以 ML 响应为准（目标框匹配规则后为 independent_v2_target_match_v2）；
	// 旧 ML 镜像回退 independent_v2。这样旧证据保留旧版本，新证据由 ML 决定，后端不伪造。
	schemaVersion := r.EvidenceSchemaVersion
	if schemaVersion == "" {
		schemaVersion = model.EvidenceSchemaVersionV2
	}
	ev := &model.FaceQualityEvidenceV2{
		EvidenceSchemaVersion: schemaVersion,
		PrimaryDetectorScore:  r.PrimaryDetectorScore,
		VerificationStatus:    r.VerificationStatus,
		VerifierScore:         r.VerifierScore,
		MaxContextScore:       r.MaxContextScore,
		TargetMatchIoU:        r.TargetMatchIoU,
		// 诊断字段透传：低于阈值的候选框几何，仅供审计/排障，不作为确认分。
		BestTargetIoU:            r.BestTargetIoU,
		BestTargetCandidateScore: r.BestTargetCandidateScore,
		// 尺度归一化审计透传：送入 YuNet 的缩放比例与输入尺寸。旧 ML 镜像不返回这三个字段，
		// JSON 反序列化为零值，写入证据后不影响旧 schema 的可读性。
		VerifierInputScale:    r.VerifierInputScale,
		VerifierInputWidthPx:  r.VerifierInputWidthPx,
		VerifierInputHeightPx: r.VerifierInputHeightPx,
		VerifierName:             r.VerifierName,
		VerifierVersion:          r.VerifierVersion,
		OriginalWidth:            crops.OriginalWidth,
		OriginalHeight:           crops.OriginalHeight,
		FaceBoxWidthPx:           crops.FaceBoxWidthPx,
		FaceBoxHeightPx:          crops.FaceBoxHeightPx,
		ContextCropWidthPx:       crops.ContextCropWidthPx,
		ContextCropHeightPx:      crops.ContextCropHeightPx,
		ContextExpandRatio:       contextExpandRatio,
		ReasonCodes:              append([]string{}, r.ReasonCodes...),
		SuggestedDecision:        outcome.SuggestedDecision,
		RuleVersion:              runRuleVersion,
		ModelVersion:             r.VerifierVersion,
	}
	if r.BestTargetCandidateBox != nil {
		ev.BestTargetCandidateBox = &model.FaceCandidateBox{
			X:      r.BestTargetCandidateBox.X,
			Y:      r.BestTargetCandidateBox.Y,
			Width:  r.BestTargetCandidateBox.Width,
			Height: r.BestTargetCandidateBox.Height,
		}
	}
	if r.Quality != nil {
		ev.SharpnessNorm = r.Quality.SharpnessNorm
		ev.BrightnessNorm = r.Quality.BrightnessNorm
		ev.ContrastNorm = r.Quality.ContrastNorm
		ev.Occluded = r.Quality.Occluded
		ev.QualityDomain = r.Quality.QualityDomain
		ev.QualityVersion = r.Quality.QualityVersion
	}
	return ev
}

// writeV2RescoreResult 写 v2 证据快照 + 审计事件 + 必要排除事务。
// 与 v1 writeRescoreResult 结构一致，但事件 evidence_pipeline=independent_v2，
// evidence 用 FaceQualityEvidenceV2，并检测旧自动结论冲突。
func (s *faceQualityRescoreService) writeV2RescoreResult(run *model.FaceQualityRescoreRun, item *model.FaceQualityRescoreItem, ev *model.FaceQualityEvidenceV2, outcome v2QualityOutcome) {
	people := s.people
	evJSON := marshalV2Evidence(ev)
	now := time.Now().UTC()

	affectedPersonIDs := make(map[uint]struct{})
	affectedPhotoIDs := make(map[uint]struct{})

	// 检测旧自动结论冲突：v2 accepted 或与旧自动理由冲突 → review_required + auto_decision_conflict。
	// v2 不得自动恢复已被自动隔离的 Face。
	decision := outcome.Decision
	reason := outcome.Reason
	reasonCodes := append([]string{}, outcome.ReasonCodes...)
	if conflict := s.detectV2AutoConflict(item, decision); conflict {
		decision = model.FaceQualityDecisionReviewRequired
		reason = ""
		reasonCodes = append(reasonCodes, "auto_decision_conflict")
	}

	if err := people.executeWrite(func() error {
		return people.db.Transaction(func(tx *gorm.DB) error {
			// 1) 旧 baseline 事件失活。
			if item.BaselineEventID != 0 {
				if err := tx.Model(&model.FaceQualityEvent{}).
					Where("id = ?", item.BaselineEventID).
					Update("is_current", false).Error; err != nil {
					return fmt.Errorf("clear baseline event %d: %w", item.BaselineEventID, err)
				}
			}

			// 2) 写 v2 审计事件。
			evt := &model.FaceQualityEvent{
				PhotoID:          item.PhotoID,
				FaceID:           &item.FaceID,
				BBoxX:            item.BBoxX,
				BBoxY:            item.BBoxY,
				BBoxWidth:        item.BBoxWidth,
				BBoxHeight:       item.BBoxHeight,
				Decision:         decision,
				Reason:           reason,
				Source:           model.FaceQualitySourceAuto,
				RuleVersion:      run.RuleVersion,
				ModelVersion:     run.ModelVersion,
				EvidenceJSON:     evJSON,
				ReasonCodes:      reasonCodesCSV(reasonCodes),
				EvidenceOrigin:   model.FaceQualityEvidenceOriginHistoricalRescore,
				EvidenceState:    model.FaceQualityEvidenceStateAvailable,
				EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
				RescoreRunID:     &run.ID,
				IsCurrent:        true,
			}
			if err := tx.Create(evt).Error; err != nil {
				return fmt.Errorf("create v2 rescore event: %w", err)
			}

			// 3) 排除事务（仅 enforce 高确定性 non_face/low_quality）。
			exclude := (decision == model.FaceQualityDecisionNonFace || decision == model.FaceQualityDecisionLowQuality) &&
				run.ApplyMode == model.FaceQualityRescoreApplyModeEnforce
			if exclude {
				var faceBefore model.Face
				if err := tx.Where("id = ?", item.FaceID).First(&faceBefore).Error; err != nil {
					return fmt.Errorf("load face %d before exclude: %w", item.FaceID, err)
				}
				if faceBefore.PersonID != nil && *faceBefore.PersonID != 0 {
					affectedPersonIDs[*faceBefore.PersonID] = struct{}{}
				}
				if err := upsertExclusionTx(tx, item.PhotoID, item.FaceID, reason, item.BBoxX, item.BBoxY, item.BBoxWidth, item.BBoxHeight, now); err != nil {
					return err
				}
				if err := tx.Model(&model.Face{}).Where("id = ?", item.FaceID).Updates(map[string]interface{}{
					"person_id":        nil,
					"cluster_status":   model.FaceClusterStatusExcluded,
					"cluster_score":    0,
					"exclusion_reason": reason,
					"excluded_at":      &now,
					"updated_at":       now,
				}).Error; err != nil {
					return fmt.Errorf("exclude face %d: %w", item.FaceID, err)
				}
			}
			affectedPhotoIDs[item.PhotoID] = struct{}{}
			return nil
		})
	}); err != nil {
		logger.Warnf("face_quality rescore v2 item %d: write result: %v", item.ID, err)
		item.Status = model.FaceQualityRescoreItemStatusRetryableError
		item.LastError = err.Error()
		return
	}

	s.postExcludeRefresh(affectedPersonIDs, affectedPhotoIDs)
}

// detectV2AutoConflict 检测 v2 判定是否与该 Face 的旧自动结论冲突。
// v2 不得自动恢复已被自动隔离的 Face：若 v2 判 accepted 但 Face 当前是 auto 排除态，
// 或 v2 判 non_face/low_quality 与旧 auto 理由不同，视为冲突 → review_required。
func (s *faceQualityRescoreService) detectV2AutoConflict(item *model.FaceQualityRescoreItem, v2Decision string) bool {
	var face model.Face
	if err := s.people.db.Where("id = ?", item.FaceID).First(&face).Error; err != nil {
		return false
	}
	// Face 当前是自动排除态（excluded + auto reason），v2 想判 accepted → 冲突（不得自动恢复）。
	if face.ClusterStatus == model.FaceClusterStatusExcluded && face.ExclusionReason != "" {
		if v2Decision == model.FaceQualityDecisionAccepted {
			return true
		}
		// v2 判的排除理由与旧理由不同 → 冲突。
		if (v2Decision == model.FaceQualityDecisionNonFace && face.ExclusionReason != model.ExclusionReasonNonFace) ||
			(v2Decision == model.FaceQualityDecisionLowQuality && face.ExclusionReason != model.ExclusionReasonLowQuality) {
			return true
		}
	}
	return false
}

// marshalV2Evidence 把 v2 证据序列化为 JSON。失败返回空串。
func marshalV2Evidence(e *model.FaceQualityEvidenceV2) string {
	if e == nil {
		return ""
	}
	b, err := json.Marshal(e)
	if err != nil {
		logger.Warnf("marshal v2 face quality evidence: %v", err)
		return ""
	}
	return string(b)
}

// writeRescoreResult 写证据快照 + 审计事件 + 必要的排除事务。
// accepted/review_required 保留 person_id/cluster_status；exclude 仅 enforce 时复用排除事务。
func (s *faceQualityRescoreService) writeRescoreResult(run *model.FaceQualityRescoreRun, item *model.FaceQualityRescoreItem, result mlclient.ScoreKnownFaceResult, outcome qualityOutcome) {
	people := s.people
	evJSON := marshalEvidence(mlEvidenceToModel(result.Evidence))
	now := time.Now().UTC()

	affectedPersonIDs := make(map[uint]struct{})
	affectedPhotoIDs := make(map[uint]struct{})

	if err := people.executeWrite(func() error {
		return people.db.Transaction(func(tx *gorm.DB) error {
			// 1) 只更新 Face 质检快照字段，不动 BBox/embedding/thumbnail/person_id/cluster_status。
			// Face.QualityReasonsCSV 的 DB 列名是 quality_reasons_csv（GORM 默认 snake_case + CSV 后缀）。
			updates := map[string]interface{}{
				"face_validity_score":   result.Evidence.FaceValidityScore,
				"quality_reasons_csv":   reasonCodesCSV(result.Evidence.QualityReasons),
				"quality_rule_version":  qualityRuleVersionFromEvidence(mlEvidenceToModel(result.Evidence)),
				"quality_model_version": qualityModelVersionFromEvidence(mlEvidenceToModel(result.Evidence)),
			}
			if result.QualityScore != nil {
				updates["quality_score"] = *result.QualityScore
			}
			if err := tx.Model(&model.Face{}).Where("id = ?", item.FaceID).Updates(updates).Error; err != nil {
				return fmt.Errorf("update face %d quality snapshot: %w", item.FaceID, err)
			}

			// 2) 策略决策。
			// evaluateFaceQuality 在 shadow 模式下已把 exclude 降级为 review_required，
			// 因此这里只需区分 enforce 高置信排除与否则接受/灰区；无需再重复 shadow 降级。
			decision := outcome.Decision
			reason := outcome.Reason
			exclude := outcome.Action == model.FaceQualityActionExclude && run.ApplyMode == model.FaceQualityRescoreApplyModeEnforce

			// 3) 旧 baseline 事件失活。
			if err := tx.Model(&model.FaceQualityEvent{}).
				Where("id = ?", item.BaselineEventID).
				Update("is_current", false).Error; err != nil {
				return fmt.Errorf("clear baseline event %d: %w", item.BaselineEventID, err)
			}

			// 4) 写 rescore 审计事件。
			evt := &model.FaceQualityEvent{
				PhotoID:          item.PhotoID,
				FaceID:           &item.FaceID,
				BBoxX:            item.BBoxX,
				BBoxY:            item.BBoxY,
				BBoxWidth:        item.BBoxWidth,
				BBoxHeight:       item.BBoxHeight,
				Decision:         decision,
				Reason:           reason,
				Source:           model.FaceQualitySourceAuto,
				RuleVersion:      run.RuleVersion,
				ModelVersion:     run.ModelVersion,
				EvidenceJSON:     evJSON,
				ReasonCodes:      reasonCodesCSV(outcome.ReasonCodes),
				EvidenceOrigin:   model.FaceQualityEvidenceOriginHistoricalRescore,
				EvidenceState:    model.FaceQualityEvidenceStateAvailable,
				EvidencePipeline: model.FaceQualityEvidencePipelineLegacyV1,
				RescoreRunID:     &run.ID,
				IsCurrent:        true,
			}
			if err := tx.Create(evt).Error; err != nil {
				return fmt.Errorf("create rescore event: %w", err)
			}

			// 5) 排除事务（仅 enforce 高置信）。
			if exclude {
				var faceBefore model.Face
				if err := tx.Where("id = ?", item.FaceID).First(&faceBefore).Error; err != nil {
					return fmt.Errorf("load face %d before exclude: %w", item.FaceID, err)
				}
				if faceBefore.PersonID != nil && *faceBefore.PersonID != 0 {
					affectedPersonIDs[*faceBefore.PersonID] = struct{}{}
				}
				if err := upsertExclusionTx(tx, item.PhotoID, item.FaceID, reason, item.BBoxX, item.BBoxY, item.BBoxWidth, item.BBoxHeight, now); err != nil {
					return err
				}
				if err := tx.Model(&model.Face{}).Where("id = ?", item.FaceID).Updates(map[string]interface{}{
					"person_id":        nil,
					"cluster_status":   model.FaceClusterStatusExcluded,
					"cluster_score":    0,
					"exclusion_reason": reason,
					"excluded_at":      &now,
					"updated_at":       now,
				}).Error; err != nil {
					return fmt.Errorf("exclude face %d: %w", item.FaceID, err)
				}
			}
			affectedPhotoIDs[item.PhotoID] = struct{}{}
			return nil
		})
	}); err != nil {
		logger.Warnf("face_quality rescore item %d: write result: %v", item.ID, err)
		item.Status = model.FaceQualityRescoreItemStatusRetryableError
		item.LastError = err.Error()
		return
	}

	// 事务后局部刷新（仅排除路径有受影响人物）。
	s.postExcludeRefresh(affectedPersonIDs, affectedPhotoIDs)
}

// postExcludeRefresh 复用 peopleService 的局部刷新链路：人物状态/计数/画像/ANN/proto/merge。
func (s *faceQualityRescoreService) postExcludeRefresh(personIDs map[uint]struct{}, photoIDs map[uint]struct{}) {
	people := s.people
	personIDList := make([]uint, 0, len(personIDs))
	for pid := range personIDs {
		personIDList = append(personIDList, pid)
	}
	for _, pid := range personIDList {
		if err := people.syncPersonState(pid); err != nil {
			logger.Warnf("syncPersonState after rescore exclude for person %d: %v", pid, err)
		}
	}
	photoIDList := make([]uint, 0, len(photoIDs))
	for pid := range photoIDs {
		photoIDList = append(photoIDList, pid)
	}
	if len(photoIDList) > 0 {
		if err := people.executeWrite(func() error {
			return people.photoRepo.RecomputeTopPersonCategory(photoIDList)
		}); err != nil {
			logger.Warnf("recompute top person category after rescore: %v", err)
		}
		if len(personIDList) > 0 {
			people.invalidateIdentityProfiles(IdentityProfileInvalidation{
				DirtyPersonIDs: personIDList,
				Reason:         "face_quality_rescore_exclude",
			})
			people.markProtoCacheDirty(personIDList, nil, "face_quality_rescore_exclude")
		}
		people.markMergeSuggestionsDirty("face_quality_rescore_exclude")
		people.invalidateStatsCache()
	}
}

// markBaselineEventFailed 把 baseline 当前事件标为 rescore 失败态（retryable/unmatched），
// 使其从“历史待补证据”移入“待重试/处理异常”。不改 Face 快照、不使旧事件失效语义错乱。
func (s *faceQualityRescoreService) markBaselineEventFailed(item *model.FaceQualityRescoreItem, state string) {
	_ = s.people.executeWrite(func() error {
		return s.people.db.Transaction(func(tx *gorm.DB) error {
			// 若 baseline 已不是当前（被人工作出结论），不覆盖。
			var cur model.FaceQualityEvent
			if err := tx.Where("id = ? AND is_current = ?", item.BaselineEventID, true).First(&cur).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return nil
				}
				return err
			}
			if cur.Source == model.FaceQualitySourceManual {
				return nil
			}
			return tx.Model(&model.FaceQualityEvent{}).Where("id = ?", item.BaselineEventID).
				Updates(map[string]interface{}{
					"evidence_origin": model.FaceQualityEvidenceOriginHistoricalRescore,
					"evidence_state":  state,
					"rescore_run_id":  item.RunID, // 保留 traceability：失败事件仍归属于本 run
				}).Error
		})
	})
}

// failItems 把一批 item 标失败，并把各自的 baseline 事件标失败态。
func (s *faceQualityRescoreService) failItems(run *model.FaceQualityRescoreRun, items []*model.FaceQualityRescoreItem, msg, itemStatus, eventState string) {
	for _, it := range items {
		it.Status = itemStatus
		it.LastError = truncateErr(msg)
		it.AttemptCount++
		_ = s.people.executeWrite(func() error { return s.repo.UpdateItem(it) })
		s.markBaselineEventFailed(it, eventState)
	}
	s.refreshRunCounts(run)
}

func (s *faceQualityRescoreService) refreshRunCounts(run *model.FaceQualityRescoreRun) {
	// 计数重定义（计划 §3.1）：
	//   processed_face_count 仅 item.status=processed（已获得模型证据），不再含 retryable/unmatched/superseded。
	//   retryable_count = retryable + unmatched。
	//   superseded_manual_count 单列。
	//   review_required_count 只统计本 run 当前 evidence_state=available 且 decision=review_required 的真实灰区。
	processed, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessed)
	retry, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusRetryableError)
	unmatched, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusUnmatched)
	superseded, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusSupersededManual)
	run.ProcessedFaceCount = int(processed)
	run.RetryableCount = int(retry) + int(unmatched)
	run.SupersededManualCount = int(superseded)

	// processed_photo_count：已到终态（非 pending/processing）的照片数。
	processedPhoto, _ := s.repo.CountTerminalPhotos(run.ID)
	run.ProcessedPhotoCount = processedPhoto

	// accepted/review_required/auto_excluded 从本 run 的当前审计事件统计。
	// 真实灰区只计 evidence_state=available 且 decision=review_required，排除 retryable/unmatched。
	db := s.people.db
	type cnt struct {
		D string
		C int64
	}
	var rows []cnt
	_ = db.Model(&model.FaceQualityEvent{}).
		Select("decision as d, count(*) as c").
		Where("rescore_run_id = ? AND is_current = ? AND evidence_state = ?",
			run.ID, true, model.FaceQualityEvidenceStateAvailable).
		Group("decision").Scan(&rows).Error
	acc, rv, exc := 0, 0, 0
	for _, r := range rows {
		switch r.D {
		case model.FaceQualityDecisionAccepted:
			acc = int(r.C)
		case model.FaceQualityDecisionReviewRequired:
			rv = int(r.C)
		case model.FaceQualityDecisionNonFace, model.FaceQualityDecisionLowQuality:
			exc += int(r.C)
		}
	}
	run.AcceptedCount = acc
	run.ReviewRequiredCount = rv
	run.AutoExcludedCount = exc

	// last_error：本 run 最近一条非空 item 错误。
	if le, err := s.repo.LatestItemError(run.ID); err == nil {
		run.LastError = le
	}

	// 只写统计字段，绝不写 status——否则 worker 手持的过期 run 对象会把
	// 并发的 paused/cancelled 整行覆盖回 running。状态转换一律走 TransitionRunStatus。
	progress := repository.FaceQualityRescoreRunProgress{
		ProcessedFaceCount:    run.ProcessedFaceCount,
		ProcessedPhotoCount:   run.ProcessedPhotoCount,
		AcceptedCount:         run.AcceptedCount,
		ReviewRequiredCount:   run.ReviewRequiredCount,
		AutoExcludedCount:     run.AutoExcludedCount,
		RetryableCount:        run.RetryableCount,
		SupersededManualCount: run.SupersededManualCount,
		LastError:             run.LastError,
	}
	_ = s.people.executeWrite(func() error { return s.repo.UpdateRunProgress(run.ID, progress) })
}

// maybeCompleteRun 检查 run 是否全部 item 处理完（无 pending/processing），是则选择终态：
// 无技术错误（retryable_count=0）→ completed；存在 retryable/unmatched → completed_with_errors。
// 已被 Cancel/Failed/paused 的 run 不覆盖（避免与人工取消/暂停竞态）。终态写入用条件转换，
// 禁止 db.Save(latest) 整行覆盖——否则会把并发暂停覆盖回 running。
func (s *faceQualityRescoreService) maybeCompleteRun(run *model.FaceQualityRescoreRun) {
	if run.Status == model.FaceQualityRescoreStatusCancelled || run.Status == model.FaceQualityRescoreStatusFailed {
		return
	}
	pending, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusPending)
	processing, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessing)
	if pending == 0 && processing == 0 {
		// 重新读取 run，确认中途未被人工 Cancel/pause/Failed。
		latest, err := s.repo.GetRun(run.ID)
		if err != nil || latest == nil {
			return
		}
		// 只在仍为 running|queued 时完成；paused/cancelled/已终态的 run 不覆盖。
		if latest.Status != model.FaceQualityRescoreStatusRunning && latest.Status != model.FaceQualityRescoreStatusQueued {
			return
		}
		// 先刷新计数，确保 retryable_count 反映最新失败 item（只写统计字段）。
		s.refreshRunCounts(latest)
		now := time.Now().UTC()
		to := model.FaceQualityRescoreStatusCompleted
		if latest.RetryableCount > 0 {
			to = model.FaceQualityRescoreStatusCompletedWithError
		}
		ok, err := s.transitionRunStatus(run.ID,
			[]string{model.FaceQualityRescoreStatusRunning, model.FaceQualityRescoreStatusQueued},
			to, &now)
		if err != nil || !ok {
			// 并发方已改状态——不覆盖，直接返回。
			return
		}
		latest.Status = to
		latest.CompletedAt = &now
		*run = *latest
	}
}

// prepareScoreImageBase64 复用 detectFacesLocally 的输入逻辑：优先展示缩略图（已旋转校正），
// 缺失时用 ImageProcessor.ProcessForAI 定向缩放。不直接把未校正原图路径与旧框混用。
//
// imageForTest 可注入测试用的 base64（非空时直接返回），生产路径为 nil。
var imageForTest func(photo *model.Photo) (string, error)

func (s *faceQualityRescoreService) prepareScoreImageBase64(photo *model.Photo) (string, error) {
	if imageForTest != nil {
		return imageForTest(photo)
	}
	if photo == nil {
		return "", fmt.Errorf("photo is nil")
	}
	// 优先展示缩略图（与 detectFacesLocally 同一坐标系）。
	if thumbPath := s.people.displayThumbnailPath(photo); thumbPath != "" {
		if data, err := readFile(thumbPath); err == nil && len(data) > 0 {
			return base64.StdEncoding.EncodeToString(data), nil
		}
	}
	// Fallback: ProcessForAI（已做 EXIF 方向校正 + 缩放）。
	processor := util.NewImageProcessor(1024, 85)
	processed, err := processor.ProcessForAI(photo.FilePath)
	if err != nil {
		return "", fmt.Errorf("process for ai: %w", err)
	}
	if len(processed) == 0 {
		return "", fmt.Errorf("empty processed image")
	}
	return base64.StdEncoding.EncodeToString(processed), nil
}

// mlEvidenceToModel 把 mlclient 证据镜像转为 model.FaceQualityEvidence（策略引擎入参）。
func mlEvidenceToModel(e *mlclient.FaceQualityEvidence) *model.FaceQualityEvidence {
	if e == nil {
		return nil
	}
	return &model.FaceQualityEvidence{
		FaceValidityScore:     e.FaceValidityScore,
		PixelWidth:            e.PixelWidth,
		PixelHeight:           e.PixelHeight,
		Sharpness:             e.Sharpness,
		Brightness:            e.Brightness,
		Contrast:              e.Contrast,
		LandmarkCompleteness:  e.LandmarkCompleteness,
		LandmarkGeometryScore: e.LandmarkGeometryScore,
		Yaw:                   e.Yaw,
		Pitch:                 e.Pitch,
		Roll:                  e.Roll,
		PoseEstimable:         e.PoseEstimable,
		Occluded:              e.Occluded,
		QualityReasons:        e.QualityReasons,
		RuleVersion:           e.RuleVersion,
		ModelVersion:          e.ModelVersion,
	}
}

func truncateErr(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// readFile 是 os.ReadFile 的包级别名，便于测试 mock（历史重评分 worker 不直接依赖 os）。
var readFile = func(path string) ([]byte, error) {
	return osReadFile(path)
}

// errRunConflict / errCalibrationRequired / errRunNotFound 供 handler 映射 HTTP 状态码。
// 导出为公共错误，handler 用 errors.Is 判定。
var (
	ErrRescoreRunConflict               = fmt.Errorf("an active rescore run already exists")
	ErrRescoreCalibrationRequired       = fmt.Errorf("a completed calibration run is required before full/enforce")
	ErrRescoreRunNotFound               = fmt.Errorf("rescore run not found")
	ErrRescoreRetrySourceInvalid        = fmt.Errorf("retry source run is not a completed calibration with retryable failures")
	ErrRescoreRetrySourceNotCalibration = fmt.Errorf("retry source run is not calibration mode")
	ErrRescoreRetrySourceNotTerminal    = fmt.Errorf("retry source run is not in a terminal state")
	ErrRescoreRetryNoTargets            = fmt.Errorf("retry source run has no current retryable failures")
	ErrRescoreRestoreLegacyV1NotAllowed = fmt.Errorf("restore-auto is only allowed for independent_v2 runs")
	ErrRescoreV2VerifierUnavailable     = fmt.Errorf("v2 verifier unavailable; deploy a verified YuNet model before creating/resuming/retrying independent_v2 runs")
	ErrRescoreFaceIDsNotCalibration     = fmt.Errorf("face_ids is only allowed for calibration+shadow runs (pre-release targeted validation)")
	ErrRescoreFaceIDsNotIndependentV2   = fmt.Errorf("face_ids is only allowed for independent_v2 pipeline runs")
	ErrRescoreTooManyFaceIDs            = fmt.Errorf("face_ids must contain at most 50 unique non-zero ids")
	ErrRescoreRuleVersionNotV3          = fmt.Errorf("a face_quality_v3 calibration is required before v3 full/enforce")
	// ErrRescoreRuleVersionMismatch full/enforce 引用的校准 run rule_version 与本 run 不匹配。
	// 通用规则版本门禁：v3 full→必须 v3 calib；v4 full→必须 v4 calib。此错误独立于
	// ErrRescoreRuleVersionNotV3（不再 wrap），handler 映射为通用 RESCORE_RULE_VERSION_MISMATCH；
	// v3 旧路径继续使用 ErrRescoreRuleVersionNotV3 + RESCORE_RULE_VERSION_NOT_V3 向后兼容。
	ErrRescoreRuleVersionMismatch = fmt.Errorf("a calibration run with matching rule_version is required before full/enforce")
	// ErrRescoreUnknownRuleVersion independent_v2 管线下传入了未知的非空 rule_version。
	// 必须返回参数错误，不得静默降级为 v2——否则拼写错误会产出错误的 v2 run 与证据。
	ErrRescoreUnknownRuleVersion = fmt.Errorf("unknown rule_version for independent_v2 pipeline; use face_quality_v2/v3/v4 or leave empty")

	// 内部别名，保持 service 内部引用不变。
	errRunConflict               = ErrRescoreRunConflict
	errCalibrationRequired       = ErrRescoreCalibrationRequired
	errRunNotFound               = ErrRescoreRunNotFound
	errRetrySourceNotCalibration = ErrRescoreRetrySourceNotCalibration
	errRetrySourceNotTerminal    = ErrRescoreRetrySourceNotTerminal
	errRetryNoTargets            = ErrRescoreRetryNoTargets
	errRestoreLegacyV1NotAllowed = ErrRescoreRestoreLegacyV1NotAllowed
	errV2VerifierUnavailable     = ErrRescoreV2VerifierUnavailable
)

// 编译期断言：service 实现 interface。
var _ FaceQualityRescoreService = (*faceQualityRescoreService)(nil)

// 防止 unused 警告（json 在未来事件 evidence 持久化路径用到，当前 marshalEvidence 已覆盖）。
var _ = json.Marshal
var _ = strings.TrimSpace
