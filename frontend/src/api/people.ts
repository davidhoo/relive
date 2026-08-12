import http from '@/utils/request'
import type { ApiResponse, PagedResponse, CursorPagedResponse } from '@/types/api'
import type { Photo } from '@/types/photo'
import type {
  ExclusionReason,
  Face,
  FaceQualityAction,
  FaceQualityDecisionResult,
  FaceQualityRescoreRun,
  FaceQualityRescoreRunCreateRequest,
  FaceQualityRestoreResult,
  FaceQualityReviewPage,
  FaceQualityReviewParams,
  FaceQualityStats,
  PeopleBackgroundLogsResponse,
  PeopleListParams,
  PeopleMergeJob,
  PeopleStats,
  PeopleTask,
  Person,
  PersonMergeSuggestion,
  PersonMergeSuggestionStats,
  PersonMergeSuggestionTask,
  PhotoPeopleResponse,
  UpdateFaceExclusionResult,
  UpdateVisibilityResult,
} from '@/types/people'

export const peopleApi = {
  getList(params?: PeopleListParams) {
    return http.get<ApiResponse<PagedResponse<Person>>>('/people', { params })
  },

  getById(id: number) {
    return http.get<ApiResponse<Person>>(`/people/${id}`)
  },

  getPhotos(id: number, params?: { page?: number; page_size?: number; pagination?: 'cursor'; cursor?: string }) {
    return http.get<ApiResponse<Photo[] | PagedResponse<Photo> | CursorPagedResponse<Photo>>>(`/people/${id}/photos`, { params })
  },

  getFaces(id: number, params?: { page?: number; page_size?: number; pagination?: 'cursor'; cursor?: string }) {
    return http.get<ApiResponse<Face[] | PagedResponse<Face> | CursorPagedResponse<Face>>>(`/people/${id}/faces`, { params })
  },

  updateCategory(id: number, category: Person['category']) {
    return http.patch<ApiResponse<void>>(`/people/${id}/category`, { category })
  },

  updateName(id: number, name: string) {
    return http.patch<ApiResponse<void>>(`/people/${id}/name`, { name })
  },

  updateAvatar(id: number, faceId: number) {
    return http.patch<ApiResponse<void>>(`/people/${id}/avatar`, { face_id: faceId })
  },

  merge(targetPersonId: number, sourcePersonIds: number[]) {
    return http.post<ApiResponse<{ job_id: number; status: string }>>('/people/merge', {
      target_person_id: targetPersonId,
      source_person_ids: sourcePersonIds,
    })
  },

  updateVisibility(personIds: number[], hidden: boolean) {
    return http.patch<ApiResponse<UpdateVisibilityResult>>('/people/visibility', {
      person_ids: personIds,
      hidden,
    })
  },

  getMergeJob(jobId: number) {
    return http.get<ApiResponse<PeopleMergeJob>>(`/people/merge-jobs/${jobId}`)
  },

  split(sourcePersonId: number, faceIds: number[]) {
    return http.post<ApiResponse<Person>>('/people/split', {
      source_person_id: sourcePersonId,
      face_ids: faceIds,
    })
  },

  moveFaces(faceIds: number[], targetPersonId: number) {
    return http.post<ApiResponse<void>>('/people/move-faces', {
      face_ids: faceIds,
      target_person_id: targetPersonId,
    })
  },

  /**
   * 照片详情页对单张人脸执行“改名”归属变更。后端聚合接口，原子完成移动/拆分/命名/分类。
   * 返回更新后的照片人物信息，前端直接刷新即可。
   */
  assignFacePerson(faceId: number, payload: { name: string; category: string; target_person_id?: number }) {
    return http.post<ApiResponse<PhotoPeopleResponse>>(`/people/faces/${faceId}/person-assignment`, payload)
  },

  updateFaceExclusion(faceIds: number[], excluded: boolean, reason?: ExclusionReason) {
    return http.patch<ApiResponse<UpdateFaceExclusionResult>>('/people/faces/exclusion', {
      face_ids: faceIds,
      excluded,
      reason: excluded ? reason : undefined,
    })
  },

  // ---- 人脸质检审核 ----

  getFaceQualityStats() {
    return http.get<ApiResponse<FaceQualityStats>>('/people/face-quality/stats')
  },

  listFaceQualityReviews(params?: FaceQualityReviewParams) {
    return http.get<ApiResponse<FaceQualityReviewPage>>('/people/face-quality/reviews', { params })
  },

  applyFaceQualityDecision(eventIds: number[], action: FaceQualityAction, reason?: string) {
    return http.patch<ApiResponse<FaceQualityDecisionResult>>('/people/faces/quality-decision', {
      event_ids: eventIds,
      action,
      reason,
    })
  },

  restoreAutoFaceQuality(ruleVersion: string, limit?: number) {
    return http.post<ApiResponse<FaceQualityRestoreResult>>('/people/face-quality/restore-auto', {
      rule_version: ruleVersion,
      limit,
    })
  },

  // ---- 历史重评分运行 ----

  listFaceQualityRescoreRuns(limit?: number) {
    return http.get<ApiResponse<{ items: FaceQualityRescoreRun[] }>>('/people/face-quality/rescore-runs', {
      params: limit ? { limit } : undefined,
    })
  },

  getFaceQualityRescoreRun(id: number) {
    return http.get<ApiResponse<FaceQualityRescoreRun>>(`/people/face-quality/rescore-runs/${id}`)
  },

  createFaceQualityRescoreRun(req: FaceQualityRescoreRunCreateRequest) {
    return http.post<ApiResponse<FaceQualityRescoreRun>>('/people/face-quality/rescore-runs', req)
  },

  pauseFaceQualityRescoreRun(id: number) {
    return http.post<ApiResponse<void>>(`/people/face-quality/rescore-runs/${id}/pause`)
  },

  resumeFaceQualityRescoreRun(id: number) {
    return http.post<ApiResponse<void>>(`/people/face-quality/rescore-runs/${id}/resume`)
  },

  cancelFaceQualityRescoreRun(id: number) {
    return http.post<ApiResponse<void>>(`/people/face-quality/rescore-runs/${id}/cancel`)
  },

  restoreAutoFaceQualityRescoreRun(id: number, limit?: number) {
    return http.post<ApiResponse<FaceQualityRestoreResult>>(
      `/people/face-quality/rescore-runs/${id}/restore-auto`,
      null,
      { params: limit ? { limit } : undefined },
    )
  },

  getTask() {
    return http.get<ApiResponse<PeopleTask | null>>('/people/task')
  },

  getStats() {
    return http.get<ApiResponse<PeopleStats>>('/people/stats')
  },

  getBackgroundLogs() {
    return http.get<ApiResponse<PeopleBackgroundLogsResponse>>('/people/background/logs')
  },

  startBackground() {
    return http.post<ApiResponse<PeopleTask>>('/people/background/start')
  },

  stopBackground() {
    return http.post<ApiResponse<void>>('/people/background/stop')
  },

  resetAllPeople() {
    return http.post<ApiResponse<{ photos_enqueued: number; background_started: boolean }>>('/people/reset')
  },

  dissolvePerson(id: number) {
    return http.post<ApiResponse<{ faces_released: number }>>(`/people/${id}/dissolve`)
  },

  rescanByPath(path: string) {
    return http.post<ApiResponse<{ count: number; background_started?: boolean }>>('/people/rescan-by-path', { path })
  },

  enqueueUnprocessed() {
    return http.post<ApiResponse<{ enqueued: number }>>('/people/enqueue-unprocessed')
  },

  getPhotoPeople(photoId: number) {
    return http.get<ApiResponse<PhotoPeopleResponse>>(`/photos/${photoId}/people`)
  },

  enqueueFaceDetection(photoId: number, force = false) {
    return http.post<ApiResponse<{ photo_id: number; force: boolean }>>(`/photos/${photoId}/face-detection`, null, {
      params: force ? { force: 'true' } : undefined,
    })
  },

  getMergeSuggestionTask() {
    return http.get<ApiResponse<PersonMergeSuggestionTask | null>>('/people/merge-suggestions/task')
  },

  getMergeSuggestionStats() {
    return http.get<ApiResponse<PersonMergeSuggestionStats>>('/people/merge-suggestions/stats')
  },

  getMergeSuggestionLogs() {
    return http.get<ApiResponse<PeopleBackgroundLogsResponse>>('/people/merge-suggestions/background/logs')
  },

  pauseMergeSuggestionTask() {
    return http.post<ApiResponse<void>>('/people/merge-suggestions/background/pause')
  },

  resumeMergeSuggestionTask() {
    return http.post<ApiResponse<void>>('/people/merge-suggestions/background/resume')
  },

  rebuildMergeSuggestionTask() {
    return http.post<ApiResponse<void>>('/people/merge-suggestions/background/rebuild')
  },

  listMergeSuggestions(params?: { page?: number; page_size?: number }) {
    return http.get<ApiResponse<PagedResponse<PersonMergeSuggestion>>>('/people/merge-suggestions', { params })
  },

  /**
   * 获取合并建议详情。
   * acceptNotFound: 显式开启时，将 404 视为预期响应（建议已处理完毕），
   * 通过 validateStatus 放行 2xx 与 404，避免进入 Axios 全局错误拦截器弹出
   * “Merge suggestion not found”。该行为仅限本次请求，不影响全局 404 处理。
   * 其他 4xx/5xx 仍按失败处理。
   */
  getMergeSuggestion(id: number, options?: { acceptNotFound?: boolean }) {
    const config = options?.acceptNotFound
      ? { validateStatus: (status: number) => (status >= 200 && status < 300) || status === 404 }
      : undefined
    return http.get<ApiResponse<PersonMergeSuggestion>>(`/people/merge-suggestions/${id}`, config)
  },

  excludeMergeSuggestionCandidates(id: number, candidatePersonIds: number[]) {
    return http.post<ApiResponse<void>>(`/people/merge-suggestions/${id}/exclude`, {
      candidate_person_ids: candidatePersonIds,
    })
  },

  applyMergeSuggestion(id: number, candidatePersonIds: number[]) {
    return http.post<ApiResponse<void>>(`/people/merge-suggestions/${id}/apply`, {
      candidate_person_ids: candidatePersonIds,
    })
  },

  calculateSimilarity(personId: number, targetPersonId: number) {
    return http.post<ApiResponse<{
      person_id_1: number
      person_id_2: number
      similarity_score: number
      merge_threshold: number
      attach_threshold: number
    }>>(`/people/${personId}/similarity`, {
      target_person_id: targetPersonId,
    })
  },
}
