import http from '@/utils/request'
import type { Photo, PhotoListParams, PhotoStats, ScanPhotosRequest, RebuildPhotosRequest, CleanupPhotosResponse, CountPhotosByPathsRequest, CountPhotosByPathsResponse, CountDerivedStatusByPathsRequest, CountDerivedStatusByPathsResponse, PhotoCountsResponse, TagsResponse, AdjacentPhotosResponse, OrientationSuggestionGroup, OrientationSuggestionDetail, OrientationSuggestionStats, OrientationSuggestionTask } from '@/types/photo'
import type { ApiResponse, PagedResponse } from '@/types/api'

export const photoApi = {
  // 获取照片列表
  getList(params?: PhotoListParams) {
    return http.get<ApiResponse<PagedResponse<Photo>>>('/photos', { params })
  },

  // 获取照片详情
  getById(id: number) {
    return http.get<ApiResponse<Photo>>(`/photos/${id}`)
  },

  // 获取相邻照片 ID
  getAdjacent(id: number, params?: PhotoListParams) {
    return http.get<ApiResponse<AdjacentPhotosResponse>>(`/photos/${id}/adjacent`, { params })
  },

  // 异步扫描照片（新接口，立即返回任务 ID）
  startScan(data?: ScanPhotosRequest) {
    return http.post<ApiResponse<{ task_id: string }>>('/photos/scan/async', data || {})
  },

  // 获取扫描任务状态
  getScanTask() {
    return http.get<ApiResponse<{ task: any; is_running: boolean }>>('/photos/scan/task')
  },

  // 停止当前扫描/重建任务
  stopScanTask(taskId: string) {
    return http.post<ApiResponse<any>>(`/photos/tasks/${taskId}/stop`, {})
  },

  // 异步重建照片（新接口，立即返回任务 ID）
  startRebuild(data?: RebuildPhotosRequest) {
    return http.post<ApiResponse<{ task_id: string }>>('/photos/rebuild/async', data || {})
  },

  // 清理不存在文件的照片
  cleanup() {
    return http.post<ApiResponse<CleanupPhotosResponse>>('/photos/cleanup', {})
  },

  // 获取照片统计
  getStats() {
    return http.get<ApiResponse<PhotoStats>>('/photos/stats')
  },

  // 获取照片按状态计数（轻量接口）
  getCounts() {
    return http.get<ApiResponse<PhotoCountsResponse>>('/photos/counts')
  },

  // 获取所有分类
  getCategories() {
    return http.get<ApiResponse<string[]>>('/photos/categories')
  },

  // 获取热门标签（支持搜索）
  getTags(params?: { q?: string; limit?: number }) {
    return http.get<ApiResponse<TagsResponse>>('/photos/tags', { params })
  },

  // 按路径统计照片数量
  countByPaths(data: CountPhotosByPathsRequest) {
    return http.post<ApiResponse<CountPhotosByPathsResponse>>('/photos/count-by-paths', data)
  },

  // 按路径统计缩略图/GPS 派生状态
  countDerivedStatusByPaths(data: CountDerivedStatusByPathsRequest) {
    return http.post<ApiResponse<CountDerivedStatusByPathsResponse>>('/photos/derived-status-by-paths', data)
  },

  // 批量更新照片状态（排除/恢复）
  batchUpdateStatus(data: { photo_ids: number[]; status: string }) {
    return http.patch<ApiResponse<{ affected: number }>>('/photos/batch-status', data)
  },

  // 批量旋转照片
  batchRotate(data: { photo_ids: number[]; direction: 'left' | 'right' }) {
    return http.patch<ApiResponse<{ affected: number }>>('/photos/batch-rotation', data)
  },

  // 更新照片分类
  updateCategory(id: number, category: string) {
    return http.patch<ApiResponse<any>>(`/photos/${id}/category`, { category })
  },

  // 手动旋转照片
  updateRotation(id: number, rotation: number) {
    return http.patch<ApiResponse<any>>(`/photos/${id}/rotation`, { rotation })
  },

  // 手动设置照片位置
  setLocation(id: number, data: { latitude: number; longitude: number }) {
    return http.patch<ApiResponse<{ location: string }>>(`/photos/${id}/location`, data)
  },

  // ========== 方向建议 API ==========

  // 获取方向建议分组
  getOrientationGroups() {
    return http.get<ApiResponse<{ groups: OrientationSuggestionGroup[] }>>('/orientation-suggestions/groups')
  },

  // 获取指定旋转角度的方向建议详情
  getOrientationDetail(rotation: number, page?: number, pageSize?: number) {
    return http.get<ApiResponse<OrientationSuggestionDetail>>('/orientation-suggestions/detail', {
      params: { rotation, page, page_size: pageSize }
    })
  },

  // 应用方向建议
  applyOrientationSuggestions(photoIds: number[]) {
    return http.post<ApiResponse<{ applied: number }>>('/orientation-suggestions/apply', { photo_ids: photoIds })
  },

  // 忽略方向建议
  dismissOrientationSuggestions(photoIds: number[]) {
    return http.post<ApiResponse<void>>('/orientation-suggestions/dismiss', { photo_ids: photoIds })
  },

  // 获取方向建议任务状态
  getOrientationTask() {
    return http.get<ApiResponse<OrientationSuggestionTask>>('/orientation-suggestions/task')
  },

  // 获取方向建议统计
  getOrientationStats() {
    return http.get<ApiResponse<OrientationSuggestionStats>>('/orientation-suggestions/stats')
  },

  // 获取方向建议后台日志
  getOrientationLogs() {
    return http.get<ApiResponse<{ lines: string[] }>>('/orientation-suggestions/logs')
  },

  // 暂停方向建议后台任务
  pauseOrientationTask() {
    return http.post<ApiResponse<void>>('/orientation-suggestions/pause')
  },

  // 恢复方向建议后台任务
  resumeOrientationTask() {
    return http.post<ApiResponse<void>>('/orientation-suggestions/resume')
  },

  // 重建方向建议
  rebuildOrientationSuggestions() {
    return http.post<ApiResponse<void>>('/orientation-suggestions/rebuild')
  },
}
