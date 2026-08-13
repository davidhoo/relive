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
  exclusion_reason?: 'non_face' | 'low_quality' | ''
  excluded_at?: string
  updated_at?: string
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
  excluded_faces?: Face[]
  pending_review_faces?: Face[]
}

export type ExclusionReason = 'non_face' | 'low_quality'

export interface UpdateFaceExclusionResult {
  updated: number
  photos: PhotoPeopleResponse[]
}

// ---- 人脸质检审核 ----

export type FaceQualityState =
  | 'pending_review'
  | 'historical_missing_evidence'
  | 'rescore_retryable'
  | 'auto_excluded'
  | 'manual_confirmed'
export type FaceQualityAction =
  | 'confirm_exclude'
  | 'mark_non_face'
  | 'mark_low_quality'
  | 'accept'
  | 'restore'

export interface FaceQualityStats {
  pending_review: number
  historical_missing_evidence: number
  rescore_retryable: number
  auto_excluded: number
  manual_confirmed: number
  total: number
  by_reason: Record<string, number>
  by_rule_version: Record<string, number>
}

export interface FaceQualityReasonStats {
  reason: string
  count: number
}

export interface FaceQualityEvidenceV2 {
  evidence_schema_version: string
  primary_detector_score: number
  verification_status: 'face' | 'no_face' | 'uncertain' | 'error' | string
  verifier_score: number
  verifier_name: string
  verifier_version: string
  original_width: number
  original_height: number
  face_box_width_px: number
  face_box_height_px: number
  context_crop_width_px: number
  context_crop_height_px: number
  context_expand_ratio: number
  sharpness_norm?: number
  brightness_norm?: number
  contrast_norm?: number
  occluded?: boolean
  quality_domain?: string
  quality_version?: string
  reason_codes?: string[]
  suggested_decision?: string
  rule_version: string
  model_version: string
}

export interface FaceQualityReviewItem {
  event_id: number
  photo_id: number
  face_id?: number
  decision: string
  reason?: string
  source: string
  rule_version: string
  model_version: string
  reason_codes?: string[]
  review_action?: string
  reviewed_at?: string
  restored_at?: string
  created_at: string
  updated_at: string
  bbox_x: number
  bbox_y: number
  bbox_width: number
  bbox_height: number
  thumbnail_path?: string
  photo_file_path?: string
  photo_thumbnail?: string
  face_validity_score: number
  quality_score: number
  evidence_json?: string
  // 证据来源/状态（向后兼容新增）。
  evidence_origin?: string
  evidence_state?: string
  rescore_run_id?: number
  // 证据管线：legacy_v1 / independent_v2。
  evidence_pipeline?: 'legacy_v1' | 'independent_v2' | string
  // v2 独立复核结构化证据（仅 evidence_pipeline=independent_v2 时存在）。
  evidence_v2?: FaceQualityEvidenceV2
  // shadow 校准的系统建议决策（non_face/low_quality）。
  suggested_decision?: string
  // 仅当 evidence_json 非空且可解析为质检证据时为 true。
  // 区分“模型真实评分（含 0 分）”与“历史回填无证据样本”。缺失字段按 false 处理。
  quality_evidence_available: boolean
}

export interface FaceQualityReviewPage {
  items: FaceQualityReviewItem[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

export interface FaceQualityReviewParams {
  state?: FaceQualityState
  reason?: string
  source?: string
  rule_version?: string
  start_time?: string
  end_time?: string
  page?: number
  page_size?: number
}

export interface FaceQualityDecisionResult {
  processed: number
}

export interface FaceQualityRestoreResult {
  restored: number
  rule_version?: string
}

// ---- 历史重评分运行 ----

export type FaceQualityRescoreMode = 'calibration' | 'full'
export type FaceQualityRescoreApplyMode = 'shadow' | 'enforce'
export type FaceQualityRescoreStatus =
  | 'queued'
  | 'running'
  | 'paused'
  | 'completed'
  | 'completed_with_errors'
  | 'failed'
  | 'cancelled'

export interface FaceQualityRescoreRun {
  id: number
  mode: FaceQualityRescoreMode
  apply_mode: FaceQualityRescoreApplyMode
  status: FaceQualityRescoreStatus
  target_photo_count: number
  target_face_count: number
  processed_photo_count: number
  processed_face_count: number
  accepted_count: number
  review_required_count: number
  auto_excluded_count: number
  retryable_count: number
  superseded_manual_count: number
  last_error?: string
  started_at?: string
  completed_at?: string
  rule_version: string
  model_version: string
  photo_limit: number
  retry_of_run_id?: number
  calibration_run_id?: number
  // 证据管线：legacy_v1 / independent_v2。v1 run 不可作为 v2 enforce 校准。
  pipeline_version?: 'legacy_v1' | 'independent_v2' | string
  // 目标快照范围语义。
  target_scope?: string
  eligible_for_enforce: boolean
  created_at: string
  updated_at: string
}

export interface FaceQualityRescoreRunCreateRequest {
  mode: FaceQualityRescoreMode
  photo_limit?: number
  // mode=full 时必填，指向服务端验证通过的合格校准 run。
  calibration_run_id?: number
  // 证据管线：未填默认 independent_v2（本任务主链路）。
  pipeline_version?: 'legacy_v1' | 'independent_v2' | string
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
