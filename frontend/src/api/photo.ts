import http from '@/utils/request'
import type { Photo, PhotoListParams, PhotoStats, ScanPhotosRequest, RebuildPhotosRequest, CleanupPhotosResponse, CountPhotosByPathsRequest, CountPhotosByPathsResponse, CountDerivedStatusByPathsRequest, CountDerivedStatusByPathsResponse, PhotoCountsResponse } from '@/types/photo'
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

  // 获取所有标签
  getTags() {
    return http.get<ApiResponse<string[]>>('/photos/tags')
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

  // 更新照片分类
  updateCategory(id: number, category: string) {
    return http.patch<ApiResponse<any>>(`/photos/${id}/category`, { category })
  },

  // 手动设置照片位置
  setLocation(id: number, data: { latitude: number; longitude: number }) {
    return http.patch<ApiResponse<{ location: string }>>(`/photos/${id}/location`, data)
  },
}
