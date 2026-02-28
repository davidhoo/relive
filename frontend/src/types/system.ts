// 系统统计
export interface SystemStats {
  total_photos: number
  analyzed_photos: number
  unanalyzed_photos: number
  total_devices: number
  online_devices: number
  total_displays: number
  storage_size?: number
  database_size?: number
  go_version?: string
  started_at?: string
  uptime?: number
}

// 系统健康
export interface SystemHealth {
  status: string
  version?: string
  uptime?: number
  timestamp?: string
  time?: string
}
