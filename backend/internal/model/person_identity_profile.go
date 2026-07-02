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
