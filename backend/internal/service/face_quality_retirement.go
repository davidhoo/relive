package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// 人脸质检退役迁移标记（apply 成功后写入 app_config，幂等）。
const FaceQualityRetirementMigrationKey = "migration.face_quality_retirement_v1"

// 退役分类（交付统计用）。清理完成计数不得含 unknown。
const (
	RetirementCategoryKeepManual           = "keep_manual"
	RetirementCategoryPromoteManual        = "promote_manual_source"
	RetirementCategoryTransferManualOrphan = "transfer_manual_orphan"
	RetirementCategoryClearAutoExclude     = "clear_auto_exclusion"
	RetirementCategoryClearAutoReview      = "clear_auto_review"
	RetirementCategoryUnknown              = "unknown"
	RetirementCategoryArchiveRun           = "archive_rescore_run"
)

// RetirementItem 逐条预演/变更清单项。
type RetirementItem struct {
	Category        string `json:"category"`
	FaceID          uint   `json:"face_id,omitempty"`
	PhotoID         uint   `json:"photo_id,omitempty"`
	ExclusionID     uint   `json:"exclusion_id,omitempty"`
	RescoreRunID    uint   `json:"rescore_run_id,omitempty"`
	CurrentStatus   string `json:"current_status,omitempty"`
	ExclusionSource string `json:"exclusion_source,omitempty"`
	ExclusionReason string `json:"exclusion_reason,omitempty"`
	Basis           string `json:"basis"`
	Fingerprint     string `json:"fingerprint"`
	OldPersonID     *uint  `json:"old_person_id,omitempty"`
	LostAssignment  bool   `json:"lost_assignment,omitempty"`
}

// RetirementPlan 只读预演结果。
type RetirementPlan struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Items       []RetirementItem `json:"items"`
	Summary     map[string]int   `json:"summary"`
}

// RetirementApplyResult apply 结果。
type RetirementApplyResult struct {
	AppliedAt    time.Time        `json:"applied_at"`
	Changed      []RetirementItem `json:"changed"`
	Summary      map[string]int   `json:"summary"`
	Idempotent   bool             `json:"idempotent"`
	MigrationKey string           `json:"migration_key"`
}

var errRetirementFingerprintMismatch = errors.New("retirement fingerprint mismatch: re-plan required")

// PlanFaceQualityRetirement 只读分类，不写库。
// 保守规则：无足够证据不得 clear；仅有 auto 事件 + exclusion.source=unknown 归 unknown。
func PlanFaceQualityRetirement(db *gorm.DB) (*RetirementPlan, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}

	var faces []model.Face
	if err := db.Where("cluster_status IN ?", []string{
		model.FaceClusterStatusExcluded,
		model.FaceClusterStatusReviewRequired,
	}).Find(&faces).Error; err != nil {
		return nil, fmt.Errorf("list affected faces: %w", err)
	}

	var exclusions []model.FaceExclusion
	if err := db.Find(&exclusions).Error; err != nil {
		return nil, fmt.Errorf("list exclusions: %w", err)
	}
	exclByFace := make(map[uint]model.FaceExclusion, len(exclusions))
	exclusionsByPhoto := make(map[uint][]model.FaceExclusion)
	for _, e := range exclusions {
		exclByFace[e.SourceFaceID] = e
		exclusionsByPhoto[e.PhotoID] = append(exclusionsByPhoto[e.PhotoID], e)
	}

	var currentEvents []model.FaceQualityEvent
	if err := db.Where("is_current = ?", true).Find(&currentEvents).Error; err != nil {
		return nil, fmt.Errorf("list current quality events: %w", err)
	}
	eventsByFace := make(map[uint][]model.FaceQualityEvent)
	eventsByPhoto := make(map[uint][]model.FaceQualityEvent)
	for _, ev := range currentEvents {
		if ev.FaceID != nil {
			eventsByFace[*ev.FaceID] = append(eventsByFace[*ev.FaceID], ev)
		}
		eventsByPhoto[ev.PhotoID] = append(eventsByPhoto[ev.PhotoID], ev)
	}

	seenFace := make(map[uint]struct{}, len(faces))
	items := make([]RetirementItem, 0, len(faces)+len(exclusions))

	for _, face := range faces {
		seenFace[face.ID] = struct{}{}
		excl, hasExcl := exclByFace[face.ID]
		ev := pickCurrentEvent(face, eventsByFace[face.ID], eventsByPhoto[face.PhotoID])
		item := classifyFaceRetirement(face, hasExcl, excl, ev)
		// A replacement face may still be governed by an old bbox exclusion.
		// Conflicting or indirect provenance must not authorize automatic cleanup.
		for _, other := range exclusionsByPhoto[face.PhotoID] {
			if other.PhotoID == face.PhotoID && other.SourceFaceID != face.ID &&
				bboxIoU(face.BBoxX, face.BBoxY, face.BBoxWidth, face.BBoxHeight, other.BBoxX, other.BBoxY, other.BBoxWidth, other.BBoxHeight) > exclusionIoUThreshold {
				item.Category = RetirementCategoryUnknown
				item.Basis = "overlapping exclusion belongs to another face ID; preserve and reconcile provenance"
			}
		}
		items = append(items, item)
	}

	// exclusion 行存在但 face 已不在 excluded/review_required：仍需分类（可能是残留）。
	for _, excl := range exclusions {
		if _, ok := seenFace[excl.SourceFaceID]; ok {
			continue
		}
		var face model.Face
		err := db.First(&face, excl.SourceFaceID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			items = append(items, RetirementItem{
				Category:        RetirementCategoryUnknown,
				ExclusionID:     excl.ID,
				PhotoID:         excl.PhotoID,
				ExclusionSource: normalizeExclusionSource(excl.Source),
				ExclusionReason: excl.Reason,
				Basis:           "exclusion row without matching face; keep and list",
				Fingerprint:     fingerprintExclusionOnly(excl),
			})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load face %d for exclusion %d: %w", excl.SourceFaceID, excl.ID, err)
		}
		ev := pickCurrentEvent(face, eventsByFace[face.ID], eventsByPhoto[face.PhotoID])
		items = append(items, classifyFaceRetirement(face, true, excl, ev))
	}

	var runs []model.FaceQualityRescoreRun
	if err := db.Where("status IN ?", []string{
		model.FaceQualityRescoreStatusQueued,
		model.FaceQualityRescoreStatusRunning,
		model.FaceQualityRescoreStatusPaused,
	}).Find(&runs).Error; err != nil {
		if !isMissingTable(err) {
			return nil, fmt.Errorf("list active rescore runs: %w", err)
		}
	} else {
		for _, run := range runs {
			items = append(items, RetirementItem{
				Category:      RetirementCategoryArchiveRun,
				RescoreRunID:  run.ID,
				CurrentStatus: run.Status,
				Basis:         "active rescore run must be archived to cancelled; worker no longer runs",
				Fingerprint:   fingerprintRescoreRun(run),
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		if items[i].FaceID != items[j].FaceID {
			return items[i].FaceID < items[j].FaceID
		}
		return items[i].RescoreRunID < items[j].RescoreRunID
	})

	return &RetirementPlan{
		GeneratedAt: time.Now().UTC(),
		Items:       items,
		Summary:     summarizeRetirement(items),
	}, nil
}

func classifyFaceRetirement(face model.Face, hasExcl bool, excl model.FaceExclusion, ev *model.FaceQualityEvent) RetirementItem {
	src := ""
	if hasExcl {
		src = normalizeExclusionSource(excl.Source)
	}
	base := RetirementItem{
		FaceID:          face.ID,
		PhotoID:         face.PhotoID,
		CurrentStatus:   face.ClusterStatus,
		ExclusionReason: face.ExclusionReason,
		OldPersonID:     face.PersonID,
		LostAssignment:  face.PersonID == nil && face.ClusterStatus == model.FaceClusterStatusExcluded,
	}
	if hasExcl {
		base.ExclusionID = excl.ID
		base.ExclusionSource = src
		if base.ExclusionReason == "" {
			base.ExclusionReason = excl.Reason
		}
	}
	base.Fingerprint = fingerprintFaceState(face, hasExcl, excl, ev)

	manualExcludeEvent := ev != nil && isManualExcludeEvent(ev)
	manualRestoreOrAccept := ev != nil && isManualRestoreOrAcceptEvent(ev)
	autoExcludeEvent := ev != nil && isAutoExcludeEvent(ev)
	autoReviewEvent := ev != nil && isAutoReviewEvent(ev)

	// 人工恢复/接受是最终意图：不重新施加排除；若仍 excluded 则来源冲突 → unknown。
	if manualRestoreOrAccept && face.ClusterStatus == model.FaceClusterStatusExcluded {
		base.Category = RetirementCategoryUnknown
		base.Basis = "current event is manual restore/accept but face still excluded; keep and list"
		return base
	}

	switch {
	case hasExcl && src == model.ExclusionSourceManual:
		base.Category = RetirementCategoryKeepManual
		base.Basis = "face_exclusions.source=manual"
		return base

	case hasExcl && src == model.ExclusionSourceAuto && autoExcludeEvent && !manualExcludeEvent:
		base.Category = RetirementCategoryClearAutoExclude
		base.Basis = "exclusion.source=auto and current event is auto exclude; no manual exclude evidence"
		return base

	case hasExcl && src == model.ExclusionSourceUnknown && manualExcludeEvent:
		base.Category = RetirementCategoryPromoteManual
		base.Basis = "exclusion.source=unknown but current event is manual exclude; promote source=manual and keep"
		return base

	case hasExcl && src == model.ExclusionSourceAuto && !autoExcludeEvent:
		base.Category = RetirementCategoryUnknown
		base.Basis = "exclusion.source=auto but current event is not a matching auto exclude; keep and list"
		return base

	case hasExcl && src == model.ExclusionSourceUnknown:
		base.Category = RetirementCategoryUnknown
		base.Basis = "exclusion.source=unknown without sufficient manual proof; auto event alone is not enough"
		return base

	case !hasExcl && face.ClusterStatus == model.FaceClusterStatusReviewRequired && autoReviewEvent:
		base.Category = RetirementCategoryClearAutoReview
		base.Basis = "review_required with current auto review_required event; no exclusion row"
		return base

	case !hasExcl && face.ClusterStatus == model.FaceClusterStatusExcluded && manualExcludeEvent:
		// 质检页人工排除孤儿：有 manual 事件、脸仍 excluded、缺 face_exclusions 行 → 转存。
		base.Category = RetirementCategoryTransferManualOrphan
		if base.ExclusionReason == "" && ev != nil {
			base.ExclusionReason = ev.Reason
		}
		base.Basis = "excluded face has current manual exclude event but no exclusion row; create source=manual exclusion"
		return base

	case !hasExcl && face.ClusterStatus == model.FaceClusterStatusExcluded && autoExcludeEvent:
		base.Category = RetirementCategoryClearAutoExclude
		base.Basis = "excluded face has current auto exclude event but no exclusion row; clear to pending"
		return base

	case !hasExcl && face.ClusterStatus == model.FaceClusterStatusExcluded:
		base.Category = RetirementCategoryUnknown
		base.Basis = "excluded status without exclusion row and without decisive current event; keep and list"
		return base

	default:
		base.Category = RetirementCategoryUnknown
		base.Basis = "insufficient evidence; keep and list"
		return base
	}
}

func isManualExcludeEvent(ev *model.FaceQualityEvent) bool {
	if ev == nil || ev.Source != model.FaceQualitySourceManual || ev.RestoredAt != nil {
		return false
	}
	switch ev.ReviewAction {
	case model.FaceQualityReviewActionConfirmExclude,
		model.FaceQualityReviewActionMarkNonFace,
		model.FaceQualityReviewActionMarkLowQuality:
		return true
	}
	if ev.ReviewAction == "" &&
		(ev.Decision == model.FaceQualityDecisionNonFace || ev.Decision == model.FaceQualityDecisionLowQuality) {
		return true
	}
	return false
}

func isManualRestoreOrAcceptEvent(ev *model.FaceQualityEvent) bool {
	if ev == nil || ev.Source != model.FaceQualitySourceManual {
		return false
	}
	return ev.ReviewAction == model.FaceQualityReviewActionRestore ||
		ev.ReviewAction == model.FaceQualityReviewActionAccept ||
		ev.Decision == model.FaceQualityDecisionAccepted
}

func isAutoExcludeEvent(ev *model.FaceQualityEvent) bool {
	if ev == nil || ev.Source != model.FaceQualitySourceAuto || ev.RestoredAt != nil {
		return false
	}
	return ev.Decision == model.FaceQualityDecisionNonFace ||
		ev.Decision == model.FaceQualityDecisionLowQuality
}

func isAutoReviewEvent(ev *model.FaceQualityEvent) bool {
	if ev == nil || ev.Source != model.FaceQualitySourceAuto || ev.RestoredAt != nil {
		return false
	}
	return ev.Decision == model.FaceQualityDecisionReviewRequired
}

func pickCurrentEvent(face model.Face, byFace, byPhoto []model.FaceQualityEvent) *model.FaceQualityEvent {
	if len(byFace) > 0 {
		best := byFace[0]
		for _, ev := range byFace[1:] {
			if ev.ID > best.ID {
				best = ev
			}
		}
		return &best
	}
	var best *model.FaceQualityEvent
	for i := range byPhoto {
		ev := byPhoto[i]
		if bboxIoU(face.BBoxX, face.BBoxY, face.BBoxWidth, face.BBoxHeight,
			ev.BBoxX, ev.BBoxY, ev.BBoxWidth, ev.BBoxHeight) < 0.3 {
			continue
		}
		if best == nil || ev.ID > best.ID {
			cp := ev
			best = &cp
		}
	}
	return best
}

func normalizeExclusionSource(source string) string {
	s := strings.TrimSpace(source)
	if s == "" || !model.IsValidExclusionSource(s) {
		return model.ExclusionSourceUnknown
	}
	return s
}

func fingerprintFaceState(face model.Face, hasExcl bool, excl model.FaceExclusion, ev *model.FaceQualityEvent) string {
	raw, _ := json.Marshal(struct {
		Face         model.Face
		HasExclusion bool
		Exclusion    model.FaceExclusion
		Event        *model.FaceQualityEvent
	}{face, hasExcl, excl, ev})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func fingerprintExclusionOnly(excl model.FaceExclusion) string {
	raw, _ := json.Marshal(excl)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func fingerprintRescoreRun(run model.FaceQualityRescoreRun) string {
	raw, _ := json.Marshal(run)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func summarizeRetirement(items []RetirementItem) map[string]int {
	summary := map[string]int{}
	for _, it := range items {
		summary[it.Category]++
	}
	summary["total"] = len(items)
	return summary
}

// ApplyFaceQualityRetirement 按预演清单写入。指纹不匹配则整单中止。
// 不调用检测/复核模型；不触发全库重聚类。
func ApplyFaceQualityRetirement(db *gorm.DB, plan *RetirementPlan) (*RetirementApplyResult, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	if plan == nil {
		return nil, errors.New("plan is nil")
	}

	var existing model.AppConfig
	if err := db.Where("key = ?", FaceQualityRetirementMigrationKey).First(&existing).Error; err == nil {
		return &RetirementApplyResult{
			AppliedAt:    time.Now().UTC(),
			Changed:      nil,
			Summary:      map[string]int{"total": 0},
			Idempotent:   true,
			MigrationKey: FaceQualityRetirementMigrationKey,
		}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check migration marker: %w", err)
	}

	changed := make([]RetirementItem, 0)
	affectedPhotos := make(map[uint]struct{})
	now := time.Now().UTC()

	if err := db.Transaction(func(tx *gorm.DB) error {
		fresh, err := PlanFaceQualityRetirement(tx)
		if err != nil {
			return err
		}
		if err := assertRetirementFingerprints(plan, fresh); err != nil {
			return err
		}
		// Execute canonical database-derived actions only, never editable JSON fields.
		for _, item := range fresh.Items {
			switch item.Category {
			case RetirementCategoryKeepManual:
				continue
			case RetirementCategoryPromoteManual:
				if item.ExclusionID == 0 {
					return fmt.Errorf("promote_manual missing exclusion_id for face %d", item.FaceID)
				}
				if err := tx.Model(&model.FaceExclusion{}).Where("id = ?", item.ExclusionID).
					Updates(map[string]interface{}{
						"source":     model.ExclusionSourceManual,
						"updated_at": now,
					}).Error; err != nil {
					return err
				}
				changed = append(changed, item)
			case RetirementCategoryTransferManualOrphan:
				if item.FaceID == 0 {
					return fmt.Errorf("transfer_manual_orphan missing face_id")
				}
				var face model.Face
				if err := tx.First(&face, item.FaceID).Error; err != nil {
					return fmt.Errorf("load face %d for orphan transfer: %w", item.FaceID, err)
				}
				reason := item.ExclusionReason
				if !model.IsValidExclusionReason(reason) {
					reason = face.ExclusionReason
				}
				if !model.IsValidExclusionReason(reason) {
					reason = model.ExclusionReasonNonFace
				}
				excl := model.FaceExclusion{
					PhotoID:      face.PhotoID,
					SourceFaceID: face.ID,
					Reason:       reason,
					Source:       model.ExclusionSourceManual,
					BBoxX:        face.BBoxX,
					BBoxY:        face.BBoxY,
					BBoxWidth:    face.BBoxWidth,
					BBoxHeight:   face.BBoxHeight,
					CreatedAt:    now,
					UpdatedAt:    now,
				}
				if err := tx.Create(&excl).Error; err != nil {
					return fmt.Errorf("create orphan manual exclusion for face %d: %w", face.ID, err)
				}
				item.ExclusionID = excl.ID
				item.ExclusionSource = model.ExclusionSourceManual
				changed = append(changed, item)
			case RetirementCategoryClearAutoExclude:
				if item.ExclusionID != 0 {
					if err := tx.Delete(&model.FaceExclusion{}, item.ExclusionID).Error; err != nil {
						return err
					}
				}
				if item.FaceID != 0 {
					if err := tx.Model(&model.Face{}).Where("id = ?", item.FaceID).Updates(map[string]interface{}{
						"cluster_status":   model.FaceClusterStatusPending,
						"exclusion_reason": "",
						"excluded_at":      nil,
						"person_id":        nil,
						"updated_at":       now,
					}).Error; err != nil {
						return err
					}
					affectedPhotos[item.PhotoID] = struct{}{}
				}
				changed = append(changed, item)
			case RetirementCategoryClearAutoReview:
				if item.FaceID != 0 {
					if err := tx.Model(&model.Face{}).Where("id = ?", item.FaceID).Updates(map[string]interface{}{
						"cluster_status": model.FaceClusterStatusPending,
						"updated_at":     now,
					}).Error; err != nil {
						return err
					}
					affectedPhotos[item.PhotoID] = struct{}{}
				}
				changed = append(changed, item)
			case RetirementCategoryArchiveRun:
				if item.RescoreRunID == 0 {
					continue
				}
				if err := tx.Model(&model.FaceQualityRescoreRun{}).Where("id = ?", item.RescoreRunID).
					Updates(map[string]interface{}{
						"status":     model.FaceQualityRescoreStatusCancelled,
						"last_error": "retired: face quality module offline",
						"updated_at": now,
					}).Error; err != nil {
					return err
				}
				changed = append(changed, item)
			case RetirementCategoryUnknown:
				continue
			default:
				return fmt.Errorf("unknown retirement category %q", item.Category)
			}
		}

		photoIDs := make([]uint, 0, len(affectedPhotos))
		for id := range affectedPhotos {
			photoIDs = append(photoIDs, id)
		}
		if len(photoIDs) > 0 {
			if err := recomputePhotoFaceCountsTx(tx, photoIDs); err != nil {
				return err
			}
		}

		marker := model.AppConfig{
			Key:   FaceQualityRetirementMigrationKey,
			Value: fmt.Sprintf("done@%s", now.Format(time.RFC3339)),
		}
		if err := tx.Create(&marker).Error; err != nil {
			return fmt.Errorf("write migration marker: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &RetirementApplyResult{
		AppliedAt:    now,
		Changed:      changed,
		Summary:      summarizeRetirement(changed),
		Idempotent:   false,
		MigrationKey: FaceQualityRetirementMigrationKey,
	}, nil
}

func assertRetirementFingerprints(plan, fresh *RetirementPlan) error {
	if len(plan.Items) != len(fresh.Items) {
		return fmt.Errorf("%w: item set changed", errRetirementFingerprintMismatch)
	}
	seen := make(map[string]bool)
	freshByKey := make(map[string]RetirementItem, len(fresh.Items))
	for _, it := range fresh.Items {
		freshByKey[retirementItemKey(it)] = it
	}
	for _, it := range plan.Items {
		key := retirementItemKey(it)
		if seen[key] {
			return fmt.Errorf("%w: duplicate item", errRetirementFingerprintMismatch)
		}
		seen[key] = true
		cur, ok := freshByKey[key]
		if !ok {
			return fmt.Errorf("%w: missing item %s", errRetirementFingerprintMismatch, retirementItemKey(it))
		}
		approved, _ := json.Marshal(it)
		current, _ := json.Marshal(cur)
		if string(approved) != string(current) {
			return fmt.Errorf("%w: item %s changed (was %s/%s now %s/%s)",
				errRetirementFingerprintMismatch, retirementItemKey(it),
				it.Category, it.Fingerprint, cur.Category, cur.Fingerprint)
		}
	}
	return nil
}

func retirementItemKey(it RetirementItem) string {
	return fmt.Sprintf("%s|f=%d|e=%d|r=%d", it.Category, it.FaceID, it.ExclusionID, it.RescoreRunID)
}

func recomputePhotoFaceCountsTx(tx *gorm.DB, photoIDs []uint) error {
	for _, photoID := range photoIDs {
		var count int64
		if err := tx.Model(&model.Face{}).
			Where("photo_id = ? AND (cluster_status != ? OR exclusion_reason = ?)",
				photoID, model.FaceClusterStatusExcluded, model.ExclusionReasonLowQuality).
			Count(&count).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Photo{}).Where("id = ?", photoID).
			Update("face_count", count).Error; err != nil {
			return err
		}
	}
	return nil
}

func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table")
}

// MarshalRetirementPlanJSON 便于 CLI 输出。
func MarshalRetirementPlanJSON(plan *RetirementPlan) ([]byte, error) {
	return json.MarshalIndent(plan, "", "  ")
}
