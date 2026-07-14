package repository

import (
	"fmt"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)
// PersonPhotoRepository 维护与查询人物照片分页索引派生表 person_photos。
//
// 该表的权威写入路径是 SQLite trigger（见 pkg/database 的 person_photos 迁移），
// 仓库方法主要供回填、一致性校验与切换后的 cursor 查询使用。
type PersonPhotoRepository interface {
	// MigrateStatus 读取/写入回填进度（app_config）。
	GetMigrationStatus(tx *gorm.DB) (status string, lastFaceID uint, err error)
	SetMigrationStatus(tx *gorm.DB, status string, lastFaceID uint) error
	// v2 修复流程的状态键（与 v1 分键）。
	GetMigrationStatusV2(tx *gorm.DB) (status string, lastFaceID uint, err error)
	SetMigrationStatusV2(tx *gorm.DB, status string, lastFaceID uint) error

	// BackfillBatch 从 lastFaceID 之后扫描一批有效人脸，按 (person_id, photo_id) 去重
	// 插入 person_photos，返回本批处理到的最大 face id 与插入条数。
	BackfillBatch(tx *gorm.DB, lastFaceID uint, batchSize int) (newLastFaceID uint, inserted int, err error)

	// RunConsistencyCheck 校验 person_photos 与有效 faces 的一致性，返回结构化报告。
	// 报告中每个计数独立可见，便于日志定位根因（旧版只返回聚合 int，无法区分缺失/多余/孤儿）。
	RunConsistencyCheck(tx *gorm.DB) (PersonPhotosConsistencyReport, error)

	// ListPhotoIDsByPersonCursor 从 person_photos 按 taken_at DESC, photo_id DESC 读取
	// 一页 photo_id（limit+1 判定 hasMore）。cursor 为 keyset 游标，nil 表示首页。
	ListPhotoIDsByPersonCursor(personID uint, cursor *PersonPhotoCursor, limit int) (ids []uint, hasMore bool, nextCursor *PersonPhotoCursor, err error)

	// RepairBatch 执行一轮修复：删除 extra/orphan 关联、补齐 missing、同步 taken_at。
	// 返回本轮修复的各类计数。修复是幂等、分批的，不阻塞服务启动，重启后可继续。
	RepairBatch(tx *gorm.DB, batchSize int) (PersonPhotosRepairDelta, error)

	// RunVerification 跑一致性校验，不一致返回 error。
	RunVerification(tx *gorm.DB) error

	// MigrationReady 报告回填是否完成且一致性校验通过（v1/v2 状态任一为 ready）。
	MigrationReady(tx *gorm.DB) (bool, error)
}

// PersonPhotosConsistencyReport 是 person_photos 一致性校验的结构化报告。
// 每个字段独立计数，日志应逐项打印，便于定位是哪类不一致（缺失/多余/孤儿/taken_at/人物数）。
type PersonPhotosConsistencyReport struct {
	MissingAssociations int64 // 有效 face 存在但 person_photos 缺失对应 (person,photo)
	ExtraAssociations   int64 // person_photos 存在但无有效 face 对应（多收录）
	OrphanPhotos        int64 // person_photos 引用的 photo 已被物理删除
	TakenAtMismatches   int64 // person_photos.taken_at 与 photos.taken_at 不一致
	PersonCountMismatch int64 // 按人物聚合后，有效 face 的 DISTINCT photo 数与 person_photos 记录数不匹配
}

// IsClean 报告是否所有不一致计数均为 0。
func (r PersonPhotosConsistencyReport) IsClean() bool {
	return r.MissingAssociations == 0 &&
		r.ExtraAssociations == 0 &&
		r.OrphanPhotos == 0 &&
		r.TakenAtMismatches == 0 &&
		r.PersonCountMismatch == 0
}

// PersonPhotosRepairDelta 是单轮修复的增量计数。
type PersonPhotosRepairDelta struct {
	ExtraDeleted       int64 // 删除的 extra 关联（无有效 face）
	OrphanDeleted      int64 // 删除的孤儿关联（photo 已删）
	MissingInserted    int64 // 补齐的 missing 关联
	TakenAtFixed       int64 // 同步修复的 taken_at
	RemainingMissing   int64 // 本轮未补齐（受 batchSize 限制），>0 表示需继续修复
	RemainingExtra     int64
	RemainingOrphan    int64
	RemainingTakenAt   int64
}

type personPhotoRepository struct {
	db *gorm.DB
}

func NewPersonPhotoRepository(db *gorm.DB) PersonPhotoRepository {
	return &personPhotoRepository{db: db}
}

const (
	// v1 状态键（历史回填 + 一致性校验）。v2 修复流程兼容并复用 v1 ready 标记：
	// 修复通过后同时把 v1/v2 标为 ready，handler 的 MigrationReady 检查任一 ready 即放行索引查询。
	personPhotoMigrationKey      = "migration.person_photos_v1.status"
	personPhotoLastFaceIDKey     = "migration.person_photos_v1.last_face_id"
	personPhotoMigrationKeyV2    = "migration.person_photos_v2.status"
	personPhotoLastFaceIDKeyV2   = "migration.person_photos_v2.last_face_id"
	personPhotoStatusBackfilling = "backfilling"
	personPhotoStatusVerifying   = "verifying"
	personPhotoStatusRepairing   = "repairing"
	personPhotoStatusReady       = "ready"
)

// GetMigrationStatus 读取回填状态与进度。
// 用原生查询绕过 app_config 软删除（GORM First 会忽略 deleted_at 非空行）。
func (r *personPhotoRepository) GetMigrationStatus(tx *gorm.DB) (string, uint, error) {
	db := tx
	if db == nil {
		db = r.db
	}
	var statusVal *string
	if err := db.Raw(`SELECT value FROM app_config WHERE key = ? LIMIT 1`, personPhotoMigrationKey).Scan(&statusVal).Error; err != nil {
		return "", 0, err
	}
	status := ""
	if statusVal != nil {
		status = *statusVal
	}

	var lastVal *string
	var lastFaceID uint
	if err := db.Raw(`SELECT value FROM app_config WHERE key = ? LIMIT 1`, personPhotoLastFaceIDKey).Scan(&lastVal).Error; err != nil {
		return status, 0, err
	}
	if lastVal != nil {
		var v uint64
		for _, c := range *lastVal {
			if c < '0' || c > '9' {
				continue
			}
			v = v*10 + uint64(c-'0')
		}
		lastFaceID = uint(v)
	}
	return status, lastFaceID, nil
}

// SetMigrationStatus 写入回填状态与进度。事务可选。
func (r *personPhotoRepository) SetMigrationStatus(tx *gorm.DB, status string, lastFaceID uint) error {
	db := tx
	if db == nil {
		db = r.db
	}
	if err := upsertAppConfig(db, personPhotoMigrationKey, status); err != nil {
		return err
	}
	return upsertAppConfig(db, personPhotoLastFaceIDKey, uintToStr(lastFaceID))
}

// upsertAppConfig 用 ON CONFLICT 更新 app_config 单行。
// 注意：app_config 有 deleted_at 软删除列与 uniqueIndex:idx_key，GORM First 走软删除；
// 这里用原生 INSERT...ON CONFLICT(key) 绕过软删除，保证迁移标记可被任意连接读写。
func upsertAppConfig(db *gorm.DB, key, value string) error {
	return db.Exec(
		`INSERT INTO app_config(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value,
	).Error
}

func uintToStr(v uint) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// BackfillBatch 从 lastFaceID 之后扫描一批有效人脸（person_id 非空且非 excluded），
// 连同其照片 taken_at 一次性插入 person_photos（ON CONFLICT 忽略，保证去重）。
func (r *personPhotoRepository) BackfillBatch(tx *gorm.DB, lastFaceID uint, batchSize int) (uint, int, error) {
	db := tx
	if db == nil {
		db = r.db
	}
	if batchSize <= 0 {
		batchSize = 500
	}

	type faceRow struct {
		ID       uint       `gorm:"column:id"`
		PersonID *uint      `gorm:"column:person_id"`
		PhotoID  uint       `gorm:"column:photo_id"`
		TakenAt  *time.Time `gorm:"column:taken_at"`
	}

	var rows []faceRow
	if err := db.Table("faces").
		Select("faces.id, faces.person_id, faces.photo_id, photos.taken_at").
		Joins("INNER JOIN photos ON photos.id = faces.photo_id").
		Where("faces.id > ? AND faces.person_id IS NOT NULL AND faces.person_id != 0 AND faces.cluster_status != ?",
			lastFaceID, model.FaceClusterStatusExcluded).
		Order("faces.id ASC").
		Limit(batchSize).
		Scan(&rows).Error; err != nil {
		return lastFaceID, 0, err
	}
	if len(rows) == 0 {
		return lastFaceID, 0, nil
	}

	newLast := lastFaceID
	// 收集本批有效行，过滤掉 person_id 为空/0 的（理论上面已过滤，二次保险）。
	type insertRow struct {
		PersonID uint
		PhotoID  uint
		TakenAt  *time.Time
	}
	toInsert := make([]insertRow, 0, len(rows))
	for _, row := range rows {
		newLast = row.ID
		if row.PersonID == nil || *row.PersonID == 0 {
			continue
		}
		toInsert = append(toInsert, insertRow{PersonID: *row.PersonID, PhotoID: row.PhotoID, TakenAt: row.TakenAt})
	}

	inserted := 0
	if len(toInsert) == 0 {
		// 无可插入行，但仍推进 newLast，避免下次重复扫描空批。
		return newLast, 0, nil
	}

	// 批量插入：每 chunk 用一条多值 INSERT INTO ... VALUES (...), (...) + ON CONFLICT DO NOTHING，
	// 比逐条 INSERT 快得多（NAS 上 fsync 摊薄）。chunk 大小受 SQLite 变量数限制（999）。
	const chunkSize = 200 // 3 列 × 200 = 600 占位符，安全
	if err := db.Transaction(func(tx2 *gorm.DB) error {
		for start := 0; start < len(toInsert); start += chunkSize {
			end := start + chunkSize
			if end > len(toInsert) {
				end = len(toInsert)
			}
			chunk := toInsert[start:end]
			placeholders := make([]string, len(chunk))
			args := make([]interface{}, 0, len(chunk)*3)
			for i, r := range chunk {
				placeholders[i] = "(?, ?, ?)"
				args = append(args, r.PersonID, r.PhotoID, r.TakenAt)
				inserted++
			}
			sql := "INSERT INTO person_photos(person_id, photo_id, taken_at) VALUES " +
				strings.Join(placeholders, ", ") +
				" ON CONFLICT(person_id, photo_id) DO NOTHING"
			if err := tx2.Exec(sql, args...).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return lastFaceID, 0, err
	}

	return newLast, inserted, nil
}

// RunConsistencyCheck 校验 person_photos 与有效 faces 一致性，返回结构化报告。
//
// 五类独立计数：
//  1. MissingAssociations：有效 face（person_id 非空非 0、非 excluded）存在但 person_photos 缺失对应 (person,photo)。
//  2. ExtraAssociations：person_photos 存在但无任何有效 face 对应（excluded face 误收录或已失效）。
//  3. OrphanPhotos：person_photos 引用的 photo 已被物理删除。
//  4. TakenAtMismatches：person_photos.taken_at 与 photos.taken_at 不一致。
//  5. PersonCountMismatch：按人物聚合后，有效 face 的 DISTINCT photo 数与 person_photos 记录数不匹配。
func (r *personPhotoRepository) RunConsistencyCheck(tx *gorm.DB) (PersonPhotosConsistencyReport, error) {
	db := tx
	if db == nil {
		db = r.db
	}
	var rep PersonPhotosConsistencyReport

	// 1. Missing：有效 face 但 person_photos 缺失。
	if err := db.Raw(`
		SELECT COUNT(*) FROM (
			SELECT DISTINCT f.person_id, f.photo_id
			FROM faces f
			WHERE f.person_id IS NOT NULL AND f.person_id != 0 AND f.cluster_status != ?
			  AND NOT EXISTS (
				SELECT 1 FROM person_photos pp
				WHERE pp.person_id = f.person_id AND pp.photo_id = f.photo_id
			  )
		)`, model.FaceClusterStatusExcluded).Scan(&rep.MissingAssociations).Error; err != nil {
		return rep, err
	}

	// 2. Extra：person_photos 存在但无有效 face。
	if err := db.Raw(`
		SELECT COUNT(*) FROM person_photos pp
		WHERE NOT EXISTS (
			SELECT 1 FROM faces f
			WHERE f.person_id = pp.person_id AND f.photo_id = pp.photo_id
			  AND f.cluster_status != ?
		)`, model.FaceClusterStatusExcluded).Scan(&rep.ExtraAssociations).Error; err != nil {
		return rep, err
	}

	// 3. Orphan：person_photos 引用的 photo 已物理删除。
	if err := db.Raw(`
		SELECT COUNT(*) FROM person_photos pp
		WHERE NOT EXISTS (SELECT 1 FROM photos p WHERE p.id = pp.photo_id)
	`).Scan(&rep.OrphanPhotos).Error; err != nil {
		return rep, err
	}

	// 4. taken_at 不一致。
	if err := db.Raw(`
		SELECT COUNT(*) FROM person_photos pp
		JOIN photos p ON p.id = pp.photo_id
		WHERE (pp.taken_at IS NULL) != (p.taken_at IS NULL)
		   OR (pp.taken_at IS NOT NULL AND p.taken_at IS NOT NULL
		       AND pp.taken_at != p.taken_at)
	`).Scan(&rep.TakenAtMismatches).Error; err != nil {
		return rep, err
	}

	// 5. 按人物聚合：有效 face DISTINCT photo 数 vs person_photos 记录数。
	//    两段 LEFT JOIN（兼容不支持 FULL OUTER JOIN 的旧 SQLite）。
	var leftDiff, rightDiff int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM (
			SELECT fv.person_id
			FROM (
				SELECT person_id, COUNT(DISTINCT photo_id) AS cnt
				FROM faces
				WHERE person_id IS NOT NULL AND person_id != 0 AND cluster_status != ?
				GROUP BY person_id
			) fv
			LEFT JOIN (
				SELECT person_id, COUNT(*) AS cnt FROM person_photos GROUP BY person_id
			) pp ON pp.person_id = fv.person_id
			WHERE COALESCE(fv.cnt,0) != COALESCE(pp.cnt,0)
		)`, model.FaceClusterStatusExcluded).Scan(&leftDiff).Error; err != nil {
		return rep, err
	}
	if err := db.Raw(`
		SELECT COUNT(*) FROM (
			SELECT pp.person_id
			FROM (
				SELECT person_id, COUNT(*) AS cnt FROM person_photos GROUP BY person_id
			) pp
			LEFT JOIN (
				SELECT person_id, COUNT(DISTINCT photo_id) AS cnt
				FROM faces
				WHERE person_id IS NOT NULL AND person_id != 0 AND cluster_status != ?
				GROUP BY person_id
			) fv ON fv.person_id = pp.person_id
			WHERE COALESCE(pp.cnt,0) != COALESCE(fv.cnt,0)
		)`, model.FaceClusterStatusExcluded).Scan(&rightDiff).Error; err != nil {
		return rep, err
	}
	rep.PersonCountMismatch = leftDiff + rightDiff

	return rep, nil
}

// MigrationReady 报告回填是否完成且校验通过（v1/v2 任一 ready 即放行索引查询）。
func (r *personPhotoRepository) RunVerification(tx *gorm.DB) error {
	rep, err := r.RunConsistencyCheck(tx)
	if err != nil {
		return err
	}
	if !rep.IsClean() {
		return fmt.Errorf("person_photos consistency check failed: missing=%d extra=%d orphan=%d taken_at=%d person_count=%d",
			rep.MissingAssociations, rep.ExtraAssociations, rep.OrphanPhotos, rep.TakenAtMismatches, rep.PersonCountMismatch)
	}
	return nil
}

func (r *personPhotoRepository) MigrationReady(tx *gorm.DB) (bool, error) {
	status, _, err := r.GetMigrationStatus(tx)
	if err != nil {
		return false, err
	}
	if status == personPhotoStatusReady {
		return true, nil
	}
	// 兼容 v2 状态键。
	statusV2, _, err := r.getMigrationStatusKey(tx, personPhotoMigrationKeyV2)
	if err != nil {
		return false, err
	}
	return statusV2 == personPhotoStatusReady, nil
}

// RepairBatch 执行一轮 person_photos 修复（幂等、分批，不阻塞启动）。
//
// 修复顺序（与一致性报告对应）：
//  1. 删除 extra 关联（无有效 face）。
//  2. 删除 orphan 关联（photo 已物理删除）。
//  3. 补齐 missing 关联（受 batchSize 限制，返回 RemainingMissing）。
//  4. 同步 taken_at 不一致（受 batchSize 限制）。
//
// 不得删除并重建整个表。修复后调用方应重新校验；只有所有计数为 0 才标记 ready。
func (r *personPhotoRepository) RepairBatch(tx *gorm.DB, batchSize int) (PersonPhotosRepairDelta, error) {
	db := tx
	if db == nil {
		db = r.db
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	var delta PersonPhotosRepairDelta

	// 1. 删除 extra：person_photos 无有效 face 对应的行。
	res := db.Exec(`
		DELETE FROM person_photos
		WHERE rowid IN (
			SELECT pp.rowid FROM person_photos pp
			WHERE NOT EXISTS (
				SELECT 1 FROM faces f
				WHERE f.person_id = pp.person_id AND f.photo_id = pp.photo_id
				  AND f.cluster_status != ?
			)
			LIMIT ?
		)`, model.FaceClusterStatusExcluded, batchSize)
	if res.Error != nil {
		return delta, res.Error
	}
	delta.ExtraDeleted = res.RowsAffected

	// 2. 删除 orphan：photo 已物理删除。
	res = db.Exec(`
		DELETE FROM person_photos
		WHERE rowid IN (
			SELECT pp.rowid FROM person_photos pp
			WHERE NOT EXISTS (SELECT 1 FROM photos p WHERE p.id = pp.photo_id)
			LIMIT ?
		)`, batchSize)
	if res.Error != nil {
		return delta, res.Error
	}
	delta.OrphanDeleted = res.RowsAffected

	// 3. 补齐 missing：从有效 face 找缺失关联，分批插入（ON CONFLICT 去重）。
	type missRow struct {
		PersonID uint       `gorm:"column:person_id"`
		PhotoID  uint       `gorm:"column:photo_id"`
		TakenAt  *time.Time `gorm:"column:taken_at"`
	}
	var missing []missRow
	if err := db.Raw(`
		SELECT f.person_id AS person_id, f.photo_id AS photo_id, p.taken_at AS taken_at
		FROM faces f
		JOIN photos p ON p.id = f.photo_id
		WHERE f.person_id IS NOT NULL AND f.person_id != 0 AND f.cluster_status != ?
		  AND NOT EXISTS (
			SELECT 1 FROM person_photos pp
			WHERE pp.person_id = f.person_id AND pp.photo_id = f.photo_id
		  )
		LIMIT ?`, model.FaceClusterStatusExcluded, batchSize).Scan(&missing).Error; err != nil {
		return delta, err
	}
	if len(missing) > 0 {
		const chunkSize = 200 // 3 列 × 200 = 600 占位符，安全
		if err := db.Transaction(func(tx2 *gorm.DB) error {
			for start := 0; start < len(missing); start += chunkSize {
				end := start + chunkSize
				if end > len(missing) {
					end = len(missing)
				}
				chunk := missing[start:end]
				placeholders := make([]string, len(chunk))
				args := make([]interface{}, 0, len(chunk)*3)
				for i, m := range chunk {
					placeholders[i] = "(?, ?, ?)"
					args = append(args, m.PersonID, m.PhotoID, m.TakenAt)
				}
				sql := "INSERT INTO person_photos(person_id, photo_id, taken_at) VALUES " +
					strings.Join(placeholders, ", ") +
					" ON CONFLICT(person_id, photo_id) DO NOTHING"
				if err := tx2.Exec(sql, args...).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return delta, err
		}
		delta.MissingInserted = int64(len(missing))
	}

	// 4. 同步 taken_at 不一致（分批）。
	res = db.Exec(`
		UPDATE person_photos
		SET taken_at = (SELECT p.taken_at FROM photos p WHERE p.id = person_photos.photo_id)
		WHERE rowid IN (
			SELECT pp.rowid FROM person_photos pp
			JOIN photos p ON p.id = pp.photo_id
			WHERE (pp.taken_at IS NULL) != (p.taken_at IS NULL)
			   OR (pp.taken_at IS NOT NULL AND p.taken_at IS NOT NULL
			       AND pp.taken_at != p.taken_at)
			LIMIT ?
		)`, batchSize)
	if res.Error != nil {
		return delta, res.Error
	}
	delta.TakenAtFixed = res.RowsAffected

	// 报告剩余量（受 batchSize 限制可能未一次清完），供调用方决定是否继续下一轮。
	rep, err := r.RunConsistencyCheck(db)
	if err != nil {
		return delta, err
	}
	delta.RemainingMissing = rep.MissingAssociations
	delta.RemainingExtra = rep.ExtraAssociations
	delta.RemainingOrphan = rep.OrphanPhotos
	delta.RemainingTakenAt = rep.TakenAtMismatches
	return delta, nil
}

// SetMigrationStatusV2 写入 v2 修复状态与进度（与 v1 分键，互不干扰）。
func (r *personPhotoRepository) SetMigrationStatusV2(tx *gorm.DB, status string, lastFaceID uint) error {
	db := tx
	if db == nil {
		db = r.db
	}
	if err := upsertAppConfig(db, personPhotoMigrationKeyV2, status); err != nil {
		return err
	}
	return upsertAppConfig(db, personPhotoLastFaceIDKeyV2, uintToStr(lastFaceID))
}

// GetMigrationStatusV2 读取 v2 修复状态与进度。
func (r *personPhotoRepository) GetMigrationStatusV2(tx *gorm.DB) (string, uint, error) {
	status, _, err := r.getMigrationStatusKey(tx, personPhotoMigrationKeyV2)
	if err != nil {
		return "", 0, err
	}
	_, lastFaceID, err := r.getMigrationStatusKey(tx, personPhotoLastFaceIDKeyV2)
	if err != nil {
		return status, 0, err
	}
	return status, lastFaceID, nil
}

// getMigrationStatusKey 读取单个 app_config key（status 文本 + 解析为 uint 的 lastFaceID）。
func (r *personPhotoRepository) getMigrationStatusKey(tx *gorm.DB, key string) (string, uint, error) {
	db := tx
	if db == nil {
		db = r.db
	}
	var val *string
	if err := db.Raw(`SELECT value FROM app_config WHERE key = ? LIMIT 1`, key).Scan(&val).Error; err != nil {
		return "", 0, err
	}
	s := ""
	if val != nil {
		s = *val
	}
	var n uint
	if val != nil {
		var v uint64
		for _, c := range *val {
			if c < '0' || c > '9' {
				continue
			}
			v = v*10 + uint64(c-'0')
		}
		n = uint(v)
	}
	return s, n, nil
}

// ListPhotoIDsByPersonCursor 从 person_photos 按 taken_at DESC, photo_id DESC 读取一页 photo_id。
// 取 limit+1 判定 hasMore。cursor 为 nil 表示首页。NULL taken_at 排在非空之后（SQLite DESC 默认）。
func (r *personPhotoRepository) ListPhotoIDsByPersonCursor(personID uint, cursor *PersonPhotoCursor, limit int) ([]uint, bool, *PersonPhotoCursor, error) {
	q := r.db.Table("person_photos").
		Select("photo_id, taken_at").
		Where("person_id = ?", personID).
		Order("taken_at DESC, photo_id DESC")

	if cursor != nil {
		if cursor.TakenAt != nil {
			// 非空区：(taken_at < cursor) OR (taken_at = cursor AND photo_id < cursor) OR taken_at IS NULL.
			// 整组显式加括号，避免与 person_id 过滤叠加时 OR/AND 优先级歧义导致 cursor 不推进。
			q = q.Where(
				"(taken_at < ? OR (taken_at = ? AND photo_id < ?) OR taken_at IS NULL)",
				*cursor.TakenAt, *cursor.TakenAt, cursor.ID,
			)
		} else {
			// NULL 区：仅 taken_at IS NULL 且 photo_id < cursor
			q = q.Where("taken_at IS NULL AND photo_id < ?", cursor.ID)
		}
	}

	type row struct {
		PhotoID uint       `gorm:"column:photo_id"`
		TakenAt *time.Time `gorm:"column:taken_at"`
	}
	var rows []row
	if err := q.Limit(limit + 1).Scan(&rows).Error; err != nil {
		return nil, false, nil, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.PhotoID)
	}

	var nextCursor *PersonPhotoCursor
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = &PersonPhotoCursor{
			TakenAt: last.TakenAt,
			ID:      last.PhotoID,
		}
	}

	return ids, hasMore, nextCursor, nil
}
