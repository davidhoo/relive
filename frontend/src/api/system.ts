import http from '@/utils/request'
import type { SystemHealth, SystemStats } from '@/types/system'

export const systemApi = {
  // 获取系统健康状态
  getHealth() {
    return http.get<SystemHealth>('/system/health')
  },

  // 获取系统统计
  getStats() {
    return http.get<SystemStats>('/system/stats')
  },
}
