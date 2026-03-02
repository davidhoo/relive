import { http } from '@/utils/request'
import type { Photo, PhotoListParams, PhotoStats, ScanPhotosRequest, ScanPhotosResponse } from '@/types/photo'
import type { PagedResponse } from '@/types/api'

export const photoApi = {
  // 获取照片列表
  getList(params?: PhotoListParams) {
    return http.get<PagedResponse<Photo>>('/photos', { params })
  },

  // 获取照片详情
  getById(id: number) {
    return http.get<Photo>(`/photos/${id}`)
  },

  // 扫描照片
  scan(data?: ScanPhotosRequest) {
    return http.post<ScanPhotosResponse>('/photos/scan', data || {})
  },

  // 重新扫描照片（强制更新所有信息）
  rescan(data?: ScanPhotosRequest) {
    return http.post<ScanPhotosResponse>('/photos/rescan', data || {})
  },

  // 获取照片统计
  getStats() {
    return http.get<PhotoStats>('/photos/stats')
  },
}
