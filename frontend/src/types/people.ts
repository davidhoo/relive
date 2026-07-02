export type PersonCategory = 'family' | 'friend' | 'acquaintance' | 'stranger'
export type FaceProcessStatus = 'none' | 'pending' | 'processing' | 'ready' | 'no_face' | 'failed'
export type PeopleVisibility = 'visible' | 'hidden' | 'all'

export interface Face {
  id: number
  photo_id: number
  person_id?: number
  bbox_x?: number
  bbox_y?: number
  bbox_width?: number
  bbox_height?: number
  confidence?: number
  quality_score?: number
  thumbnail_path?: string
  cluster_status?: string
  cluster_score?: number
  manual_locked?: boolean
  manual_lock_reason?: string
  manual_locked_at?: string
  recluster_generation?: number
}

export interface Person {
  id: number
  name?: string
  category: PersonCategory
  representative_face_id?: number
  has_avatar: boolean
  avatar_locked?: boolean
  face_count: number
  photo_count: number
  hidden: boolean
  created_at: string
  updated_at: string
  faces?: Face[]
}

export interface PeopleListParams {
  page?: number
  page_size?: number
  category?: PersonCategory
  search?: string
  has_avatar?: string // 'true' 只返回有头像的人物
  visibility?: PeopleVisibility
}

export interface UpdateVisibilityResult {
  updated: number
  requested: number
  hidden: boolean
  missing_count: number
}

export interface PeopleTask {
  status?: string
  current_photo_id?: number
  current_phase?: 'detecting' | 'clustering' | 'idle' | string
  current_message?: string
  processed_jobs: number
  started_at?: string
  stopped_at?: string
}

export interface PeopleStats {
  total: number
  pending: number
  queued: number
  processing: number
  completed: number
  failed: number
  cancelled: number
  pending_faces_total: number
  pending_faces_never_clustered: number
  pending_faces_retried: number
  total_faces: number
  // 已检测照片数（按照片当前 face_process_status 计算，独立于任务明细）
  detected_photos: number
  // 待检测照片数（face_process_status 为 none/pending/processing 的活跃照片）
  pending_photos: number
}

export interface PeopleBackgroundLogsResponse {
  lines: string[]
}

export interface PhotoPeopleResponse {
  photo_id: number
  face_process_status: FaceProcessStatus
  face_count: number
  top_person_category?: PersonCategory | ''
  people: Person[]
}

export interface PersonMergeSuggestionTask {
  status?: string
  current_message?: string
  processed_pairs: number
  started_at?: string
  stopped_at?: string
}

export interface PersonMergeSuggestionStats {
  total: number
  pending: number
  applied: number
  dismissed: number
  obsolete: number
  pending_items: number
  excluded_items: number
  merged_items: number
}

export interface PersonMergeSuggestionItem {
  id: number
  suggestion_id: number
  candidate_person_id: number
  similarity_score: number
  rank: number
  status: string
  match_source: 'legacy' | 'identity_profile'
  warning?: 'same_photo_cooccurrence'
  candidate_person?: Person
}

export interface PersonMergeSuggestion {
  id: number
  target_person_id: number
  target_category_snapshot: string
  status: string
  candidate_count: number
  top_similarity: number
  reviewed_at?: string
  created_at: string
  updated_at: string
  target_person?: Person
  items?: PersonMergeSuggestionItem[]
}

export interface PeopleMergeJob {
  id: number
  type: 'merge_into' | 'merge_to'
  status: 'pending' | 'processing' | 'completed' | 'failed'
  target_id: number
  source_ids: string
  result?: string
  error_message?: string
  started_at?: string
  completed_at?: string
  created_at: string
  updated_at: string
}
