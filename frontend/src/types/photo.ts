// 照片模型
export interface Photo {
  id: number
  file_path: string
  file_name?: string
  file_size?: number
  file_hash: string

  // EXIF 信息
  taken_at?: string
  camera_model?: string
  width?: number
  height?: number
  orientation?: number
  gps_latitude?: number
  gps_longitude?: number
  location?: string

  // 设备信息
  esp32_device_id?: string

  // AI 分析结果
  ai_analyzed: boolean
  analyzed_at?: string
  ai_provider?: string
  description?: string
  caption?: string
  memory_score?: number
  beauty_score?: number
  emotion_score?: number
  technical_score?: number
  overall_score?: number
  main_category?: string
  tags?: string

  // 时间戳
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
  path?: string  // Optional - uses default from config if not provided
}

// 扫描照片响应
export interface ScanPhotosResponse {
  scanned_count: number
  new_count: number
  updated_count: number
}
