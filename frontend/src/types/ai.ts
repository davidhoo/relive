// AI 分析进度
export interface AIAnalyzeProgress {
  total: number
  completed: number
  failed: number
  is_running: boolean
  current_photo_id?: number
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
  status: string // pending, running, completed, failed
  total_count: number
  success_count: number
  failed_count: number
  current_index: number
  started_at: string
  completed_at?: string
  error_message?: string
}

// AI Provider 信息
export interface AIProviderInfo {
  name: string
  is_available: boolean
  estimated_cost?: string
}
