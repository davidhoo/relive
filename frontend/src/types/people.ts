export type PersonCategory = 'family' | 'friend' | 'acquaintance' | 'stranger'
export type FaceProcessStatus = 'none' | 'pending' | 'processing' | 'ready' | 'no_face' | 'failed'

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
  created_at: string
  updated_at: string
  faces?: Face[]
}

export interface PeopleListParams {
  page?: number
  page_size?: number
  category?: PersonCategory
  search?: string
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
