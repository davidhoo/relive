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

  // 扫描照片
  scan(data?: ScanPhotosRequest) {
    return http.post<ApiResponse<ScanPhotosResponse>>('/photos/scan', data || {})
  },

  // 重建照片（重新扫描文件、提取 EXIF、计算哈希、地理编码、生成缩略图，保留 AI 分析结果）
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
