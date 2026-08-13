package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// FaceQualityRescoreRunQuery 列表查询过滤条件。
type FaceQualityRescoreRunQuery struct {
	Status string // 可选：仅返回某状态
	Limit  int
}

// FaceQualityRescoreRunProgress 仅包含 worker 可安全刷新的统计字段。
// 严禁含 status/started_at/completed_at——这些只能由 TransitionRunStatus 条件写入，
// 否则 worker 手持过期内存对象会把 paused/cancelled 覆盖回 running。
type FaceQualityRescoreRunProgress struct {
	ProcessedFaceCount    int
	ProcessedPhotoCount   int
	AcceptedCount         int
	ReviewRequiredCount   int
	AutoExcludedCount     int
	RetryableCount        int
	SupersededManualCount int
	LastError             string
}

// FaceQualityRescoreRepository 管理历史重评分运行与目标快照。
type FaceQualityRescoreRepository interface {
	CreateRun(run *model.FaceQualityRescoreRun) error
	// UpdateRun 整行写回 run（db.Save）。仅供 run 创建/初始化或经条件检查后的非 worker 管理路径使用。
	// 后台 worker 的进度刷新不得调用本方法——它会把过期内存对象（含旧 status）整行覆盖，
	// 从而把 paused/cancelled 改回 running。worker 进度刷新一律用 UpdateRunProgress。
	UpdateRun(run *model.FaceQualityRescoreRun) error
	// UpdateRunProgress 只写统计字段，绝不触碰 status/started_at/completed_at。
	// worker 每批完成后用它持久化进度，保证并发暂停/取消不会被覆盖。
	UpdateRunProgress(runID uint, progress FaceQualityRescoreRunProgress) error
	// TransitionRunStatus 以 WHERE id=? AND status IN ? 做条件状态转换，返回是否命中。
	// false 表示并发方已改变状态，调用方不得重试或覆盖。completedAt 非空时一并写终态时间。
	TransitionRunStatus(runID uint, from []string, to string, completedAt *time.Time) (bool, error)
	GetRun(id uint) (*model.FaceQualityRescoreRun, error)
	ListRuns(q FaceQualityRescoreRunQuery) ([]*model.FaceQualityRescoreRun, error)
	// HasActiveRun 报告是否存在 status IN (running, paused) 的运行（单活跃 run 互斥）。
	HasActiveRun() (bool, error)
	// HasCompletedCalibration 报告是否存在已完成的 calibration 运行（full/enforce 前置条件）。
	// Deprecated: 保留向后兼容；full/enforce 门禁改用 GetEligibleCalibration 逐项验证。
	HasCompletedCalibration() (bool, error)
	// GetEligibleCalibration 读取并逐项验证某 run 是否为合格校准（计划 §3.4）。
	// 返回 (run, eligible, error)。run 为 nil 且 err 为 gorm.ErrRecordNotFound 时表示不存在。
	GetEligibleCalibration(runID uint) (*model.FaceQualityRescoreRun, error)
	// CountPendingOrProcessing 报告某 run 是否仍有未到终态的 item（pending/processing）。
	CountPendingOrProcessing(runID uint) (int64, error)
	// ListRetryableTargets 列出某来源 run 的当前 historical_rescore + retryable_error|unmatched 事件，
	// 供 retry 创建新 shadow calibration 精确快照失败集合。窄查询，不扫全部历史缺证据。
	ListRetryableTargets(sourceRunID uint) ([]model.FaceQualityRescoreRetryTarget, error)
	// ListV2SnapshotTargets 选择没有当前 source=manual 结论、且没有当前 evidence_pipeline=independent_v2
	// 事件的 Face（以 faces.id 为主体）。这是 v2 历史快照的目标集合，不限于 historical_backfill + missing。
	// photoLimit>0 时按 photo_id 升序限制快照照片数（校准用）。
	ListV2SnapshotTargets(photoLimit int) ([]model.FaceQualityRescoreRetryTarget, error)

	CreateItems(items []*model.FaceQualityRescoreItem) error
	ListItemsByRun(runID uint) ([]*model.FaceQualityRescoreItem, error)
	// ClaimNextPhotoItems 领取一组属于同一照片、status=pending 的 item（按 photo_id 分组），
	// 把它们置为 processing 并返回。无 pending 时返回空切片。
	// 本方法不做 run 状态门禁，仅供进程重启/测试等不需要状态校验的路径使用；
	// v1/v2 worker 一律改用 ClaimNextPhotoItemsWhenRunning。
	ClaimNextPhotoItems(runID uint) ([]*model.FaceQualityRescoreItem, error)
	// ClaimNextPhotoItemsWhenRunning 在同一事务内先断言 run.status='running'，
	// 不满足时返回空集且不更新任何 item，从而阻止暂停/取消中的 run 领取下一批照片。
	ClaimNextPhotoItemsWhenRunning(runID uint) ([]*model.FaceQualityRescoreItem, error)
	UpdateItem(item *model.FaceQualityRescoreItem) error
	// ResetProcessingItems 进程重启时把 processing item 回到 pending（不丢失进度）。
	ResetProcessingItems(runID uint) (int64, error)
	// CountItemsByStatus 按状态统计某 run 的 item 数。
	CountItemsByStatus(runID uint, status string) (int64, error)
	// CountTerminalPhotos 统计某 run 已到终态（item status 不为 pending/processing）的去重照片数。
	CountTerminalPhotos(runID uint) (int, error)
	// LatestItemError 返回某 run 最近一条非空 item last_error（按 id 倒序），无则返回空串。
	LatestItemError(runID uint) (string, error)
	// ListAutoExcludedByRun 列出某 run 产生的自动排除事件（rescore_run_id 匹配），
	// 供 run 级 restore-auto 精确恢复。
	ListAutoExcludedByRun(runID uint, limit int) ([]*model.FaceQualityEvent, error)
}

type faceQualityRescoreRepository struct {
	db *gorm.DB
}

func NewFaceQualityRescoreRepository(db *gorm.DB) FaceQualityRescoreRepository {
	return &faceQualityRescoreRepository{db: db}
}

func (r *faceQualityRescoreRepository) CreateRun(run *model.FaceQualityRescoreRun) error {
	return r.db.Create(run).Error
}

func (r *faceQualityRescoreRepository) UpdateRun(run *model.FaceQualityRescoreRun) error {
	return r.db.Save(run).Error
}

// UpdateRunProgress 只写统计字段（map 形式，不含 status/started_at/completed_at），
// 供 worker 每批完成后的进度刷新。updated_at 显式写入——GORM 对 map 更新不会自动维护。
func (r *faceQualityRescoreRepository) UpdateRunProgress(runID uint, p FaceQualityRescoreRunProgress) error {
	return r.db.Model(&model.FaceQualityRescoreRun{}).
		Where("id = ?", runID).
		Updates(map[string]any{
			"processed_face_count":    p.ProcessedFaceCount,
			"processed_photo_count":   p.ProcessedPhotoCount,
			"accepted_count":          p.AcceptedCount,
			"review_required_count":   p.ReviewRequiredCount,
			"auto_excluded_count":     p.AutoExcludedCount,
			"retryable_count":         p.RetryableCount,
			"superseded_manual_count": p.SupersededManualCount,
			"last_error":              p.LastError,
			"updated_at":              rescoreRunTimestamp(),
		}).Error
}

// TransitionRunStatus 以条件 UPDATE 完成状态转换：仅当当前 status ∈ from 时写 to。
// 返回 (true,nil) 表示命中并转换；(false,nil) 表示状态已被并发方改变，调用方不得重试或覆盖。
// completedAt 非空时一并写入终态时间。
func (r *faceQualityRescoreRepository) TransitionRunStatus(runID uint, from []string, to string, completedAt *time.Time) (bool, error) {
	updates := map[string]any{
		"status":     to,
		"updated_at": rescoreRunTimestamp(),
	}
	if completedAt != nil {
		updates["completed_at"] = *completedAt
	}
	res := r.db.Model(&model.FaceQualityRescoreRun{}).
		Where("id = ? AND status IN ?", runID, from).
		Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *faceQualityRescoreRepository) GetRun(id uint) (*model.FaceQualityRescoreRun, error) {
	var run model.FaceQualityRescoreRun
	if err := r.db.Where("id = ?", id).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *faceQualityRescoreRepository) ListRuns(q FaceQualityRescoreRunQuery) ([]*model.FaceQualityRescoreRun, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	tx := r.db.Model(&model.FaceQualityRescoreRun{})
	if q.Status != "" {
		tx = tx.Where("status = ?", q.Status)
	}
	var runs []*model.FaceQualityRescoreRun
	err := tx.Order("id DESC").Limit(limit).Find(&runs).Error
	return runs, err
}

func (r *faceQualityRescoreRepository) HasActiveRun() (bool, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreRun{}).
		Where("status IN ?", []string{model.FaceQualityRescoreStatusRunning, model.FaceQualityRescoreStatusPaused}).
		Count(&cnt).Error
	return cnt > 0, err
}

func (r *faceQualityRescoreRepository) HasCompletedCalibration() (bool, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreRun{}).
		Where("mode = ? AND status = ?", model.FaceQualityRescoreModeCalibration, model.FaceQualityRescoreStatusCompleted).
		Count(&cnt).Error
	return cnt > 0, err
}

// GetEligibleCalibration 读取 run 并执行计划 §3.4 的合格校准逐项验证：
//   - mode=calibration、apply_mode=shadow、status=completed（completed_with_errors 不合格）；
//   - target_face_count > 0；
//   - retryable_count == 0；
//   - processed_face_count + superseded_manual_count == target_face_count（计数闭合）；
//   - 无 pending/processing item。
//
// 返回 (run, true, nil) 合格；(run, false, nil) 存在但不合格；nil,gorm.ErrRecordNotFound 不存在。
func (r *faceQualityRescoreRepository) GetEligibleCalibration(runID uint) (*model.FaceQualityRescoreRun, error) {
	run, err := r.GetRun(runID)
	if err != nil {
		return nil, err
	}
	return run, nil
}

// CountPendingOrProcessing 报告某 run 仍处于 pending/processing 的 item 数。
func (r *faceQualityRescoreRepository) CountPendingOrProcessing(runID uint) (int64, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status IN ?", runID, []string{
			model.FaceQualityRescoreItemStatusPending,
			model.FaceQualityRescoreItemStatusProcessing,
		}).
		Count(&cnt).Error
	return cnt, err
}

// ListRetryableTargets 列出某来源 run 的当前失败事件（historical_rescore + retryable_error|unmatched），
// 按 photo_id、id 升序返回，供 retry 精确快照。
func (r *faceQualityRescoreRepository) ListRetryableTargets(sourceRunID uint) ([]model.FaceQualityRescoreRetryTarget, error) {
	type row struct {
		PhotoID       uint    `gorm:"column:photo_id"`
		FaceID        *uint   `gorm:"column:face_id"`
		BBoxX         float64 `gorm:"column:bbox_x"`
		BBoxY         float64 `gorm:"column:bbox_y"`
		BBoxWidth     float64 `gorm:"column:bbox_width"`
		BBoxHeight    float64 `gorm:"column:bbox_height"`
		ID            uint    `gorm:"column:id"`
		EvidenceState string  `gorm:"column:evidence_state"`
	}
	var rows []row
	err := r.db.Model(&model.FaceQualityEvent{}).
		Select("photo_id, face_id, bbox_x, bbox_y, bbox_width, bbox_height, id, evidence_state").
		Where("is_current = ? AND rescore_run_id = ? AND evidence_origin = ?",
			true, sourceRunID, model.FaceQualityEvidenceOriginHistoricalRescore).
		Where("evidence_state IN ?", []string{
			model.FaceQualityEvidenceStateRetryableError,
			model.FaceQualityEvidenceStateUnmatched,
		}).
		Where("face_id IS NOT NULL").
		Order("photo_id ASC, id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.FaceQualityRescoreRetryTarget, 0, len(rows))
	for _, r := range rows {
		if r.FaceID == nil {
			continue
		}
		out = append(out, model.FaceQualityRescoreRetryTarget{
			PhotoID:         r.PhotoID,
			FaceID:          *r.FaceID,
			BBoxX:           r.BBoxX,
			BBoxY:           r.BBoxY,
			BBoxWidth:       r.BBoxWidth,
			BBoxHeight:      r.BBoxHeight,
			BaselineEventID: r.ID,
			EvidenceState:   r.EvidenceState,
		})
	}
	return out, nil
}

// ListV2SnapshotTargets 选择 v2 历史快照目标集合（以 faces.id 为主体）：
//   - 没有 is_current=true 且 source=manual 的质检事件（人工结论优先，跳过）；
//   - 没有 is_current=true 且 evidence_pipeline=independent_v2 的质检事件（避免重复 v2 复核）。
//
// 已自动隔离而没有人工最终结论的 Face 仍进入快照（由 service 层决定不自动恢复，
// 冲突时写 review_required + auto_decision_conflict）。
// 返回每个 Face 的归一化 BBox 及其当前 baseline 事件 ID（无当前事件则 BaselineEventID=0）。
// photoLimit>0 时按 photo_id 升序限制快照照片数（校准用）。
//
// 注意：faces 表的 BBox 列由 GORM 默认 snake_case 映射为 b_box_x（Face 模型未显式 column），
// 而 face_quality_events 显式指定为 bbox_x。这里用 faces.b_box_x 引用前者。
func (r *faceQualityRescoreRepository) ListV2SnapshotTargets(photoLimit int) ([]model.FaceQualityRescoreRetryTarget, error) {
	type row struct {
		PhotoID         uint    `gorm:"column:photo_id"`
		FaceID          uint    `gorm:"column:face_id"`
		BBoxX           float64 `gorm:"column:bbox_x"`
		BBoxY           float64 `gorm:"column:bbox_y"`
		BBoxWidth       float64 `gorm:"column:bbox_width"`
		BBoxHeight      float64 `gorm:"column:bbox_height"`
		BaselineEventID *uint   `gorm:"column:baseline_event_id"`
	}

	// photoLimit 限制：先取满足条件的去重 photo_id 子集，再限定到这些 photo。
	// 用 NOT EXISTS 而非 NOT IN：NOT IN 在 SQLite 上易退化为全表扫描，
	// idx_fqe_evidence_pipeline (face_id, evidence_pipeline, is_current) 对 NOT EXISTS 子查询更友好。
	basePhotoIDs := r.db.Model(&model.Face{}).
		Where("NOT EXISTS (SELECT 1 FROM face_quality_events mq WHERE mq.face_id = faces.id "+
			"AND mq.is_current = ? AND mq.source = ?)",
			true, model.FaceQualitySourceManual).
		Where("NOT EXISTS (SELECT 1 FROM face_quality_events vq WHERE vq.face_id = faces.id "+
			"AND vq.is_current = ? AND vq.evidence_pipeline = ?)",
			true, model.FaceQualityEvidencePipelineIndependentV2)

	if photoLimit > 0 {
		var photoIDs []uint
		if err := basePhotoIDs.Session(&gorm.Session{}).
			Distinct("photo_id").
			Order("photo_id ASC").
			Limit(photoLimit).
			Pluck("photo_id", &photoIDs).Error; err != nil {
			return nil, err
		}
		if len(photoIDs) == 0 {
			return nil, nil
		}
		basePhotoIDs = basePhotoIDs.Where("faces.photo_id IN ?", photoIDs)
	}

	var rows []row
	// 以 faces 为驱动表 LEFT JOIN 当前事件取 baseline（每个 face 至多一条 is_current 事件；
	// 若多条，取 id 最大者作为 baseline）。
	err := basePhotoIDs.Session(&gorm.Session{}).
		Select("faces.photo_id AS photo_id, faces.id AS face_id, faces.b_box_x AS bbox_x, "+
			"faces.b_box_y AS bbox_y, faces.b_box_width AS bbox_width, faces.b_box_height AS bbox_height, "+
			"cur.id AS baseline_event_id").
		Joins("LEFT JOIN face_quality_events cur ON cur.face_id = faces.id AND cur.is_current = ? "+
			"AND cur.id = (SELECT MAX(id) FROM face_quality_events WHERE face_id = faces.id AND is_current = ?)",
			true, true).
		Order("faces.photo_id ASC, faces.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]model.FaceQualityRescoreRetryTarget, 0, len(rows))
	for _, r := range rows {
		t := model.FaceQualityRescoreRetryTarget{
			PhotoID:    r.PhotoID,
			FaceID:     r.FaceID,
			BBoxX:      r.BBoxX,
			BBoxY:      r.BBoxY,
			BBoxWidth:  r.BBoxWidth,
			BBoxHeight: r.BBoxHeight,
		}
		if r.BaselineEventID != nil {
			t.BaselineEventID = *r.BaselineEventID
		}
		out = append(out, t)
	}
	return out, nil
}

func (r *faceQualityRescoreRepository) CreateItems(items []*model.FaceQualityRescoreItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.CreateInBatches(items, 500).Error
}

func (r *faceQualityRescoreRepository) ListItemsByRun(runID uint) ([]*model.FaceQualityRescoreItem, error) {
	var items []*model.FaceQualityRescoreItem
	err := r.db.Where("run_id = ?", runID).Order("photo_id ASC, id ASC").Find(&items).Error
	return items, err
}

// ClaimNextPhotoItems 领取下一个待处理照片的全部 pending item。
// 在一个事务中：找到最小 photo_id（有 pending item），把该照片所有 pending item 置 processing。
// 本方法不做 run 状态门禁，仅供进程重启/测试等不需要状态校验的路径使用。
func (r *faceQualityRescoreRepository) ClaimNextPhotoItems(runID uint) ([]*model.FaceQualityRescoreItem, error) {
	var items []*model.FaceQualityRescoreItem
	err := r.db.Transaction(func(tx *gorm.DB) error {
		return claimNextPhotoItemsTx(tx, runID, &items)
	})
	return items, err
}

// ClaimNextPhotoItemsWhenRunning 在同一事务内先断言 run.status='running'，
// 不满足时返回空集且不更新任何 item。阻止暂停/取消中的 run 领取下一批照片。
// run 不存在视为非 running（返回空集），避免对幻影 run 领取。
func (r *faceQualityRescoreRepository) ClaimNextPhotoItemsWhenRunning(runID uint) ([]*model.FaceQualityRescoreItem, error) {
	var items []*model.FaceQualityRescoreItem
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 断言 run.status='running'。Pluck 无记录时不返回 ErrRecordNotFound，status 留空串，
		// 自然落入「非 running」分支返回空集——故无需特殊处理 ErrRecordNotFound。
		var status string
		if err := tx.Model(&model.FaceQualityRescoreRun{}).
			Where("id = ?", runID).
			Pluck("status", &status).Error; err != nil {
			return err
		}
		if status != model.FaceQualityRescoreStatusRunning {
			return nil
		}
		return claimNextPhotoItemsTx(tx, runID, &items)
	})
	return items, err
}

// claimNextPhotoItemsTx 在已开启的事务内领取下一个照片的全部 pending item。
// 找到最小 pending photo_id，把该照片所有 pending item 置 processing 并回读。
func claimNextPhotoItemsTx(tx *gorm.DB, runID uint, items *[]*model.FaceQualityRescoreItem) error {
	var photoID uint
	err := tx.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status = ?", runID, model.FaceQualityRescoreItemStatusPending).
		Order("photo_id ASC").
		Limit(1).
		Pluck("photo_id", &photoID).Error
	if err != nil {
		return err
	}
	if photoID == 0 {
		return nil // 无 pending
	}

	if err := tx.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND photo_id = ? AND status = ?", runID, photoID, model.FaceQualityRescoreItemStatusPending).
		Update("status", model.FaceQualityRescoreItemStatusProcessing).Error; err != nil {
		return err
	}

	return tx.Where("run_id = ? AND photo_id = ? AND status = ?", runID, photoID, model.FaceQualityRescoreItemStatusProcessing).
		Order("id ASC").Find(items).Error
}

func (r *faceQualityRescoreRepository) UpdateItem(item *model.FaceQualityRescoreItem) error {
	return r.db.Save(item).Error
}

func (r *faceQualityRescoreRepository) ResetProcessingItems(runID uint) (int64, error) {
	res := r.db.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status = ?", runID, model.FaceQualityRescoreItemStatusProcessing).
		Update("status", model.FaceQualityRescoreItemStatusPending)
	return res.RowsAffected, res.Error
}

func (r *faceQualityRescoreRepository) CountItemsByStatus(runID uint, status string) (int64, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status = ?", runID, status).
		Count(&cnt).Error
	return cnt, err
}

// CountTerminalPhotos 统计某 run 已到终态（status 不为 pending/processing）的去重照片数。
func (r *faceQualityRescoreRepository) CountTerminalPhotos(runID uint) (int, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status NOT IN ?", runID, []string{
			model.FaceQualityRescoreItemStatusPending,
			model.FaceQualityRescoreItemStatusProcessing,
		}).
		Distinct("photo_id").Count(&cnt).Error
	return int(cnt), err
}

// LatestItemError 返回某 run 最近一条非空 last_error（按 id 倒序），无则返回空串。
func (r *faceQualityRescoreRepository) LatestItemError(runID uint) (string, error) {
	var item model.FaceQualityRescoreItem
	err := r.db.Where("run_id = ? AND last_error != ''", runID).
		Order("id DESC").Limit(1).First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return item.LastError, nil
}

func (r *faceQualityRescoreRepository) ListAutoExcludedByRun(runID uint, limit int) ([]*model.FaceQualityEvent, error) {
	if limit <= 0 {
		limit = 5000
	}
	var records []*model.FaceQualityEvent
	err := r.db.Where("rescore_run_id = ? AND source = ? AND is_current = ?",
		runID, model.FaceQualitySourceAuto, true).
		Where("decision IN ?", []string{model.FaceQualityDecisionNonFace, model.FaceQualityDecisionLowQuality}).
		Where("face_id IS NOT NULL").
		Limit(limit).Find(&records).Error
	return records, err
}

// rescoreRunTimestamp 用于测试注入确定性时间。
var rescoreRunTimestamp = func() time.Time { return time.Now().UTC() }
