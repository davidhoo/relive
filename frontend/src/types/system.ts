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
  database_updated_at?: string
  go_version?: string
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

// 系统环境信息
export interface SystemEnvironment {
  is_docker: boolean
  default_path: string
  work_dir: string
}
