import http from '@/utils/request'
import type { SystemHealth, SystemStats, SystemResetRequest, SystemResetResponse, SystemEnvironment } from '@/types/system'
import type { ApiResponse } from '@/types/api'

export const systemApi = {
  // 获取系统健康状态
  getHealth() {
    return http.get<ApiResponse<SystemHealth>>('/system/health')
  },

  // 获取系统统计
  getStats() {
    return http.get<ApiResponse<SystemStats>>('/system/stats')
  },

  // 获取系统环境信息
  getEnvironment() {
    return http.get<ApiResponse<SystemEnvironment>>('/system/environment')
  },

  // 系统还原
  reset(data: SystemResetRequest) {
    return http.post<ApiResponse<SystemResetResponse>>('/system/reset', data)
  },
}
