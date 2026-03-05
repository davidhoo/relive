import http from '@/utils/request'
import type { ESP32Device, DeviceStats } from '@/types/device'
import type { ApiResponse, PagedResponse } from '@/types/api'

// 设备管理 API（支持多种硬件平台）
export const deviceApi = {
  // 获取设备列表
  getList(params?: { page?: number; page_size?: number }) {
    return http.get<ApiResponse<PagedResponse<ESP32Device>>>('/devices', { params })
  },

  // 获取设备详情
  getById(deviceId: string) {
    return http.get<ApiResponse<ESP32Device>>(`/devices/${deviceId}`)
  },

  // 创建设备
  create(data: CreateDeviceRequest) {
    return http.post<ApiResponse<CreateDeviceResponse>>('/devices', data)
  },

  // 删除设备
  delete(id: number) {
    return http.delete<ApiResponse<void>>(`/devices/${id}`)
  },

  // 更新设备可用状态
  updateEnabled(id: number, enabled: boolean) {
    return http.put<ApiResponse<void>>(`/devices/${id}/enabled`, { enabled })
  },

  // 获取设备统计
  getStats() {
    return http.get<ApiResponse<DeviceStats>>('/devices/stats')
  },
}

// 创建设备请求
export interface CreateDeviceRequest {
  name: string
  device_type?: string
  hardware_model?: string
  platform?: string
  screen_width?: number
  screen_height?: number
  firmware_version?: string
  mac_address?: string
  description?: string
}

// 更新设备可用状态请求
export interface UpdateDeviceEnabledRequest {
  enabled: boolean
}

// 创建设备响应（包含 API Key）
export interface CreateDeviceResponse {
  id: number
  created_at: string
  device_id: string
  name: string
  api_key: string  // ⚠️ 仅创建时返回
  device_type: string
  platform: string
  screen_width: number
  screen_height: number
  firmware_version: string
  mac_address: string
  description: string
}
