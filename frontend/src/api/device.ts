import { http } from '@/utils/request'
import type { ESP32Device, DeviceStats } from '@/types/device'
import type { PagedResponse } from '@/types/api'

export const deviceApi = {
  // 获取设备列表
  getList(params?: { page?: number; page_size?: number }) {
    return http.get<PagedResponse<ESP32Device>>('/esp32/devices', { params })
  },

  // 获取设备详情
  getById(deviceId: string) {
    return http.get<ESP32Device>(`/esp32/devices/${deviceId}`)
  },

  // 获取设备统计
  getStats() {
    return http.get<DeviceStats>('/esp32/stats')
  },
}
