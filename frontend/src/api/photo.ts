import http from '@/utils/request'
import type { Photo, PhotoListParams, PhotoStats, ScanPhotosRequest, ScanPhotosResponse, RebuildPhotosRequest, RebuildPhotosResponse, CleanupPhotosResponse, CountPhotosByPathsRequest, CountPhotosByPathsResponse } from '@/types/photo'
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

  // 异步重建照片（新接口，立即返回任务 ID）
  startRebuild(data?: RebuildPhotosRequest) {
    return http.post<ApiResponse<{ task_id: string }>>('/photos/rebuild/async', data || {})
  },

  // 同步扫描照片（已弃用，保留兼容）
  scan(data?: ScanPhotosRequest) {
    return http.post<ApiResponse<ScanPhotosResponse>>('/photos/scan', data || {}, {
      timeout: 300000, // 5 分钟
    })
  },

  // 同步重建照片（已弃用，保留兼容）
  rebuild(data?: RebuildPhotosRequest) {
    return http.post<ApiResponse<RebuildPhotosResponse>>('/photos/rebuild', data || {})
  },

  // 清理不存在文件的照片
  cleanup() {
    return http.post<ApiResponse<CleanupPhotosResponse>>('/photos/cleanup', {})
  },

  // 获取照片统计
  getStats() {
    return http.get<ApiResponse<PhotoStats>>('/photos/stats')
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
}
