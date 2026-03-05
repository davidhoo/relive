// 设备模型（支持多种硬件平台：ESP32、Android、iOS等）
export interface ESP32Device {
  id: number
  device_id: string
  device_name?: string
  name?: string
  device_type?: string      // 设备类型：embedded, mobile, web, offline, service
  ip_address?: string
  is_enabled?: boolean      // 是否可用（服务端控制）
  online?: boolean
  is_online?: boolean
  last_heartbeat?: string
  battery_level?: number
  wifi_rssi?: number
  photo_count?: number
  api_key?: string          // API Key（仅在详情接口返回）
  description?: string      // 描述/备注
  created_at: string
  updated_at: string
}

// 设备统计
export interface DeviceStats {
  total: number
  total_devices?: number
  online: number
  online_devices?: number
  offline_devices?: number
}
