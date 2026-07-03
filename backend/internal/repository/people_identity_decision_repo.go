package repository

import (
	"sort"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// peopleIdentityDecisionListMax 是 ListRecent 的绝对上限，为 Task 14 的只读接口预留。
const peopleIdentityDecisionListMax = 200

// PeopleIdentityDecisionRepository 持久化身份画像 shadow/rescue 决策遥测。
//
// 所有方法都不读取或返回 embedding、图片路径、缩略图路径或人物名称。CreateIgnore 利用
// DecisionKey 唯一索引实现幂等写入，避免相同组件重试产生重复记录。清理通过 ListIDsBefore
// + DeleteByIDs 分批短事务完成，不执行无界 DELETE。
type PeopleIdentityDecisionRepository interface {
	// CreateIgnore 写入一条决策记录。相同 DecisionKey 唯一冲突时返回 created=false, err=nil，
	// 不先 SELECT 再 INSERT。其他数据库错误正常返回。
	CreateIgnore(decision *model.PeopleIdentityDecision) (created bool, err error)
	// ListRecent 按 created_at DESC, id DESC 返回最近 limit 条，limit 上限 200。
	// limit<=0 返回空结果。不加载任何 embedding 或路径字段。
	ListRecent(limit int) ([]*model.PeopleIdentityDecision, error)
	// ListIDsBefore 返回 created_at < cutoff 的 ID，按 created_at ASC, id ASC，最多 limit 条。
	// limit<=0 返回空结果。只返回 ID，不做 offset 分页。
	ListIDsBefore(cutoff time.Time, limit int) ([]uint, error)
	// DeleteByIDs 按 ID 物理删除一批记录。空输入返回 0。ID 去重、过滤 0、按 SQLite 参数上限分块。
	DeleteByIDs(ids []uint) (int64, error)
	// GetSummarySince 汇总 created_at >= since 的决策分布：SELECT decision, COUNT(*)
	// GROUP BY decision。利用 idx_pid_created 索引。未知 decision 计入 Total 但不写入已知分类。
	// 空表返回零值结构，不返回 nil。不加载完整 decision 行，不按 Person/Face ID 分组。
	GetSummarySince(since time.Time) (*model.IdentityDecisionSummary, error)
}

type peopleIdentityDecisionRepository struct {
	db *gorm.DB
}

// NewPeopleIdentityDecisionRepository constructs the repository.
func NewPeopleIdentityDecisionRepository(db *gorm.DB) PeopleIdentityDecisionRepository {
	return &peopleIdentityDecisionRepository{db: db}
}

// CreateIgnore 直接 INSERT，依赖 DecisionKey 唯一索引实现幂等。SQLite 唯一冲突返回
// created=false, err=nil；其他错误正常返回。禁止先 SELECT 再 INSERT。
func (r *peopleIdentityDecisionRepository) CreateIgnore(decision *model.PeopleIdentityDecision) (bool, error) {
	if decision == nil {
		return false, nil
	}
	err := r.db.Create(decision).Error
	if err != nil {
		if isUniqueConstraintError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// isUniqueConstraintError 识别 SQLite 唯一约束冲突。GORM 配置未启用 TranslateError，
// 因此 SQLite 驱动返回原始错误，消息包含 "UNIQUE constraint failed"。
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint")
}

// ListRecent 按 created_at DESC, id DESC 返回最近 limit 条。limit<=0 返回空结果；
// 超过 200 截断为 200。people_identity_decisions 表不含 embedding 或路径字段，
// 因此无需额外 Select 裁剪。
func (r *peopleIdentityDecisionRepository) ListRecent(limit int) ([]*model.PeopleIdentityDecision, error) {
	if limit <= 0 {
		return []*model.PeopleIdentityDecision{}, nil
	}
	if limit > peopleIdentityDecisionListMax {
		limit = peopleIdentityDecisionListMax
	}
	var out []*model.PeopleIdentityDecision
	err := r.db.Order("created_at DESC, id DESC").Limit(limit).Find(&out).Error
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.PeopleIdentityDecision{}
	}
	return out, nil
}

// ListIDsBefore 返回 created_at < cutoff 的 ID，按 created_at ASC, id ASC，最多 limit 条。
// 利用 idx_pid_created 索引。limit<=0 返回空结果。
func (r *peopleIdentityDecisionRepository) ListIDsBefore(cutoff time.Time, limit int) ([]uint, error) {
	if limit <= 0 {
		return nil, nil
	}
	var ids []uint
	err := r.db.Model(&model.PeopleIdentityDecision{}).
		Where("created_at < ?", cutoff).
		Order("created_at ASC, id ASC").
		Limit(limit).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// DeleteByIDs 按 ID 物理删除一批记录。空输入返回 0。ID 去重、过滤 0 后按 SQLite 参数上限
// 分块，每块独立 DELETE，累加删除行数。
func (r *peopleIdentityDecisionRepository) DeleteByIDs(ids []uint) (int64, error) {
	cleaned := dedupFilterNonZeroIDs(ids)
	if len(cleaned) == 0 {
		return 0, nil
	}
	var total int64
	for _, chunk := range chunkIDs(cleaned) {
		result := r.db.Where("id IN ?", chunk).Delete(&model.PeopleIdentityDecision{})
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
	}
	return total, nil
}

// GetSummarySince 汇总 created_at >= since 的决策分布。使用 SELECT decision, COUNT(*)
// GROUP BY decision，利用 idx_pid_created 索引按时间范围扫描。未知 decision 计入 Total
// 但不写入已知分类；空表返回零值结构。windowHours 由 service 注入（最近 24 小时）。
// 不加载完整 decision 行，不按 Person/Face ID 分组。
func (r *peopleIdentityDecisionRepository) GetSummarySince(since time.Time) (*model.IdentityDecisionSummary, error) {
	summary := &model.IdentityDecisionSummary{}

	type row struct {
		Decision string
		Count    int64
	}
	var rows []row
	if err := r.db.Model(&model.PeopleIdentityDecision{}).
		Select("decision, COUNT(*) as count").
		Where("created_at >= ?", since).
		Group("decision").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	for _, r := range rows {
		summary.Total += r.Count
		switch r.Decision {
		case model.PeopleIdentityDecisionAgree:
			summary.Agree = r.Count
		case model.PeopleIdentityDecisionDisagree:
			summary.Disagree = r.Count
		case model.PeopleIdentityDecisionLegacyMissProfileHit:
			summary.LegacyMissProfileHit = r.Count
		case model.PeopleIdentityDecisionLegacyMissProfileMiss:
			summary.LegacyMissProfileMiss = r.Count
		case model.PeopleIdentityDecisionProfileMiss:
			summary.ProfileMiss = r.Count
		case model.PeopleIdentityDecisionProfileUnavailable:
			summary.ProfileUnavailable = r.Count
		case model.PeopleIdentityDecisionProfileBlocked:
			summary.ProfileBlocked = r.Count
		case model.PeopleIdentityDecisionRescueApplied:
			summary.RescueApplied = r.Count
		}
	}
	return summary, nil
}
func dedupFilterNonZeroIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
