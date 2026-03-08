import http from '@/utils/request'
import type { ApiResponse } from '@/types/api'
import type { ThumbnailTask, ThumbnailStats, ThumbnailBackgroundLogsResponse } from '@/types/thumbnail'

export const thumbnailApi = {
  startBackground() {
    return http.post<ApiResponse<ThumbnailTask>>('/thumbnails/background/start')
  },

  stopBackground() {
    return http.post<ApiResponse<void>>('/thumbnails/background/stop')
  },

  getTask() {
    return http.get<ApiResponse<ThumbnailTask | null>>('/thumbnails/task')
  },

  getBackgroundLogs() {
    return http.get<ApiResponse<ThumbnailBackgroundLogsResponse>>('/thumbnails/background/logs')
  },

  getStats() {
    return http.get<ApiResponse<ThumbnailStats>>('/thumbnails/stats')
  },

  enqueue(photoId: number) {
    return http.post<ApiResponse<void>>('/thumbnails/enqueue', { photo_id: photoId })
  },
}
