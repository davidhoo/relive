import http from '@/utils/request'
import type { ESP32Device, DeviceStats } from '@/types/device'
import type { ApiResponse, PagedResponse } from '@/types/api'

export const deviceApi = {
  getList(params?: { page?: number; page_size?: number }) {
    return http.get<ApiResponse<PagedResponse<ESP32Device>>>('/devices', { params })
  },

  getById(deviceId: string) {
    return http.get<ApiResponse<ESP32Device>>(`/devices/${deviceId}`)
  },

  create(data: CreateDeviceRequest) {
    return http.post<ApiResponse<CreateDeviceResponse>>('/devices', data)
  },

  delete(id: number) {
    return http.delete<ApiResponse<void>>(`/devices/${id}`)
  },

  updateEnabled(id: number, enabled: boolean) {
    return http.put<ApiResponse<void>>(`/devices/${id}/enabled`, { enabled })
  },

  updateRenderProfile(id: number, renderProfile: string) {
    return http.put<ApiResponse<void>>(`/devices/${id}/render-profile`, { render_profile: renderProfile })
  },

  getStats() {
    return http.get<ApiResponse<DeviceStats>>('/devices/stats')
  },
}

export interface CreateDeviceRequest {
  name: string
  device_type?: string
  description?: string
  render_profile?: string
}

export interface UpdateDeviceEnabledRequest {
  enabled: boolean
}

export interface CreateDeviceResponse {
  id: number
  created_at: string
  device_id: string
  name: string
  api_key: string
  device_type: string
  description: string
  render_profile?: string
}
