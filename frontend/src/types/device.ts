export interface Device {
  id: number
  device_id: string
  device_name?: string
  name?: string
  device_type?: string
  render_profile?: string
  ip_address?: string
  is_enabled?: boolean
  online?: boolean
  is_online?: boolean
  last_seen?: string
  photo_count?: number
  api_key?: string
  description?: string
  created_at: string
  updated_at: string
}

export interface DeviceStats {
  total: number
  total_devices?: number
  online: number
  online_devices?: number
  offline_devices?: number
}
