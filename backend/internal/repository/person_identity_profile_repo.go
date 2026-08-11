package repository

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrPersonNotFound 由 ReplaceGeneration 在目标人物不存在时返回，
// 供上层服务区分"人物中途被删除"（应清理派生画像、不计为系统失败）与其他写入错误。
var ErrPersonNotFound = errors.New("person not found")

// IdentityProfileInvalidationReason 是身份画像失效的稳定原因标识。
// 不接受任意业务文本作为 reason，避免脏数据落库与隐私泄漏。
type IdentityProfileInvalidationReason string

const (
	InvalidationReasonDetectionReplaced IdentityProfileInvalidationReason = "detection_replaced_faces"
	InvalidationReasonPeopleMerged      IdentityProfileInvalidationReason = "people_merged"
	InvalidationReasonPersonSplit       IdentityProfileInvalidationReason = "person_split"
	InvalidationReasonFacesMoved        IdentityProfileInvalidationReason = "faces_moved"
	InvalidationReasonPersonDissolved   IdentityProfileInvalidationReason = "person_dissolved"
	InvalidationReasonClusteringAssign  IdentityProfileInvalidationReason = "clustering_assignment"
	InvalidationReasonReclusterAssign   IdentityProfileInvalidationReason = "recluster_assignment"
	InvalidationReasonRescueAttach      IdentityProfileInvalidationReason = "rescue_attach"
	InvalidationReasonResetAllPeople    IdentityProfileInvalidationReason = "reset_all_people"
)

// invalidationReasons 是允许落库 dirty_reason 的稳定原因白名单。
var invalidationReasons = map[IdentityProfileInvalidationReason]struct{}{
	InvalidationReasonDetectionReplaced: {},
	InvalidationReasonPeopleMerged:      {},
	InvalidationReasonPersonSplit:       {},
	InvalidationReasonFacesMoved:        {},
	InvalidationReasonPersonDissolved:   {},
	InvalidationReasonClusteringAssign:  {},
	InvalidationReasonReclusterAssign:   {},
	InvalidationReasonRescueAttach:      {},
	InvalidationReasonResetAllPeople:    {},
}

// dirtyReasonMaxLen 限制写入 dirty_reason 列的长度，与 model 中 varchar(50) 对齐留余量。
const dirtyReasonMaxLen = 50

// IdentityProfileInvalidationRequest 描述一次身份画像统一失效请求。
//
// 与 service 层的 IdentityProfileInvalidation 一一对应，但定义在 repository 包以避免
// service → repository 的类型依赖倒置。dirty 与 deleted 同时存在某人物时以 deleted 为准；
// ResetAll=true 时忽略 ID 列表，清空全部派生画像。
type IdentityProfileInvalidationRequest struct {
	DirtyPersonIDs   []uint
	DeletedPersonIDs []uint
	ResetAll         bool
	Reason           IdentityProfileInvalidationReason
}

// PersonIdentityProfileRepository 持久化人物身份画像的派生数据：profile 元数据、
// 各 generation 的 centers 与 members。所有写操作以 SQLite 事务保证原子性；
// ReplaceGeneration 在事务内写入新 generation 的 center/member 后才原子切换 active_generation，
// 任何失败都回滚，旧 generation 保持激活。画像是派生数据，legacy 指派（faces.person_id）
// 始终是真相来源，画像可安全丢弃重建。
type PersonIdentityProfileRepository interface {
	// MarkDirty 将指定人物标记为待重建。去重 ID 并忽略 0；已有 profile 仅更新状态与原因，
	// 不改动 active_generation，也不删除既有中心/成员；不存在则创建 active_generation=0 的 profile。
	MarkDirty(personIDs []uint, reason string) error
	// ListDirty 返回 status=dirty 且 person_id > cursor 的 profile，按 person_id ASC，
	// 严格应用 limit，游标分页避免全量加载。
	ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error)
	// ListDirtyByReasons 返回 status=dirty、person_id > cursor 且 dirty_reason IN reasons
	// 的 profile，按 person_id ASC，严格应用 limit。reasons 为空时不按 reason 过滤
	// （等价于 ListDirty）。用于协调器高/低优先级分批拉取，避免全量加载后内存排序。
	ListDirtyByReasons(reasons []string, cursor uint, limit int) ([]*model.PersonIdentityProfile, error)
	// GetActive 加载人物活动 generation 的完整构建（profile + centers + members）。
	// profile 不存在或 active_generation=0 时返回 (nil, nil)。
	GetActive(personID uint) (*model.PersonIdentityProfileBuild, error)
	// ListAllActiveCenters 通过 JOIN 一次查询返回所有人物活动 generation 的中心，
	// 按 person_id ASC, ordinal ASC，不返回历史中心。仅返回满足以下全部条件的中心，
	// 使完整 snapshot 的输入天然满足 ANN 构建要求（数据库侧过滤，禁止全量加载后 Go 内过滤）：
	//   - center.generation = profile.active_generation 且 active_generation > 0；
	//   - profile.embedding_model 等于 embeddingModel（当前服务模型签名）；
	//   - 对应人物仍存在于 people 表。
	ListAllActiveCenters(embeddingModel string) ([]*model.PersonIdentityCenter, error)
	// ListActiveCentersByPersonIDs 批量返回指定人物活动 generation 的中心，避免逐候选
	// N+1 查询。仅返回满足以下全部条件的中心（数据库侧 JOIN 过滤）：
	//   - center.generation = profile.active_generation 且 active_generation > 0；
	//   - profile.status = ready；
	//   - profile.embedding_model 等于 embeddingModel；
	//   - 对应人物仍存在于 people 表。
	// 输入去重并忽略 0；按 SQLite 参数上限分块；空输入直接返回空 map 不访问数据库。
	// 返回顺序为 person_id ASC, ordinal ASC, center_id ASC。
	ListActiveCentersByPersonIDs(personIDs []uint, embeddingModel string) (map[uint][]*model.PersonIdentityCenter, error)
	// ReplaceGeneration 在单事务内写入新 generation（centers + members）并原子激活。
	// build 中 accepted member 的 CenterID 为所属 center 的 Ordinal（逻辑引用），
	// 持久化 center 取得真实主键后重映射为真实 ID。任何步骤失败整体回滚。
	ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error
	// MarkFailed 标记构建失败并记录原因，保留活动 generation、中心与成员。
	MarkFailed(personID uint, message string) error
	// DeleteByPersonIDs 按人物 ID 删除画像派生数据（members → centers → profiles），
	// 不触碰 faces / people。
	DeleteByPersonIDs(personIDs []uint) error
	// ApplyInvalidation 在单个短事务内原子应用一次身份画像失效：
	// 先删除 DeletedPersonIDs 的 member/center/profile，再把剩余 DirtyPersonIDs 标记 dirty
	// （保留其 active generation 数据供诊断）；ResetAll=true 时忽略 ID 列表，按
	// members → centers → profiles 顺序清空全部派生画像。空请求零 SQL。
	// dirty/deleted ID 去重、过滤 0、升序，且 dirty 与 deleted 同时存在时以 deleted 为准。
	// 不修改 legacy faces.person_id。
	ApplyInvalidation(req IdentityProfileInvalidationRequest) error
	// InvalidateDeletedPeople 清理 people 表中已不存在人物的画像派生数据，
	// 使用子查询定位，不做全量内存扫描。
	InvalidateDeletedPeople() error
	// DeleteInactiveGenerations 保留最近 keep 个非活动 generation，删除其余非活动 generation。
	// 永远不删除当前活动 generation；keep=0 删除全部非活动 generation。
	DeleteInactiveGenerations(personID uint, keep int) error
	// ListBackfillPersonIDs 返回 id > cursor 且尚无任何 profile 记录的人物 ID，按 id ASC，
	// 严格应用 limit。用于 backfill 一次性扫描：已存在 profile（哪怕 failed）的人物不再纳入。
	ListBackfillPersonIDs(cursor uint, limit int) ([]uint, error)
	// GetStats 汇总 profile 各 status 计数与人物总数，不做全量加载。
	GetStats() (*model.PersonIdentityProfileStats, error)
}

type personIdentityProfileRepository struct {
	db *gorm.DB
}

// NewPersonIdentityProfileRepository constructs the repository.
func NewPersonIdentityProfileRepository(db *gorm.DB) PersonIdentityProfileRepository {
	return &personIdentityProfileRepository{db: db}
}

// dedupIDs 去重并忽略 0，保持首次出现的顺序。
func dedupIDs(ids []uint) []uint {
	seen := make(map[uint]struct{})
	var out []uint
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

func (r *personIdentityProfileRepository) MarkDirty(personIDs []uint, reason string) error {
	ids := dedupIDs(personIDs)
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, chunk := range chunkIDs(ids) {
		rows := make([]*model.PersonIdentityProfile, 0, len(chunk))
		for _, pid := range chunk {
			rows = append(rows, &model.PersonIdentityProfile{
				PersonID:    pid,
				Status:      model.PersonIdentityProfileStatusDirty,
				DirtyReason: reason,
				UpdatedAt:   now,
			})
		}
		// Upsert on person_id: 仅更新 status/reason/updated_at，绝不触碰 active/next generation。
		if err := r.db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "person_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"status":       model.PersonIdentityProfileStatusDirty,
				"dirty_reason": reason,
				"updated_at":   now,
			}),
		}).Create(rows).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *personIdentityProfileRepository) ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error) {
	var profiles []*model.PersonIdentityProfile
	q := r.db.Where("status = ? AND person_id > ?", model.PersonIdentityProfileStatusDirty, cursor).
		Where("person_id NOT IN (SELECT id FROM people WHERE hidden = ?)", true).
		Order("person_id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&profiles).Error; err != nil {
		return nil, err
	}
	return profiles, nil
}

// ListDirtyByReasons 返回 status=dirty、person_id > cursor 且 dirty_reason IN reasons 的 profile。
// reasons 为空时不按 reason 过滤（等价于 ListDirty），避免空 IN 子句语义歧义。
// 按 person_id ASC，严格应用 limit，游标分页避免全量加载。
func (r *personIdentityProfileRepository) ListDirtyByReasons(reasons []string, cursor uint, limit int) ([]*model.PersonIdentityProfile, error) {
	var profiles []*model.PersonIdentityProfile
	q := r.db.Where("status = ? AND person_id > ?", model.PersonIdentityProfileStatusDirty, cursor)
	q = q.Where("person_id NOT IN (SELECT id FROM people WHERE hidden = ?)", true)
	if len(reasons) > 0 {
		q = q.Where("dirty_reason IN ?", reasons)
	}
	q = q.Order("person_id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&profiles).Error; err != nil {
		return nil, err
	}
	return profiles, nil
}

func (r *personIdentityProfileRepository) GetActive(personID uint) (*model.PersonIdentityProfileBuild, error) {
	var profile model.PersonIdentityProfile
	err := r.db.Where("person_id = ?", personID).First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if profile.ActiveGeneration == 0 {
		// profile 存在但尚无激活的 generation。
		return nil, nil
	}
	gen := profile.ActiveGeneration

	var centers []*model.PersonIdentityCenter
	if err := r.db.Where("person_id = ? AND generation = ?", personID, gen).
		Order("ordinal ASC, id ASC").Find(&centers).Error; err != nil {
		return nil, err
	}
	var members []*model.PersonIdentityCenterMember
	if err := r.db.Where("person_id = ? AND generation = ?", personID, gen).
		Order("face_id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	return &model.PersonIdentityProfileBuild{
		Profile: &profile,
		Centers: centers,
		Members: members,
	}, nil
}

func (r *personIdentityProfileRepository) ListAllActiveCenters(embeddingModel string) ([]*model.PersonIdentityCenter, error) {
	var centers []*model.PersonIdentityCenter
	// JOIN 一次查询：center generation 必须等于 profile active generation 且存在活动 generation；
	// profile.embedding_model 必须等于当前服务模型签名；对应人物仍存在于 people 表。
	// 全部条件在数据库侧过滤，避免全量加载后在 Go 内过滤。
	q := r.db.Table("person_identity_centers AS c").
		Select("c.*").
		Joins("INNER JOIN person_identity_profiles AS p ON p.person_id = c.person_id").
		Joins("INNER JOIN people ON people.id = c.person_id").
		Where("c.generation = p.active_generation AND p.active_generation > 0").
		Where("p.embedding_model = ?", embeddingModel).
		Where("people.hidden = ?", false).
		Order("c.person_id ASC, c.ordinal ASC")
	if err := q.Find(&centers).Error; err != nil {
		return nil, err
	}
	return centers, nil
}

// ListActiveCentersByPersonIDs 批量返回指定人物活动 generation 的中心，用于 matcher
// 在 ANN 召回后一次性加载所有候选中心做精确评分，避免逐候选调用 GetActive 产生 N+1。
//
// 过滤条件与 ListAllActiveCenters 一致，额外要求 profile.status = ready 并按 person ID
// 集合限定。输入去重、忽略 0、按 SQLite 参数上限分块；每个分块一次批量查询。
// 返回 map[personID][]centers，中心按 person_id ASC, ordinal ASC, id ASC 排序。
// 空输入直接返回空 map，不访问数据库。
func (r *personIdentityProfileRepository) ListActiveCentersByPersonIDs(personIDs []uint, embeddingModel string) (map[uint][]*model.PersonIdentityCenter, error) {
	out := make(map[uint][]*model.PersonIdentityCenter)
	ids := dedupIDs(personIDs)
	if len(ids) == 0 {
		return out, nil
	}
	for _, chunk := range chunkIDs(ids) {
		var centers []*model.PersonIdentityCenter
		q := r.db.Table("person_identity_centers AS c").
			Select("c.*").
			Joins("INNER JOIN person_identity_profiles AS p ON p.person_id = c.person_id").
			Joins("INNER JOIN people ON people.id = c.person_id").
			Where("c.generation = p.active_generation AND p.active_generation > 0").
			Where("p.status = ?", model.PersonIdentityProfileStatusReady).
			Where("p.embedding_model = ?", embeddingModel).
			Where("people.hidden = ?", false).
			Where("c.person_id IN ?", chunk).
			Order("c.person_id ASC, c.ordinal ASC, c.id ASC")
		if err := q.Find(&centers).Error; err != nil {
			return nil, err
		}
		for _, c := range centers {
			out[c.PersonID] = append(out[c.PersonID], c)
		}
	}
	return out, nil
}

// ReplaceGeneration 在单个 SQLite 事务内完成新 generation 的写入与激活。
// 失败时整体回滚：旧 active_generation 不变，旧中心/成员仍可读取，不残留半成品 generation。
func (r *personIdentityProfileRepository) ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error {
	if build == nil || build.Profile == nil {
		return errors.New("identity profile build is nil")
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		// 1. 验证目标 Person 存在。
		var person model.Person
		if err := tx.Where("id = ?", personID).First(&person).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("person %d not found: %w", personID, ErrPersonNotFound)
			}
			return err
		}

		// 2. 锁定/读取当前 profile 状态；不存在则创建空 dirty profile。
		var profile model.PersonIdentityProfile
		err := tx.Where("person_id = ?", personID).First(&profile).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			profile = model.PersonIdentityProfile{
				PersonID: personID,
				Status:   model.PersonIdentityProfileStatusDirty,
			}
			if err := tx.Create(&profile).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		// 3. 分配严格大于活动 generation 的新 generation。next_generation 记录高水位，
		//    仅在成功时推进，故失败重试不会跳号。
		newGen := profile.NextGeneration
		if newGen <= profile.ActiveGeneration {
			newGen = profile.ActiveGeneration + 1
			if newGen == 0 {
				newGen = 1
			}
		}

		// 4. 写入新 centers，建立 ordinal → 真实 ID 映射。
		ordinalToID := make(map[int]uint, len(build.Centers))
		for _, c := range build.Centers {
			c.ID = 0
			c.PersonID = personID
			c.Generation = newGen
			if err := tx.Create(c).Error; err != nil {
				return err
			}
			ordinalToID[c.Ordinal] = c.ID
		}

		// 5. 写入 members，将 CenterID 从 ordinal 重映射为真实主键。
		//    6. 校验：所有 center/member 归属同一人物与 generation（由强制赋值保证），
		//    且 accepted member 引用的 center ordinal 必须存在；重复 face_id 会触发
		//    (person_id, generation, face_id) 唯一约束冲突，使事务回滚。
		for _, m := range build.Members {
			m.ID = 0
			m.PersonID = personID
			m.Generation = newGen
			if m.CenterID != nil {
				realID, ok := ordinalToID[int(*m.CenterID)]
				if !ok {
					return fmt.Errorf("member face %d references unknown center ordinal %d", m.FaceID, *m.CenterID)
				}
				m.CenterID = &realID
			}
			if err := tx.Create(m).Error; err != nil {
				return err
			}
		}

		// 7-11. 最后才激活：推进 active_generation、置 ready、清空 dirty_reason/last_error、
		//       更新 face-count 快照与算法/embedding 版本、推进 next_generation、记录 built_at。
		//       在此之前任何失败（含唯一约束冲突）都会回滚，旧 generation 保持激活。
		now := time.Now().UTC()
		if err := tx.Model(&profile).Updates(map[string]interface{}{
			"active_generation":   newGen,
			"next_generation":     newGen + 1,
			"status":              model.PersonIdentityProfileStatusReady,
			"dirty_reason":        "",
			"last_error":          "",
			"face_count_snapshot": len(build.Members),
			"algorithm_version":   build.Profile.AlgorithmVersion,
			"embedding_model":     build.Profile.EmbeddingModel,
			"built_at":            &now,
			"updated_at":          now,
		}).Error; err != nil {
			return err
		}
		return nil
	})
}

func (r *personIdentityProfileRepository) MarkFailed(personID uint, message string) error {
	now := time.Now().UTC()
	return r.db.Model(&model.PersonIdentityProfile{}).
		Where("person_id = ?", personID).
		Updates(map[string]interface{}{
			"status":     model.PersonIdentityProfileStatusFailed,
			"last_error": message,
			"updated_at": now,
		}).Error
}

func (r *personIdentityProfileRepository) DeleteByPersonIDs(personIDs []uint) error {
	ids := dedupIDs(personIDs)
	if len(ids) == 0 {
		return nil
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, chunk := range chunkIDs(ids) {
			// 安全顺序：members → centers → profiles；不触碰 faces / people。
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
				return err
			}
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityCenter{}).Error; err != nil {
				return err
			}
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityProfile{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *personIdentityProfileRepository) InvalidateDeletedPeople() error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 子查询定位 people 表中已不存在人物的派生数据，不做全量内存扫描。
		orphan := "person_id NOT IN (SELECT id FROM people)"
		if err := tx.Where(orphan).Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
			return err
		}
		if err := tx.Where(orphan).Delete(&model.PersonIdentityCenter{}).Error; err != nil {
			return err
		}
		return tx.Where(orphan).Delete(&model.PersonIdentityProfile{}).Error
	})
}

// NormalizeInvalidationRequest 清洗失效请求：去重、过滤 0、升序；deleted 优先于 dirty；
// 校验 reason 白名单并截断长度；ResetAll 时清空 ID 列表。返回清洗后的请求与是否需要执行。
// 任何空请求（无 dirty/deleted 且非 ResetAll）返回 needApply=false，调用方据此零 SQL。
// 导出供 service 层复用同一清洗语义，避免逻辑重复。
func NormalizeInvalidationRequest(req IdentityProfileInvalidationRequest) (IdentityProfileInvalidationRequest, bool) {
	return normalizeInvalidationReq(req)
}

// normalizeInvalidationReq 清洗失效请求：去重、过滤 0、升序；deleted 优先于 dirty；
// 校验 reason 白名单并截断长度；ResetAll 时清空 ID 列表。返回清洗后的请求与是否需要执行。
// 任何空请求（无 dirty/deleted 且非 ResetAll）返回 needApply=false，调用方据此零 SQL。
func normalizeInvalidationReq(req IdentityProfileInvalidationRequest) (IdentityProfileInvalidationRequest, bool) {
	if req.ResetAll {
		// ResetAll 忽略 ID 列表；reason 取白名单兜底（reset_all_people）。
		reason := req.Reason
		if _, ok := invalidationReasons[reason]; !ok {
			reason = InvalidationReasonResetAllPeople
		}
		return IdentityProfileInvalidationRequest{ResetAll: true, Reason: reason}, true
	}

	deletedSet := make(map[uint]struct{}, len(req.DeletedPersonIDs))
	for _, id := range req.DeletedPersonIDs {
		if id == 0 {
			continue
		}
		deletedSet[id] = struct{}{}
	}

	dirtySet := make(map[uint]struct{}, len(req.DirtyPersonIDs))
	for _, id := range req.DirtyPersonIDs {
		if id == 0 {
			continue
		}
		if _, deleted := deletedSet[id]; deleted {
			continue // deleted 优先
		}
		dirtySet[id] = struct{}{}
	}

	dirty := make([]uint, 0, len(dirtySet))
	for id := range dirtySet {
		dirty = append(dirty, id)
	}
	deleted := make([]uint, 0, len(deletedSet))
	for id := range deletedSet {
		deleted = append(deleted, id)
	}
	sortUintSlice(dirty)
	sortUintSlice(deleted)

	if len(dirty) == 0 && len(deleted) == 0 {
		return IdentityProfileInvalidationRequest{}, false
	}

	reason := req.Reason
	if _, ok := invalidationReasons[reason]; !ok {
		reason = ""
	}
	if len(reason) > dirtyReasonMaxLen {
		reason = reason[:dirtyReasonMaxLen]
	}

	return IdentityProfileInvalidationRequest{
		DirtyPersonIDs:   dirty,
		DeletedPersonIDs: deleted,
		Reason:           reason,
	}, true
}

// sortUintSlice 升序排序 uint 切片。
func sortUintSlice(ids []uint) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

// ApplyInvalidation 在单个短事务内原子应用一次身份画像失效。
//
// 删除顺序满足外键/语义约束：members → centers → profiles（deleted 人物）；
// dirty 人物仅更新 status/dirty_reason/updated_at，绝不触碰 active/next generation，
// 保留旧 generation 数据供回滚与诊断。ResetAll=true 时按相同顺序清空全部派生画像，
// 不将全部 Person ID 加载进内存，也不逐人物开启事务。
//
// 大 ID 集合按 SQLite 参数上限分块，分块在同一个事务内执行以保持原子性：任一步失败整体回滚。
// 空请求直接返回，零 SQL。
func (r *personIdentityProfileRepository) ApplyInvalidation(req IdentityProfileInvalidationRequest) error {
	normalized, needApply := normalizeInvalidationReq(req)
	if !needApply {
		return nil
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		if normalized.ResetAll {
			// 清空全部派生画像，顺序：members → centers → profiles。
			if err := tx.Where("1 = 1").Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
				return err
			}
			if err := tx.Where("1 = 1").Delete(&model.PersonIdentityCenter{}).Error; err != nil {
				return err
			}
			return tx.Where("1 = 1").Delete(&model.PersonIdentityProfile{}).Error
		}

		// 1. 删除 deleted 人物的 member。
		for _, chunk := range chunkIDs(normalized.DeletedPersonIDs) {
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
				return err
			}
		}
		// 2. 删除对应 center。
		for _, chunk := range chunkIDs(normalized.DeletedPersonIDs) {
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityCenter{}).Error; err != nil {
				return err
			}
		}
		// 3. 删除对应 profile。
		for _, chunk := range chunkIDs(normalized.DeletedPersonIDs) {
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityProfile{}).Error; err != nil {
				return err
			}
		}
		// 4. 将剩余 dirty 人物标记 dirty，保留 active generation 数据。
		if len(normalized.DirtyPersonIDs) > 0 {
			now := time.Now().UTC()
			reason := string(normalized.Reason)
			for _, chunk := range chunkIDs(normalized.DirtyPersonIDs) {
				rows := make([]*model.PersonIdentityProfile, 0, len(chunk))
				for _, pid := range chunk {
					rows = append(rows, &model.PersonIdentityProfile{
						PersonID:    pid,
						Status:      model.PersonIdentityProfileStatusDirty,
						DirtyReason: reason,
						UpdatedAt:   now,
					})
				}
				if err := tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "person_id"}},
					DoUpdates: clause.Assignments(map[string]interface{}{
						"status":       model.PersonIdentityProfileStatusDirty,
						"dirty_reason": reason,
						"updated_at":   now,
					}),
				}).Create(rows).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (r *personIdentityProfileRepository) DeleteInactiveGenerations(personID uint, keep int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var profile model.PersonIdentityProfile
		err := tx.Where("person_id = ?", personID).First(&profile).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		active := profile.ActiveGeneration

		// 收集非活动 generation，按 generation DESC 排序（最近在前）。
		var gens []int
		if err := tx.Model(&model.PersonIdentityCenter{}).
			Where("person_id = ? AND generation != ?", personID, active).
			Distinct("generation").
			Order("generation DESC").
			Pluck("generation", &gens).Error; err != nil {
			return err
		}

		// 保留最近 keep 个非活动 generation，删除其余；keep<=0 删除全部非活动 generation。
		// 活动 generation 已被 WHERE 排除，永远不会被删除。
		var toDelete []int
		for i, g := range gens {
			if keep > 0 && i < keep {
				continue
			}
			toDelete = append(toDelete, g)
		}
		if len(toDelete) == 0 {
			return nil
		}
		// 保持成员与中心一致删除。
		if err := tx.Where("person_id = ? AND generation IN ?", personID, toDelete).
			Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
			return err
		}
		return tx.Where("person_id = ? AND generation IN ?", personID, toDelete).
			Delete(&model.PersonIdentityCenter{}).Error
	})
}

// ListBackfillPersonIDs 返回尚无 profile 记录的人物 ID（id > cursor）。
// 使用 NOT IN 子查询定位，避免全量内存扫描；已存在 profile 的人物（含 failed）不再纳入 backfill。
func (r *personIdentityProfileRepository) ListBackfillPersonIDs(cursor uint, limit int) ([]uint, error) {
	if limit <= 0 {
		return nil, nil
	}
	var ids []uint
	err := r.db.Model(&model.Person{}).
		Where("id > ? AND id NOT IN (SELECT person_id FROM person_identity_profiles) AND hidden = ?", cursor, false).
		Order("id ASC").
		Limit(limit).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// GetStats 汇总 profile 各 status 计数、人物总数、活动 center/member 聚合，供 service 与监控查询。
// backfill 游标与完成标志由 service 从持久化状态补充，此处只返回数据库聚合值。
//
// 活动中心定义：center.generation = profile.active_generation、profile.status=ready、
// active_generation>0、对应 people 仍存在。活动 member 同理限定 member.generation =
// profile.active_generation。统计全部通过 COUNT/GROUP BY/MAX/AVG 在数据库侧完成，
// 不加载全部 profile/center/member 到 Go，也不读取 centroid_embedding/sum_embedding。
func (r *personIdentityProfileRepository) GetStats() (*model.PersonIdentityProfileStats, error) {
	stats := &model.PersonIdentityProfileStats{}

	rows, err := r.db.Model(&model.PersonIdentityProfile{}).
		Select("status, COUNT(*) as count").
		Group("status").
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats.Total += count
		switch status {
		case model.PersonIdentityProfileStatusDirty:
			stats.Dirty = count
		case model.PersonIdentityProfileStatusBuilding:
			stats.Building = count
		case model.PersonIdentityProfileStatusReady:
			stats.Ready = count
		case model.PersonIdentityProfileStatusFailed:
			stats.Failed = count
		}
	}

	var people int64
	if err := r.db.Model(&model.Person{}).Count(&people).Error; err != nil {
		return nil, err
	}
	stats.TotalPeople = people

	// 活动 center 聚合：JOIN profile + people，仅统计 generation = active_generation
	// 且 active_generation > 0 且 status=ready 且对应人物存在。
	// total/active 在该子集下相等（都是活动中心）。
	type centerAgg struct {
		Total     int64
		Confirmed int64
	}
	var ca centerAgg
	if err := r.db.Table("person_identity_centers AS c").
		Select("COUNT(*) AS total, SUM(CASE WHEN c.confirmed = 1 THEN 1 ELSE 0 END) AS confirmed").
		Joins("INNER JOIN person_identity_profiles AS p ON p.person_id = c.person_id").
		Joins("INNER JOIN people ON people.id = c.person_id").
		Where("c.generation = p.active_generation AND p.active_generation > 0").
		Where("p.status = ?", model.PersonIdentityProfileStatusReady).
		Scan(&ca).Error; err != nil {
		return nil, err
	}
	stats.CenterTotal = ca.Total
	stats.CenterActive = ca.Total
	stats.CenterConfirmed = ca.Confirmed

	// 拥有活动中心且 ready 的人物数（avg 分母），以及单人物活动中心最大值。
	var activeProfiles int64
	if err := r.db.Table("person_identity_centers AS c").
		Select("COUNT(DISTINCT c.person_id)").
		Joins("INNER JOIN person_identity_profiles AS p ON p.person_id = c.person_id").
		Joins("INNER JOIN people ON people.id = c.person_id").
		Where("c.generation = p.active_generation AND p.active_generation > 0").
		Where("p.status = ?", model.PersonIdentityProfileStatusReady).
		Scan(&activeProfiles).Error; err != nil {
		return nil, err
	}
	stats.CenterActiveProfiles = activeProfiles

	var maxPer int64
	if err := r.db.Table("person_identity_centers AS c").
		Select("COUNT(*) AS cnt").
		Joins("INNER JOIN person_identity_profiles AS p ON p.person_id = c.person_id").
		Joins("INNER JOIN people ON people.id = c.person_id").
		Where("c.generation = p.active_generation AND p.active_generation > 0").
		Where("p.status = ?", model.PersonIdentityProfileStatusReady).
		Group("c.person_id").
		Order("cnt DESC").
		Limit(1).
		Scan(&maxPer).Error; err != nil {
		return nil, err
	}
	stats.CenterMaxPerProfile = int(maxPer)

	if activeProfiles > 0 {
		stats.CenterAvgPerProfile = float64(ca.Total) / float64(activeProfiles)
	}

	// 活动 member 聚合：同样限定 generation = active_generation、ready、人物存在。
	type memberAgg struct {
		Total     int64
		Accepted  int64
		Candidate int64
		Excluded  int64
	}
	var ma memberAgg
	if err := r.db.Table("person_identity_center_members AS m").
		Select("COUNT(*) AS total, "+
			"SUM(CASE WHEN m.state = 'accepted' THEN 1 ELSE 0 END) AS accepted, "+
			"SUM(CASE WHEN m.state = 'candidate' THEN 1 ELSE 0 END) AS candidate, "+
			"SUM(CASE WHEN m.state = 'excluded' THEN 1 ELSE 0 END) AS excluded").
		Joins("INNER JOIN person_identity_profiles AS p ON p.person_id = m.person_id").
		Joins("INNER JOIN people ON people.id = m.person_id").
		Where("m.generation = p.active_generation AND p.active_generation > 0").
		Where("p.status = ?", model.PersonIdentityProfileStatusReady).
		Scan(&ma).Error; err != nil {
		return nil, err
	}
	stats.MemberTotal = ma.Total
	stats.MemberAccepted = ma.Accepted
	stats.MemberCandidate = ma.Candidate
	stats.MemberExcluded = ma.Excluded

	return stats, nil
}
