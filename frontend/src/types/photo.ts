// 照片模型
export interface Photo {
  id: number
  file_path: string
  file_name?: string
  file_size?: number
  file_hash: string
  file_mod_time?: string    // 文件修改时间（来自文件系统）
  file_create_time?: string // 文件创建时间（来自文件系统）

  // EXIF 信息
  taken_at?: string
  camera_model?: string
  width?: number
  height?: number
  orientation?: number
  manual_rotation?: number
  gps_latitude?: number
  gps_longitude?: number
  location?: string
  country?: string
  province?: string
  city?: string
  district?: string
  street?: string
  poi?: string
  geocode_status?: string
  geocode_provider?: string
  geocoded_at?: string
  thumbnail_path?: string
  thumbnail_status?: string
  thumbnail_generated_at?: string

  // AI 分析结果
  ai_analyzed: boolean
  analyzed_at?: string
  ai_provider?: string
  description?: string
  caption?: string
  memory_score?: number
  beauty_score?: number
  overall_score?: number
  score_reason?: string  // 评分理由
  main_category?: string
  tags?: string[]

  // 时间戳
  created_at: string
  updated_at: string

  // 状态
  status?: string // active/excluded
}

// 照片列表请求参数
export interface PhotoListParams {
  page?: number
  page_size?: number
  analyzed?: boolean
  location?: string
  search?: string
  sort_by?: string
  sort_desc?: boolean
  status?: string
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
// 重建照片请求
export interface RebuildPhotosRequest {
  path?: string  // Optional - uses default from config if not provided
}

// 重建照片响应
// 清理照片响应
export interface CleanupPhotosResponse {
  total_count: number
  deleted_count: number
  skipped_count: number
}

// 按路径统计照片数量请求
export interface CountPhotosByPathsRequest {
  paths: string[]
}

// 按路径统计照片数量响应
export interface CountPhotosByPathsResponse {
  counts: Record<string, number>  // key: path, value: count
}

export interface PathDerivedStatus {
  photo_total: number
  analyzed_total: number
  thumbnail_total: number
  thumbnail_ready: number
  thumbnail_failed: number
  thumbnail_pending: number
  geocode_total: number
  geocode_ready: number
  geocode_failed: number
  geocode_pending: number
}

export interface CountDerivedStatusByPathsRequest {
  paths: string[]
}

export interface CountDerivedStatusByPathsResponse {
  stats: Record<string, PathDerivedStatus>
}

export interface PhotoCountsResponse {
  active_count: number
  excluded_count: number
}

// 标签及其照片数量
export interface TagInfo {
  tag: string
  count: number
}

// 标签列表响应（含总数）
export interface TagsResponse {
  items: TagInfo[]
  total: number
}
