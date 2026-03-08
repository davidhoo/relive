import http from '@/utils/request'
import type { ApiResponse } from '@/types/api'
import type { GeocodeTask, GeocodeStats, GeocodeBackgroundLogsResponse } from '@/types/geocode'

export const geocodeApi = {
  startBackground() {
    return http.post<ApiResponse<GeocodeTask>>('/geocode/background/start')
  },
  stopBackground() {
    return http.post<ApiResponse<void>>('/geocode/background/stop')
  },
  getTask() {
    return http.get<ApiResponse<GeocodeTask | null>>('/geocode/task')
  },
  getStats() {
    return http.get<ApiResponse<GeocodeStats>>('/geocode/stats')
  },
  getBackgroundLogs() {
    return http.get<ApiResponse<GeocodeBackgroundLogsResponse>>('/geocode/background/logs')
  },
  enqueue(photoId: number) {
    return http.post<ApiResponse<void>>('/geocode/enqueue', { photo_id: photoId })
  },
}
