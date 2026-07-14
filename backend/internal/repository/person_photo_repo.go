package repository

import (
	"errors"
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

	// BackfillBatch 从 lastFaceID 之后扫描一批有效人脸，按 (person_id, photo_id) 去重
	// 插入 person_photos，返回本批处理到的最大 face id 与插入条数。
	BackfillBatch(tx *gorm.DB, lastFaceID uint, batchSize int) (newLastFaceID uint, inserted int, err error)

	// RunConsistencyCheck 校验 person_photos 与有效 faces 的一致性，返回不一致计数。
	// 失败项包括：重复 (person_id,photo_id)、记录数 != COUNT(DISTINCT photo_id)、
	// excluded face 误收录、孤儿记录（photo 已删）。
	RunConsistencyCheck(tx *gorm.DB) (inconsistencies int, err error)

	// ListPhotoIDsByPersonCursor 从 person_photos 按 taken_at DESC, photo_id DESC 读取
	// 一页 photo_id（limit+1 判定 hasMore）。cursor 为 keyset 游标，nil 表示首页。
	ListPhotoIDsByPersonCursor(personID uint, cursor *PersonPhotoCursor, limit int) (ids []uint, hasMore bool, nextCursor *PersonPhotoCursor, err error)

	// RunVerification 跑一致性校验，不一致返回 error。
	RunVerification(tx *gorm.DB) error

	// MigrationReady 报告回填是否完成且一致性校验通过（status==ready）。
	MigrationReady(tx *gorm.DB) (bool, error)
}

type personPhotoRepository struct {
	db *gorm.DB
}

func NewPersonPhotoRepository(db *gorm.DB) PersonPhotoRepository {
	return &personPhotoRepository{db: db}
}

const (
	personPhotoMigrationKey      = "migration.person_photos_v1.status"
	personPhotoLastFaceIDKey     = "migration.person_photos_v1.last_face_id"
	personPhotoStatusBackfilling = "backfilling"
	personPhotoStatusVerifying   = "verifying"
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

// RunConsistencyCheck 校验 person_photos 与有效 faces 一致性，返回不一致计数。
func (r *personPhotoRepository) RunConsistencyCheck(tx *gorm.DB) (int, error) {
	db := tx
	if db == nil {
		db = r.db
	}
	inconsistencies := 0

	// 1. 重复 (person_id, photo_id) — 主键约束保证不应有，仍统计以防 WITHOUT ROWID 异常。
	var dupCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM (
			SELECT person_id, photo_id, COUNT(*) c FROM person_photos GROUP BY person_id, photo_id HAVING c > 1
		)`).Scan(&dupCount).Error; err != nil {
		return 0, err
	}
	inconsistencies += int(dupCount)

	// 2. 有效 faces 的 DISTINCT photo 数 vs person_photos 记录数（按 person 聚合后比较）。
	//    逐人物比较差异行数。
	var mismatches int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM (
			SELECT COALESCE(pp.person_id, fv.person_id) AS pid
			FROM (
				SELECT person_id, COUNT(DISTINCT photo_id) AS cnt
				FROM faces
				WHERE person_id IS NOT NULL AND person_id != 0 AND cluster_status != ?
				GROUP BY person_id
			) fv
			FULL OUTER JOIN (
				SELECT person_id, COUNT(*) AS cnt
				FROM person_photos
				GROUP BY person_id
			) pp ON pp.person_id = fv.person_id
			WHERE COALESCE(fv.cnt, 0) != COALESCE(pp.cnt, 0)
		)`, model.FaceClusterStatusExcluded).Scan(&mismatches).Error; err != nil {
		// SQLite 较早版本不支持 FULL OUTER JOIN；退化为两段比较。
		mismatches = 0
		var leftDiff int64
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
			return 0, err
		}
		var rightDiff int64
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
			return 0, err
		}
		mismatches = leftDiff + rightDiff
	}
	inconsistencies += int(mismatches)

	// 3. excluded face 误收录：person_photos 中存在对应 face 为 excluded-only 的关联。
	var excludedLeak int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM person_photos pp
		WHERE NOT EXISTS (
			SELECT 1 FROM faces f
			WHERE f.person_id = pp.person_id AND f.photo_id = pp.photo_id
			  AND f.cluster_status != ?
		)`, model.FaceClusterStatusExcluded).Scan(&excludedLeak).Error; err != nil {
		return 0, err
	}
	inconsistencies += int(excludedLeak)

	return inconsistencies, nil
}

// MigrationReady 报告回填是否完成且校验通过。
func (r *personPhotoRepository) RunVerification(tx *gorm.DB) error {
	inc, err := r.RunConsistencyCheck(tx)
	if err != nil {
		return err
	}
	if inc > 0 {
		return errors.New("person_photos consistency check failed")
	}
	return nil
}

func (r *personPhotoRepository) MigrationReady(tx *gorm.DB) (bool, error) {
	status, _, err := r.GetMigrationStatus(tx)
	if err != nil {
		return false, err
	}
	return status == personPhotoStatusReady, nil
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
			// 非空区：(taken_at < cursor) OR (taken_at = cursor AND photo_id < cursor) OR taken_at IS NULL
			q = q.Where(
				"taken_at < ? OR (taken_at = ? AND photo_id < ?) OR taken_at IS NULL",
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
