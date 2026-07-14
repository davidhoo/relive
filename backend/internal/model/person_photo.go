package model

import "time"

// PersonPhoto 是人物照片分页索引派生表。
//
// 设计目的：人物详情页照片 cursor 分页原本需要 JOIN faces → DISTINCT photo_id → ORDER BY taken_at，
// 对大人物（数千张照片）会扫描全部人脸、回表、临时排序，NAS 上 6–18 秒。本表把
// (person_id, photo_id, taken_at) 预先物化，使分页查询直接走 idx_person_photos_cursor
// 索引读取，不再 DISTINCT/ORDER BY 临时 B-Tree。
//
// 数据语义（由 SQLite trigger 维护，见 pkg/database）：
//   - 只收录 person_id IS NOT NULL 且 cluster_status != 'excluded' 的人脸关联；
//   - 同一 (person_id, photo_id) 只有一条记录（多张脸去重）；
//   - taken_at 随 photos.taken_at 同步更新。
//
// WITHOUT ROWID：主键 (person_id, photo_id) 即聚簇索引，避免额外 rowid。
type PersonPhoto struct {
	PersonID uint       `gorm:"primaryKey;column:person_id" json:"person_id"`
	PhotoID  uint       `gorm:"primaryKey;column:photo_id" json:"photo_id"`
	TakenAt  *time.Time `gorm:"column:taken_at" json:"taken_at,omitempty"`
}

func (PersonPhoto) TableName() string {
	return "person_photos"
}
