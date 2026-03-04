import http from '@/utils/request'
import type { ESP32Device, DeviceStats } from '@/types/device'
import type { ApiResponse, PagedResponse } from '@/types/api'

// 设备管理 API（支持多种硬件平台）
// 注：使用 /esp32/* 路径是为了向后兼容，实际支持所有设备类型
export const deviceApi = {
  // 获取设备列表
  getList(params?: { page?: number; page_size?: number }) {
    return http.get<ApiResponse<PagedResponse<ESP32Device>>>('/esp32/devices', { params })
  },

  // 获取设备详情
  getById(deviceId: string) {
    return http.get<ApiResponse<ESP32Device>>(`/esp32/devices/${deviceId}`)
  },

  // 获取设备统计
  getStats() {
    return http.get<ApiResponse<DeviceStats>>('/esp32/stats')
  },
}
