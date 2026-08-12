package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// 人脸质检策略阈值。与 ml-service v1 规则对齐：自动排除必须“高确定性”，
// 灰区进审核队列，合格样本接受。改动阈值须同步递增 ml-service 的 rule_version
// 并在 restore-auto 流程按旧版本回滚。
const (
	// face_validity_score 低于此值且关键点几何极差 → 高确定性非人脸，自动 exclude。
	qualityNonFaceValidityThreshold = 0.35
	// face_validity_score 灰区下限：低于此但高于 non_face 阈值 → review_required。
	qualityValidityUncertainLow = 0.5
	// face_validity_score 灰区上限：低于此值 → review_required。
	qualityValidityUncertainHigh = 0.6

	// 极低质量真实脸阈值：validity 通过但质量原因码命中以下且严重 → 自动 exclude low_quality。
	qualityLowQualitySharpness = 30.0   // sharpness 低于此为严重模糊
	qualityLowQualityPixelSize = 24     // 像素宽高低于此为严重过小

	// 无 evidence 时的 fail-closed 策略：标记 review_required，绝不伪装 non_face。
)

// qualityOutcome 是策略引擎对单个人脸的判定结果。
type qualityOutcome struct {
	Action      string // accept / exclude / review_required
	Decision    string // accepted / non_face / low_quality / review_required
	Reason      string // non_face / low_quality / ''
	ReasonCodes []string
}

// evaluateFaceQuality 是单个人脸的质检策略引擎。
// evidence 为 nil 时 fail-closed 返回 review_required（技术问题不得伪装成 non_face）。
// 在测试/旧链路下 evidence 为 nil 但 validity 兜底为高分时，仍按 fail-closed 处理，
// 保证不会因为缺少证据就把未知样本交给聚类。
func evaluateFaceQuality(evidence *model.FaceQualityEvidence, mode string) qualityOutcome {
	// disabled 模式：不自动判定，全部接受（仅产出证据快照，不写入排除态）。
	if mode == "disabled" {
		return qualityOutcome{
			Action:   model.FaceQualityActionAccept,
			Decision: model.FaceQualityDecisionAccepted,
		}
	}

	if evidence == nil {
		// 无证据：灰区，进审核，不聚类，不伪装 non_face。
		return qualityOutcome{
			Action:   model.FaceQualityActionReviewRequired,
			Decision: model.FaceQualityDecisionReviewRequired,
		}
	}

	reasons := evidence.QualityReasons
	validity := evidence.FaceValidityScore

	hasReason := func(code string) bool {
		for _, r := range reasons {
			if r == code {
				return true
			}
		}
		return false
	}

	// 高确定性非人脸：validity 极低 + 关键点几何/完整性失效。
	if validity < qualityNonFaceValidityThreshold &&
		(hasReason("invalid_landmarks") || hasReason("bad_geometry") || evidence.LandmarkGeometryScore < 0.3) {
		if mode == "enforce" {
			return qualityOutcome{
				Action:      model.FaceQualityActionExclude,
				Decision:    model.FaceQualityDecisionNonFace,
				Reason:      model.ExclusionReasonNonFace,
				ReasonCodes: reasons,
			}
		}
		// shadow：只产出候选，不自动排除。
		return qualityOutcome{
			Action:      model.FaceQualityActionReviewRequired,
			Decision:    model.FaceQualityDecisionReviewRequired,
			ReasonCodes: reasons,
		}
	}

	// 极低质量真实脸：validity 通过（>= uncertainHigh）但严重模糊或过小。
	if validity >= qualityValidityUncertainHigh {
		severeLowQuality := evidence.Sharpness < qualityLowQualitySharpness ||
			evidence.PixelWidth < qualityLowQualityPixelSize ||
			evidence.PixelHeight < qualityLowQualityPixelSize
		if severeLowQuality {
			if mode == "enforce" {
				return qualityOutcome{
					Action:      model.FaceQualityActionExclude,
					Decision:    model.FaceQualityDecisionLowQuality,
					Reason:      model.ExclusionReasonLowQuality,
					ReasonCodes: reasons,
				}
			}
			return qualityOutcome{
				Action:      model.FaceQualityActionReviewRequired,
				Decision:    model.FaceQualityDecisionReviewRequired,
				ReasonCodes: reasons,
			}
		}
	}

	// 灰区：validity 介于 non_face 阈值与 uncertainHigh 之间，或姿态不可估计。
	if validity < qualityValidityUncertainHigh {
		return qualityOutcome{
			Action:      model.FaceQualityActionReviewRequired,
			Decision:    model.FaceQualityDecisionReviewRequired,
			ReasonCodes: reasons,
		}
	}

	// 合格样本。
	return qualityOutcome{
		Action:      model.FaceQualityActionAccept,
		Decision:    model.FaceQualityDecisionAccepted,
		ReasonCodes: reasons,
	}
}

// qualityModeFromConfig 把配置字符串规范化为 disabled/shadow/enforce。
func qualityModeFromConfig(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "disabled":
		return "disabled"
	case "shadow":
		return "shadow"
	case "enforce":
		return "enforce"
	default:
		return "enforce" // 首期默认 enforce（高确定性新样本）
	}
}

// marshalEvidence 把证据序列化为 JSON 字符串存入审计表。失败时返回空串不阻塞。
func marshalEvidence(e *model.FaceQualityEvidence) string {
	if e == nil {
		return ""
	}
	b, err := json.Marshal(e)
	if err != nil {
		logger.Warnf("marshal face quality evidence: %v", err)
		return ""
	}
	return string(b)
}

// reasonCodesCSV 把原因码列表转为逗号分隔字符串，便于存入 faces.quality_reasons。
func reasonCodesCSV(codes []string) string {
	return strings.Join(codes, ",")
}

// qualityRuleVersionFromEvidence 安全取 rule_version，缺省 v1。
func qualityRuleVersionFromEvidence(e *model.FaceQualityEvidence) string {
	if e == nil || e.RuleVersion == "" {
		return "v1"
	}
	return e.RuleVersion
}

func qualityModelVersionFromEvidence(e *model.FaceQualityEvidence) string {
	if e == nil || e.ModelVersion == "" {
		return ""
	}
	return e.ModelVersion
}

// FaceQualityService 人脸质检审核与恢复服务。
type FaceQualityService interface {
	GetStats() (*model.FaceQualityStatsResponse, error)
	ListReviews(q model.FaceQualityReviewQuery) (*model.FaceQualityReviewPage, error)
	ApplyQualityDecision(req model.FaceQualityDecisionRequest) (*model.FaceQualityDecisionResult, error)
	RestoreAuto(ruleVersion string, limit int) (*model.FaceQualityRestoreResult, error)
}

// faceQualityService 实现质检审核接口。它复用 peopleService 的写事务与受影响人物刷新能力，
// 避免在 service 层另起一套 syncPersonState/画像失效链路。
type faceQualityService struct {
	people *peopleService
}

// NewFaceQualityService 构造质检审核服务。people 必须非 nil。
func NewFaceQualityService(people *peopleService) FaceQualityService {
	return &faceQualityService{people: people}
}

func (f *faceQualityService) GetStats() (*model.FaceQualityStatsResponse, error) {
	if f.people == nil || f.people.faceQualityRepo == nil {
		return nil, fmt.Errorf("face quality repo not available")
	}
	s, err := f.people.faceQualityRepo.Stats()
	if err != nil {
		return nil, err
	}
	return &model.FaceQualityStatsResponse{
		PendingReview:   s.PendingReview,
		AutoExcluded:    s.AutoExcluded,
		ManualConfirmed: s.ManualConfirmed,
		Total:           s.Total,
		ByReason:        s.ByReason,
		ByRuleVersion:   s.ByRuleVersion,
	}, nil
}

func (f *faceQualityService) ListReviews(q model.FaceQualityReviewQuery) (*model.FaceQualityReviewPage, error) {
	if f.people == nil || f.people.faceQualityRepo == nil {
		return nil, fmt.Errorf("face quality repo not available")
	}
	repoQ := repositoryFaceQualityQuery(q)
	records, total, err := f.people.faceQualityRepo.List(repoQ)
	if err != nil {
		return nil, err
	}

	items := make([]model.FaceQualityReviewItem, 0, len(records))
	for _, rec := range records {
		items = append(items, buildReviewItem(rec, f.people))
	}

	page := q.Page
	if page < 1 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if totalPages < 1 {
		totalPages = 1
	}

	return &model.FaceQualityReviewPage{
		Items:      items,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// repositoryFaceQualityQuery 把 DTO 查询转为 repo 查询。
func repositoryFaceQualityQuery(q model.FaceQualityReviewQuery) repository.FaceQualityQuery {
	repoQ := repository.FaceQualityQuery{
		Decision:    q.State,
		Source:      q.Source,
		RuleVersion: q.RuleVersion,
		Reason:      q.Reason,
		Page:        q.Page,
		PageSize:    q.PageSize,
	}
	// state 维度映射到 decision/source：审核页三个 Tab 用 state 区分。
	// pending_review → decision=review_required
	// auto_excluded → source=auto + decision IN (non_face, low_quality)
	// manual_confirmed → source=manual
	switch q.State {
	case "pending_review":
		repoQ.Decision = model.FaceQualityDecisionReviewRequired
		repoQ.Source = ""
	case "auto_excluded":
		repoQ.Source = model.FaceQualitySourceAuto
		repoQ.Decisions = []string{model.FaceQualityDecisionNonFace, model.FaceQualityDecisionLowQuality}
		repoQ.Decision = ""
	case "manual_confirmed":
		repoQ.Source = model.FaceQualitySourceManual
		repoQ.Decision = ""
	}
	if q.StartTime != "" {
		if t, err := time.Parse(time.RFC3339, q.StartTime); err == nil {
			repoQ.StartTime = &t
		}
	}
	if q.EndTime != "" {
		if t, err := time.Parse(time.RFC3339, q.EndTime); err == nil {
			repoQ.EndTime = &t
		}
	}
	return repoQ
}// buildReviewItem 把审计事件组装成审核卡片所需上下文。
func buildReviewItem(rec *model.FaceQualityEvent, people *peopleService) model.FaceQualityReviewItem {
	item := model.FaceQualityReviewItem{
		EventID:         rec.ID,
		PhotoID:         rec.PhotoID,
		FaceID:          rec.FaceID,
		Decision:        rec.Decision,
		Reason:          rec.Reason,
		Source:          rec.Source,
		RuleVersion:     rec.RuleVersion,
		ModelVersion:    rec.ModelVersion,
		ReasonCodes:     splitReasonCodes(rec.ReasonCodes),
		ReviewAction:    rec.ReviewAction,
		ReviewedAt:      rec.ReviewedAt,
		RestoredAt:      rec.RestoredAt,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		BBoxX:           rec.BBoxX,
		BBoxY:           rec.BBoxY,
		BBoxWidth:       rec.BBoxWidth,
		BBoxHeight:      rec.BBoxHeight,
		EvidenceJSON:    rec.EvidenceJSON,
	}

	// 填充人脸裁剪路径与原图路径，便于审核页展示。
	if rec.FaceID != nil && people != nil && people.faceRepo != nil {
		if face, err := people.faceRepo.GetByID(*rec.FaceID); err == nil && face != nil {
			item.ThumbnailPath = face.ThumbnailPath
			item.FaceValidityScore = face.FaceValidityScore
			item.QualityScore = face.QualityScore
		}
	}
	if people != nil && people.photoRepo != nil {
		if photo, err := people.photoRepo.GetByID(rec.PhotoID); err == nil && photo != nil {
			item.PhotoFilePath = photo.FilePath
			item.PhotoThumbnail = photo.ThumbnailPath
		}
	}
	return item
}

func splitReasonCodes(csv string) []string {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ApplyQualityDecision 处理人工审核动作：确认排除 / 改判 / 接受 / 恢复。
// 人工结论优先级：人工接受 > 自动排除；人工排除 > 自动接受。新规则不得无声覆盖人工结论。
func (f *faceQualityService) ApplyQualityDecision(req model.FaceQualityDecisionRequest) (*model.FaceQualityDecisionResult, error) {
	if f.people == nil {
		return nil, fmt.Errorf("people service not available")
	}
	if len(req.EventIDs) == 0 {
		return nil, fmt.Errorf("event_ids must not be empty")
	}
	if !model.IsValidQualityReviewAction(req.Action) {
		return nil, fmt.Errorf("invalid action: %s", req.Action)
	}

	people := f.people
	affectedPersonIDs := make(map[uint]struct{})
	affectedPhotoIDs := make(map[uint]struct{})
	processed := 0

	now := time.Now()

	if err := people.executeWrite(func() error {
		return people.db.Transaction(func(tx *gorm.DB) error {
			for _, eid := range req.EventIDs {
				var rec model.FaceQualityEvent
				if err := tx.Where("id = ?", eid).First(&rec).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						continue
					}
					return fmt.Errorf("load quality event %d: %w", eid, err)
				}

				// 解析目标判定与原因。
				// confirm_exclude 沿用原事件 reason：请求未带 reason 时用 rec.Reason，
				// decision 跟着 reason 走（low_quality → low_quality，否则 non_face），
				// 避免把 low_quality 排除误标为 non_face。
				actionReason := req.Reason
				if req.Action == model.FaceQualityReviewActionConfirmExclude && actionReason == "" {
					actionReason = rec.Reason
				}
				targetDecision, targetReason, exclude := resolveReviewAction(req.Action, actionReason)
				if exclude && !model.IsValidExclusionReason(targetReason) && targetReason != "" {
					return fmt.Errorf("invalid reason for action %s: %s", req.Action, targetReason)
				}

				// 创建新的人工事件（追加式，不删历史）。
				// 恢复动作在创建时即标记 restored_at；其他动作 reviewed_at 已设。
				newEvent := model.FaceQualityEvent{
					PhotoID:       rec.PhotoID,
					FaceID:        rec.FaceID,
					BBoxX:         rec.BBoxX,
					BBoxY:         rec.BBoxY,
					BBoxWidth:     rec.BBoxWidth,
					BBoxHeight:    rec.BBoxHeight,
					Decision:      targetDecision,
					Reason:        targetReason,
					Source:        model.FaceQualitySourceManual,
					RuleVersion:   rec.RuleVersion,
					ModelVersion:  rec.ModelVersion,
					EvidenceJSON:  rec.EvidenceJSON,
					ReasonCodes:   rec.ReasonCodes,
					ReviewAction:  req.Action,
					ReviewedAt:    &now,
					IsCurrent:     true,
				}
				if req.Action == model.FaceQualityReviewActionRestore {
					newEvent.RestoredAt = &now
				}

				// 旧事件失活。
				if err := tx.Model(&model.FaceQualityEvent{}).
					Where("photo_id = ? AND ABS(bbox_x - ?) < 0.001 AND ABS(bbox_y - ?) < 0.001 AND is_current = ?",
						rec.PhotoID, rec.BBoxX, rec.BBoxY, true).
					Update("is_current", false).Error; err != nil {
					return fmt.Errorf("clear current events: %w", err)
				}

				if err := tx.Create(&newEvent).Error; err != nil {
					return fmt.Errorf("create manual event: %w", err)
				}

				// 更新 Face 状态与 face_exclusions。
				if rec.FaceID != nil {
					faceID := *rec.FaceID
					// 先查出当前归属人物，用于事务后局部刷新。
					// 排除时该人物会失去成员；接受/恢复时若 Face 回 pending 也会影响原人物统计。
					var faceBefore model.Face
					if err := tx.Where("id = ?", faceID).First(&faceBefore).Error; err != nil {
						if err == gorm.ErrRecordNotFound {
							continue
						}
						return fmt.Errorf("load face %d before quality decision: %w", faceID, err)
					}
					if faceBefore.PersonID != nil && *faceBefore.PersonID != 0 {
						affectedPersonIDs[*faceBefore.PersonID] = struct{}{}
					}
					if exclude {
						// 排除：写 face_exclusions，Face 置 excluded。
						if err := upsertExclusionTx(tx, rec.PhotoID, faceID, targetReason, rec.BBoxX, rec.BBoxY, rec.BBoxWidth, rec.BBoxHeight, now); err != nil {
							return err
						}
						if err := tx.Model(&model.Face{}).Where("id = ?", faceID).Updates(map[string]interface{}{
							"person_id":          nil,
							"cluster_status":     model.FaceClusterStatusExcluded,
							"cluster_score":      0,
							"manual_locked":      false,
							"manual_lock_reason": "",
							"manual_locked_at":   nil,
							"exclusion_reason":   targetReason,
							"excluded_at":        &now,
							"updated_at":         now,
						}).Error; err != nil {
							return fmt.Errorf("update face %d exclusion: %w", faceID, err)
						}
					} else {
						// 接受/恢复：删除 face_exclusions，Face 回到 pending。
						if err := tx.Where("photo_id = ? AND source_face_id = ?", rec.PhotoID, faceID).
							Delete(&model.FaceExclusion{}).Error; err != nil {
							return fmt.Errorf("delete exclusion for face %d: %w", faceID, err)
						}
						if err := tx.Model(&model.Face{}).Where("id = ?", faceID).Updates(map[string]interface{}{
							"person_id":          nil,
							"cluster_status":     model.FaceClusterStatusPending,
							"cluster_score":      0,
							"manual_locked":      false,
							"manual_lock_reason": "",
							"manual_locked_at":   nil,
							"exclusion_reason":   "",
							"excluded_at":        nil,
							"retry_count":        0,
							"clustered_at":       nil,
							"updated_at":         now,
						}).Error; err != nil {
							return fmt.Errorf("restore face %d: %w", faceID, err)
						}
					}
				}

				affectedPhotoIDs[rec.PhotoID] = struct{}{}
				processed++
			}
			return nil
		})
	}); err != nil {
		return nil, err
	}

	// 事务后局部刷新：受影响人物的画像/合并建议/原型缓存。
	personIDList := make([]uint, 0, len(affectedPersonIDs))
	for pid := range affectedPersonIDs {
		personIDList = append(personIDList, pid)
	}
	for _, pid := range personIDList {
		if err := people.syncPersonState(pid); err != nil {
			logger.Warnf("syncPersonState after quality decision for person %d: %v", pid, err)
		}
	}
	photoIDList := make([]uint, 0, len(affectedPhotoIDs))
	for pid := range affectedPhotoIDs {
		photoIDList = append(photoIDList, pid)
	}
	if len(photoIDList) > 0 {
		if err := people.executeWrite(func() error {
			return people.photoRepo.RecomputeTopPersonCategory(photoIDList)
		}); err != nil {
			logger.Warnf("recompute top person category after quality decision: %v", err)
		}
		if len(personIDList) > 0 {
			people.invalidateIdentityProfiles(IdentityProfileInvalidation{
				DirtyPersonIDs: personIDList,
				Reason:         "face_quality_decision",
			})
			people.markProtoCacheDirty(personIDList, nil, "face_quality_decision")
		}
		people.markMergeSuggestionsDirty("face_quality_decision")
		people.invalidateStatsCache()
	}

	return &model.FaceQualityDecisionResult{
		Processed: processed,
	}, nil
}

// resolveReviewAction 把审核动作映射为 (decision, reason, exclude)。
// reason 参数对 mark_non_face/mark_low_quality 来自请求字面量；对 confirm_exclude
// 由调用方传入原事件 reason（避免把 low_quality 误标为 non_face）。
func resolveReviewAction(action, reason string) (decision, reasonOut string, exclude bool) {
	switch action {
	case model.FaceQualityReviewActionConfirmExclude:
		// 沿用原事件 reason，决策跟着 reason 走。
		if reason == model.ExclusionReasonLowQuality {
			return model.FaceQualityDecisionLowQuality, model.ExclusionReasonLowQuality, true
		}
		return model.FaceQualityDecisionNonFace, model.ExclusionReasonNonFace, true
	case model.FaceQualityReviewActionMarkNonFace:
		return model.FaceQualityDecisionNonFace, model.ExclusionReasonNonFace, true
	case model.FaceQualityReviewActionMarkLowQuality:
		return model.FaceQualityDecisionLowQuality, model.ExclusionReasonLowQuality, true
	case model.FaceQualityReviewActionAccept:
		return model.FaceQualityDecisionAccepted, "", false
	case model.FaceQualityReviewActionRestore:
		return model.FaceQualityDecisionAccepted, "", false
	}
	return model.FaceQualityDecisionReviewRequired, "", false
}

// upsertExclusionTx 在事务内按 photo+face 写 face_exclusions 记录。
func upsertExclusionTx(tx *gorm.DB, photoID, faceID uint, reason string, bx, by, bw, bh float64, now time.Time) error {
	var existing model.FaceExclusion
	err := tx.Where("photo_id = ? AND source_face_id = ?", photoID, faceID).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		rec := model.FaceExclusion{
			PhotoID:      photoID,
			SourceFaceID: faceID,
			Reason:       reason,
			BBoxX:        bx,
			BBoxY:        by,
			BBoxWidth:    bw,
			BBoxHeight:   bh,
		}
		return tx.Create(&rec).Error
	}
	if err != nil {
		return err
	}
	existing.Reason = reason
	existing.BBoxX = bx
	existing.BBoxY = by
	existing.BBoxWidth = bw
	existing.BBoxHeight = bh
	return tx.Save(&existing).Error
}

// RestoreAuto 按规则版本恢复自动排除的样本。仅用于回滚或阈值修正，恢复后回 pending 重新聚类。
// 整个恢复（Face 状态 + face_exclusions 删除 + 人工恢复审计事件 + 旧事件失活）在单事务内完成，
// 避免“Face 已恢复但审计事件缺失”的可追溯性断裂。受影响人物的局部刷新在事务提交后执行。
func (f *faceQualityService) RestoreAuto(ruleVersion string, limit int) (*model.FaceQualityRestoreResult, error) {
	if f.people == nil {
		return nil, fmt.Errorf("people service not available")
	}
	if ruleVersion == "" {
		return nil, fmt.Errorf("rule_version is required")
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}

	people := f.people
	records, err := people.faceQualityRepo.ListAutoByRuleVersion(ruleVersion, limit)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return &model.FaceQualityRestoreResult{Restored: 0, RuleVersion: ruleVersion}, nil
	}

	now := time.Now()
	restored := 0
	affectedPersonIDs := make(map[uint]struct{})
	affectedPhotoIDs := make(map[uint]struct{})

	if err := people.executeWrite(func() error {
		return people.db.Transaction(func(tx *gorm.DB) error {
			for _, rec := range records {
				if rec.FaceID == nil {
					continue
				}
				faceID := *rec.FaceID

				// 查出当前归属人物（恢复会改变其成员组成）。
				var faceBefore model.Face
				if err := tx.Where("id = ?", faceID).First(&faceBefore).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						continue
					}
					return fmt.Errorf("load face %d before restore: %w", faceID, err)
				}
				if faceBefore.PersonID != nil && *faceBefore.PersonID != 0 {
					affectedPersonIDs[*faceBefore.PersonID] = struct{}{}
				}

				// 删除 face_exclusions 记录。
				if err := tx.Where("photo_id = ? AND source_face_id = ?", rec.PhotoID, faceID).
					Delete(&model.FaceExclusion{}).Error; err != nil {
					return fmt.Errorf("delete exclusion for face %d: %w", faceID, err)
				}

				// Face 回 pending。
				if err := tx.Model(&model.Face{}).Where("id = ?", faceID).Updates(map[string]interface{}{
					"person_id":          nil,
					"cluster_status":     model.FaceClusterStatusPending,
					"cluster_score":      0,
					"manual_locked":      false,
					"manual_lock_reason": "",
					"manual_locked_at":   nil,
					"exclusion_reason":   "",
					"excluded_at":        nil,
					"retry_count":        0,
					"clustered_at":       nil,
					"updated_at":         now,
				}).Error; err != nil {
					return fmt.Errorf("restore face %d: %w", faceID, err)
				}

				// 旧事件失活。
				if err := tx.Model(&model.FaceQualityEvent{}).
					Where("photo_id = ? AND ABS(bbox_x - ?) < 0.001 AND ABS(bbox_y - ?) < 0.001 AND is_current = ?",
						rec.PhotoID, rec.BBoxX, rec.BBoxY, true).
					Update("is_current", false).Error; err != nil {
					return fmt.Errorf("clear current events for face %d: %w", faceID, err)
				}

				// 写人工恢复审计事件。
				newEvent := model.FaceQualityEvent{
					PhotoID:      rec.PhotoID,
					FaceID:       rec.FaceID,
					BBoxX:        rec.BBoxX,
					BBoxY:        rec.BBoxY,
					BBoxWidth:    rec.BBoxWidth,
					BBoxHeight:   rec.BBoxHeight,
					Decision:     model.FaceQualityDecisionAccepted,
					Source:       model.FaceQualitySourceManual,
					RuleVersion:  rec.RuleVersion,
					ModelVersion: rec.ModelVersion,
					EvidenceJSON: rec.EvidenceJSON,
					ReasonCodes:  rec.ReasonCodes,
					ReviewAction: model.FaceQualityReviewActionRestore,
					RestoredAt:   &now,
					IsCurrent:    true,
				}
				if err := tx.Create(&newEvent).Error; err != nil {
					return fmt.Errorf("create restore event for face %d: %w", faceID, err)
				}

				affectedPhotoIDs[rec.PhotoID] = struct{}{}
				restored++
			}
			return nil
		})
	}); err != nil {
		return nil, err
	}

	// 事务后局部刷新。
	personIDList := make([]uint, 0, len(affectedPersonIDs))
	for pid := range affectedPersonIDs {
		personIDList = append(personIDList, pid)
	}
	for _, pid := range personIDList {
		if err := people.syncPersonState(pid); err != nil {
			logger.Warnf("syncPersonState after restore-auto for person %d: %v", pid, err)
		}
	}
	photoIDList := make([]uint, 0, len(affectedPhotoIDs))
	for pid := range affectedPhotoIDs {
		photoIDList = append(photoIDList, pid)
	}
	if len(photoIDList) > 0 {
		if err := people.executeWrite(func() error {
			return people.photoRepo.RecomputeTopPersonCategory(photoIDList)
		}); err != nil {
			logger.Warnf("recompute top person category after restore-auto: %v", err)
		}
		if len(personIDList) > 0 {
			people.invalidateIdentityProfiles(IdentityProfileInvalidation{
				DirtyPersonIDs: personIDList,
				Reason:          "face_quality_restore_auto",
			})
			people.markProtoCacheDirty(personIDList, nil, "face_quality_restore_auto")
		}
		people.markMergeSuggestionsDirty("face_quality_restore_auto")
		people.invalidateStatsCache()
	}

	return &model.FaceQualityRestoreResult{
		Restored:    restored,
		RuleVersion: ruleVersion,
	}, nil
}
