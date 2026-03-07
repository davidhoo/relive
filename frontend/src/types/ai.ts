// AI 分析进度
export interface AIAnalyzeProgress {
  total: number
  completed: number
  failed: number
  is_running: boolean
  mode?: string
  status?: string
  current_photo_id?: number
  current_message?: string
  started_at?: string
}

// AI 批量分析响应
export interface AIAnalyzeBatchResponse {
  task_id: string
  status: string
  total_count: number
  queued: number
}

// AI 分析任务状态
export interface AIAnalyzeTask {
  id: string
  mode?: string
  status: string // pending, running, completed, failed
  total_count: number
  success_count: number
  failed_count: number
  current_index: number
  current_photo_id?: number
  current_message?: string
  started_at: string
  completed_at?: string
  error_message?: string
}

export interface AIBackgroundLogsResponse {
  lines: string[]
}

// AI Provider 信息
export interface AIProviderInfo {
  name: string
  is_available: boolean
}

export interface AnalysisRuntimeStatus {
  resource_key: string
  status: string
  owner_type?: string
  owner_id?: string
  message?: string
  started_at?: string
  last_heartbeat_at?: string
  lease_expires_at?: string
  is_active: boolean
}
