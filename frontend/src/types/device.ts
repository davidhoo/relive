// ESP32 设备模型
export interface ESP32Device {
  id: number
  device_id: string
  device_name?: string
  name?: string
  screen_width: number
  screen_height: number
  firmware_version?: string
  mac_address?: string
  ip_address?: string
  online: boolean
  is_online?: boolean
  last_heartbeat?: string
  battery_level?: number
  wifi_rssi?: number
  photo_count?: number
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
