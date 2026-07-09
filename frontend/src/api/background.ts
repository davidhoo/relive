import http from '@/utils/request'
import type { ApiResponse } from '@/types/api'
import type { BackgroundTaskStatus } from '@/types/background'

export const backgroundApi = {
  /**
   * 获取后台任务治理状态（只读快照：foreground/cooldown/running/load/thresholds）。
   * 仅 People 页面打开时按需轮询，频率最多 30s 一次。
   */
  getStatus() {
    return http.get<ApiResponse<BackgroundTaskStatus>>('/background/status')
  },
}
