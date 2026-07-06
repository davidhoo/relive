package model

import "time"

const (
	PersonIdentityProfileStatusDirty    = "dirty"
	PersonIdentityProfileStatusBuilding = "building"
	PersonIdentityProfileStatusReady    = "ready"
	PersonIdentityProfileStatusFailed   = "failed"
)

const (
	PersonIdentityMemberStateAccepted  = "accepted"
	PersonIdentityMemberStateCandidate = "candidate"
	PersonIdentityMemberStateExcluded  = "excluded"
)

// PersonIdentityProfile 记录每个人物的身份画像元数据：当前激活 generation、
// 构建状态、算法/模型版本与人脸数快照。画像为派生数据，可安全丢弃重建，
// legacy 指派始终是真相来源。每次重建写入新 generation，校验通过后原子切换。
type PersonIdentityProfile struct {
	ID                uint       `gorm:"primarykey" json:"id"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `gorm:"index:idx_pip_status_updated,priority:2" json:"updated_at"`
	PersonID          uint       `gorm:"not null;uniqueIndex:idx_pip_person" json:"person_id"`
	ActiveGeneration  int        `gorm:"not null;default:0" json:"active_generation"`
	NextGeneration    int        `gorm:"not null;default:0" json:"next_generation"`
	Status            string     `gorm:"type:varchar(20);not null;default:dirty;index:idx_pip_status_updated,priority:1;check:chk_pip_status,status IN ('dirty','building','ready','failed')" json:"status"`
	DirtyReason       string     `gorm:"type:varchar(50)" json:"dirty_reason,omitempty"`
	AlgorithmVersion  string     `gorm:"type:varchar(50)" json:"algorithm_version,omitempty"`
	EmbeddingModel    string     `gorm:"type:varchar(100)" json:"embedding_model,omitempty"`
	FaceCountSnapshot int        `gorm:"not null;default:0" json:"face_count_snapshot"`
	LastError         string     `gorm:"type:text" json:"last_error,omitempty"`
	BuiltAt           *time.Time `json:"built_at,omitempty"`
}

func (PersonIdentityProfile) TableName() string {
	return "person_identity_profiles"
}

// PersonIdentityCenter 是某个画像 generation 下的身份子中心，承载一个稳定的人脸模式。
// CentroidEmbedding/SumEmbedding 以 BLOB 存储（小端 float32），禁止通过 JSON 输出。
type PersonIdentityCenter struct {
	ID                uint      `gorm:"primarykey" json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	PersonID          uint      `gorm:"not null;uniqueIndex:idx_pic_person_gen_ordinal,priority:1;index:idx_pic_person_generation,priority:1" json:"person_id"`
	Generation        int       `gorm:"not null;uniqueIndex:idx_pic_person_gen_ordinal,priority:2;index:idx_pic_person_generation,priority:2" json:"generation"`
	Ordinal           int       `gorm:"not null;uniqueIndex:idx_pic_person_gen_ordinal,priority:3" json:"ordinal"`
	CentroidEmbedding []byte    `gorm:"type:blob" json:"-"`
	SumEmbedding      []byte    `gorm:"type:blob" json:"-"`
	MedoidFaceID      *uint     `gorm:"index:idx_pic_medoid_face" json:"medoid_face_id,omitempty"`
	SupportCount      int       `gorm:"not null;default:0" json:"support_count"`
	TotalWeight       float64   `gorm:"not null;default:0" json:"total_weight"`
	SimilarityP10     float64   `gorm:"not null;default:0" json:"similarity_p10"`
	SimilarityP50     float64   `gorm:"not null;default:0" json:"similarity_p50"`
	Confirmed         bool      `gorm:"not null;default:false" json:"confirmed"`
}

func (PersonIdentityCenter) TableName() string {
	return "person_identity_centers"
}

// PersonIdentityCenterMember 记录人脸在特定画像 generation 中的角色与权重。
// center_id 可空，供 candidate/excluded（尚未归入某中心）的人脸使用。
// 唯一键为 (person_id, generation, face_id)，确保同一 generation 下每张人脸只归属一次。
type PersonIdentityCenterMember struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	PersonID   uint      `gorm:"not null;uniqueIndex:idx_picm_person_gen_face,priority:1" json:"person_id"`
	Generation int       `gorm:"not null;uniqueIndex:idx_picm_person_gen_face,priority:2" json:"generation"`
	CenterID   *uint     `gorm:"index:idx_picm_center" json:"center_id,omitempty"`
	FaceID     uint      `gorm:"not null;uniqueIndex:idx_picm_person_gen_face,priority:3;index:idx_picm_face" json:"face_id"`
	PhotoID    uint      `gorm:"not null;index:idx_picm_photo" json:"photo_id"`
	Similarity float64   `gorm:"not null;default:0" json:"similarity"`
	Weight     float64   `gorm:"not null;default:0" json:"weight"`
	State      string    `gorm:"type:varchar(20);not null;check:chk_picm_state,state IN ('accepted','candidate','excluded')" json:"state"`
}

func (PersonIdentityCenterMember) TableName() string {
	return "person_identity_center_members"
}

// PersonIdentityProfileBuild 是一次画像构建的完整结果：profile 元数据加上该 generation
// 下的 centers 与 members。由 builder（纯函数）产出，经 repository 原子写入并激活；
// GetActive 读取激活 generation 时也组装为该结构。
//
// 中心归属约定：builder 为每个 accepted member 设置 CenterID 指向其所属 center 的
// Ordinal（逻辑引用，非真实主键），candidate/excluded 的 CenterID 为 nil。
// repository 在持久化 center 取得真实 ID 后，将 member.CenterID 从 Ordinal 重映射为真实主键。
type PersonIdentityProfileBuild struct {
	Profile *PersonIdentityProfile
	Centers []*PersonIdentityCenter
	Members []*PersonIdentityCenterMember
}

// PersonIdentityProfileStats 汇总身份画像的运行状态：各 status 的 profile 计数、
// 人物总数与 backfill 进度。由 repository 提供原始计数，service 补充 backfill 游标。
// legacy 模式下 service 返回零值结构而不查询数据库。
//
// Task 14 起新增 center/member 活动聚合统计字段，供只读运行状态接口使用。
// repository 通过 COUNT/GROUP BY/MAX/AVG 聚合查询填充，不加载全部行到 Go。
type PersonIdentityProfileStats struct {
	Total             int64 `json:"total"`
	Dirty             int64 `json:"dirty"`
	Building          int64 `json:"building"`
	Ready             int64 `json:"ready"`
	Failed            int64 `json:"failed"`
	TotalPeople       int64 `json:"total_people"`
	BackfillCursor    uint  `json:"backfill_cursor"`
	BackfillCompleted bool  `json:"backfill_completed"`

	// Center 聚合：仅统计活动 generation（center.generation = profile.active_generation，
	// profile.status=ready，active_generation>0，对应 people 仍存在）的中心。
	CenterTotal          int64   `json:"center_total"`
	CenterActive         int64   `json:"center_active"`
	CenterConfirmed      int64   `json:"center_confirmed"`
	CenterAvgPerProfile  float64 `json:"center_avg_per_profile"`
	CenterMaxPerProfile  int     `json:"center_max_per_profile"`
	CenterActiveProfiles int64   `json:"center_active_profiles"` // 拥有活动中心且 ready 的人物数，用于校验 avg 分母

	// Member 聚合：仅统计活动 generation 的 member。
	MemberTotal     int64 `json:"member_total"`
	MemberAccepted  int64 `json:"member_accepted"`
	MemberCandidate int64 `json:"member_candidate"`
	MemberExcluded  int64 `json:"member_excluded"`
}

// IdentityDecisionSummary 汇总指定时间窗口内身份画像 shadow/rescue 决策的计数。
// 由 repository 通过 SELECT decision, COUNT(*) GROUP BY decision 聚合填充。
// 未知 decision 计入 Total 但不写入已知分类。空表返回零值，不返回 nil。
type IdentityDecisionSummary struct {
	WindowHours           int   `json:"window_hours"`
	Total                 int64 `json:"total"`
	Agree                 int64 `json:"agree"`
	Disagree              int64 `json:"disagree"`
	LegacyMissProfileHit  int64 `json:"legacy_miss_profile_hit"`
	LegacyMissProfileMiss int64 `json:"legacy_miss_profile_miss"`
	ProfileMiss           int64 `json:"profile_miss"`
	ProfileUnavailable    int64 `json:"profile_unavailable"`
	ProfileBlocked        int64 `json:"profile_blocked"`
	RescueApplied         int64 `json:"rescue_applied"`
}

// MemberByFaceID 返回指定人脸在本次构建中的成员记录，未找到返回 nil。
func (b *PersonIdentityProfileBuild) MemberByFaceID(faceID uint) *PersonIdentityCenterMember {
	if b == nil {
		return nil
	}
	for _, m := range b.Members {
		if m.FaceID == faceID {
			return m
		}
	}
	return nil
}
