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
  queued: number
}

// AI Provider 信息
export interface AIProviderInfo {
  name: string
  is_available: boolean
  estimated_cost?: string
}
