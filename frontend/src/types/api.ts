// API 通用响应类型
export interface ApiResponse<T = any> {
  success: boolean
  data?: T
  error?: {
    code: string
    message: string
  }
  message?: string
}

// 分页响应类型
export interface PagedResponse<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

// 游标分页响应类型（人物详情页照片/人脸 cursor 模式）。
// 不返回 total/page，has_more=false 表示已到末尾，next_cursor 为空表示无下一页。
export interface CursorPagedResponse<T> {
  items: T[]
  has_more: boolean
  next_cursor?: string
}

// 分页请求参数
export interface PageParams {
  page?: number
  page_size?: number
}
