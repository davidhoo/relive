package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	CreateRun(mode, applyMode string, photoLimit int) (*model.FaceQualityRescoreRun, error)
	GetRun(id uint) (*model.FaceQualityRescoreRun, error)
	ListRuns(limit int) ([]*model.FaceQualityRescoreRun, error)
	Pause(id uint) error
	Resume(id uint) error
	Cancel(id uint) error
	// RestoreAuto 恢复某 run 产生的自动排除（rescore_run_id 匹配），不影响实时/人工/其他 run。
	RestoreAuto(runID uint, limit int) (*model.FaceQualityRestoreResult, error)
	// Run 启动 worker 循环（非阻塞）。服务重启后从 items 进度继续。
	Run()
}

type faceQualityRescoreService struct {
	people       *peopleService
	repo         repository.FaceQualityRescoreRepository
	coordinator  *BackgroundTaskCoordinator

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

// CreateRun 创建运行并快照当前 historical_backfill + missing 的目标 Face 集合。
func (s *faceQualityRescoreService) CreateRun(mode, applyMode string, photoLimit int) (*model.FaceQualityRescoreRun, error) {
	if s.people == nil {
		return nil, fmt.Errorf("people service not available")
	}
	if !model.IsValidRescoreMode(mode) {
		return nil, fmt.Errorf("invalid mode: %s", mode)
	}

	// 校准一律归一化为 shadow，忽略调用方传入的 apply_mode。
	effectiveApplyMode := applyMode
	if mode == model.FaceQualityRescoreModeCalibration {
		effectiveApplyMode = model.FaceQualityRescoreApplyModeShadow
	} else if !model.IsValidRescoreApplyMode(effectiveApplyMode) {
		return nil, fmt.Errorf("invalid apply_mode: %s", effectiveApplyMode)
	}

	// full/enforce 前置条件：已存在 completed calibration。
	if mode == model.FaceQualityRescoreModeFull && effectiveApplyMode == model.FaceQualityRescoreApplyModeEnforce {
		ok, err := s.repo.HasCompletedCalibration()
		if err != nil {
			return nil, fmt.Errorf("check completed calibration: %w", err)
		}
		if !ok {
			return nil, errCalibrationRequired
		}
	}

	// 单活跃 run 互斥：同时只允许一个 running 或 paused。
	active, err := s.repo.HasActiveRun()
	if err != nil {
		return nil, fmt.Errorf("check active run: %w", err)
	}
	if active {
		return nil, errRunConflict
	}

	// 快照目标 Face 集合：当前 historical_backfill + missing 的事件，按 photo 分组。
	targets, err := s.snapshotTargets(photoLimit)
	if err != nil {
		return nil, fmt.Errorf("snapshot targets: %w", err)
	}

	now := time.Now().UTC()
	ruleVer := qualityRuleVersionFromEvidence(nil) // "v1"
	modelVer := qualityModelVersionFromEvidence(nil)
	if modelVer == "" {
		// 与 ml-service FACE_QUALITY_MODEL_VERSION 对齐；测试环境可能为空，留 schema 默认。
		modelVer = "insightface-buffalo-sc-v1"
	}

	run := &model.FaceQualityRescoreRun{
		Mode:             mode,
		ApplyMode:        effectiveApplyMode,
		Status:           model.FaceQualityRescoreStatusQueued,
		TargetPhotoCount: countDistinctPhotos(targets),
		TargetFaceCount:  len(targets),
		RuleVersion:      ruleVer,
		ModelVersion:     modelVer,
		SelectionPolicy:  "oldest_by_photo_id",
		PhotoLimit:       photoLimit,
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

// snapshotTargets 查询当前 historical_backfill + missing 的 is_current 事件，
// 按 photo_id 升序、photo_limit 截断后返回。
func (s *faceQualityRescoreService) snapshotTargets(photoLimit int) ([]rescoreTarget, error) {
	db := s.people.db
	type row struct {
		PhotoID         uint
		FaceID          *uint
		BBoxX           float64
		BBoxY           float64
		BBoxWidth       float64
		BBoxHeight      float64
		ID              uint
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
	run.Status = model.FaceQualityRescoreStatusPaused
	return s.people.executeWrite(func() error { return s.repo.UpdateRun(run) })
}

func (s *faceQualityRescoreService) Resume(id uint) error {
	run, err := s.repo.GetRun(id)
	if err != nil {
		return err
	}
	if run.Status != model.FaceQualityRescoreStatusPaused {
		return fmt.Errorf("run %d not paused (status=%s)", id, run.Status)
	}
	// 重启时把 processing item 回到 pending（不丢失进度）。
	if _, err := s.repo.ResetProcessingItems(id); err != nil {
		return fmt.Errorf("reset processing items: %w", err)
	}
	run.Status = model.FaceQualityRescoreStatusRunning
	return s.people.executeWrite(func() error { return s.repo.UpdateRun(run) })
}

func (s *faceQualityRescoreService) Cancel(id uint) error {
	run, err := s.repo.GetRun(id)
	if err != nil {
		return err
	}
	if run.Status == model.FaceQualityRescoreStatusCompleted || run.Status == model.FaceQualityRescoreStatusCancelled {
		return nil
	}
	now := time.Now().UTC()
	run.Status = model.FaceQualityRescoreStatusCancelled
	run.CompletedAt = &now
	return s.people.executeWrite(func() error { return s.repo.UpdateRun(run) })
}

// RestoreAuto 恢复某 run 产生的自动排除。复用 faceQualityService 的恢复事务逻辑，
// 但事件来源限定 rescore_run_id=runID，不影响实时/人工/其他 run。
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
	items, err := s.repo.ClaimNextPhotoItems(run.ID)
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

	// 构造目标列表（按 item 顺序）。
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
				"face_validity_score":    result.Evidence.FaceValidityScore,
				"quality_reasons_csv":    reasonCodesCSV(result.Evidence.QualityReasons),
				"quality_rule_version":   qualityRuleVersionFromEvidence(mlEvidenceToModel(result.Evidence)),
				"quality_model_version":  qualityModelVersionFromEvidence(mlEvidenceToModel(result.Evidence)),
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
				PhotoID:        item.PhotoID,
				FaceID:         &item.FaceID,
				BBoxX:          item.BBoxX,
				BBoxY:          item.BBoxY,
				BBoxWidth:      item.BBoxWidth,
				BBoxHeight:     item.BBoxHeight,
				Decision:       decision,
				Reason:         reason,
				Source:         model.FaceQualitySourceAuto,
				RuleVersion:    run.RuleVersion,
				ModelVersion:   run.ModelVersion,
				EvidenceJSON:   evJSON,
				ReasonCodes:    reasonCodesCSV(outcome.ReasonCodes),
				EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore,
				EvidenceState:  model.FaceQualityEvidenceStateAvailable,
				RescoreRunID:   &run.ID,
				IsCurrent:      true,
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
				Reason:          "face_quality_rescore_exclude",
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
	processed, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessed)
	retry, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusRetryableError)
	unmatched, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusUnmatched)
	superseded, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusSupersededManual)
	run.ProcessedFaceCount = int(processed) + int(retry) + int(unmatched) + int(superseded)
	run.RetryableCount = int(retry) + int(unmatched)

	// accepted/review_required/auto_excluded 从本 run 的审计事件统计。
	db := s.people.db
	type cnt struct{ D string; C int64 }
	var rows []cnt
	_ = db.Model(&model.FaceQualityEvent{}).
		Select("decision as d, count(*) as c").
		Where("rescore_run_id = ? AND is_current = ?", run.ID, true).
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
	_ = s.people.executeWrite(func() error { return s.repo.UpdateRun(run) })
}

// maybeCompleteRun 检查 run 是否全部 item 处理完（无 pending/processing），是则标 completed。
// 已被 Cancel/Failed 的 run 不覆盖（避免与人工取消竞态）。
func (s *faceQualityRescoreService) maybeCompleteRun(run *model.FaceQualityRescoreRun) {
	if run.Status == model.FaceQualityRescoreStatusCancelled || run.Status == model.FaceQualityRescoreStatusFailed {
		return
	}
	pending, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusPending)
	processing, _ := s.repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessing)
	if pending == 0 && processing == 0 {
		// 重新读取 run，确认中途未被人工 Cancel/Failed。
		latest, err := s.repo.GetRun(run.ID)
		if err != nil || latest == nil {
			return
		}
		if latest.Status != model.FaceQualityRescoreStatusRunning && latest.Status != model.FaceQualityRescoreStatusQueued {
			return
		}
		now := time.Now().UTC()
		latest.Status = model.FaceQualityRescoreStatusCompleted
		latest.CompletedAt = &now
		*run = *latest
		_ = s.people.executeWrite(func() error { return s.repo.UpdateRun(latest) })
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
	ErrRescoreRunConflict         = fmt.Errorf("an active rescore run already exists")
	ErrRescoreCalibrationRequired = fmt.Errorf("a completed calibration run is required before full/enforce")
	ErrRescoreRunNotFound         = fmt.Errorf("rescore run not found")

	// 内部别名，保持 service 内部引用不变。
	errRunConflict         = ErrRescoreRunConflict
	errCalibrationRequired = ErrRescoreCalibrationRequired
	errRunNotFound         = ErrRescoreRunNotFound
)

// 编译期断言：service 实现 interface。
var _ FaceQualityRescoreService = (*faceQualityRescoreService)(nil)

// 防止 unused 警告（json 在未来事件 evidence 持久化路径用到，当前 marshalEvidence 已覆盖）。
var _ = json.Marshal
var _ = strings.TrimSpace
