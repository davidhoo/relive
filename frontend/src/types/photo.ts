// 照片模型
export interface Photo {
  id: number
  file_path: string
  file_name?: string
  file_size?: number
  file_hash: string
  width?: number
  height?: number
  taken_at?: string
  location?: string
  camera_model?: string
  esp32_device_id?: string
  is_analyzed: boolean
  analysis_result?: string
  memory_score?: number
  beauty_score?: number
  emotion_score?: number
  technical_score?: number
  overall_score?: number
  tags?: string[]
  ai_provider?: string
  analyzed_at?: string
  created_at: string
  updated_at: string
}

// 照片列表请求参数
export interface PhotoListParams {
  page?: number
  page_size?: number
  analyzed?: boolean
  location?: string
  sort_by?: string
  sort_desc?: boolean
}

// 照片统计
export interface PhotoStats {
  total: number
  analyzed: number
  unanalyzed: number
}

// 扫描照片请求
export interface ScanPhotosRequest {
  path: string
}

// 扫描照片响应
export interface ScanPhotosResponse {
  scanned_count: number
  new_count: number
  updated_count: number
}
