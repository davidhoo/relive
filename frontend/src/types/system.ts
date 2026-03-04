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

// 系统还原请求
export interface SystemResetRequest {
  confirm_text: string
}

// 系统还原响应
export interface SystemResetResponse {
  success: boolean
  message: string
  database_cleared: boolean
  thumbnails_cleared: boolean
  cache_cleared: boolean
  password_reset: boolean
}
