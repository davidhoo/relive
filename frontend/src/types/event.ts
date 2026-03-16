// 事件模型
export interface Event {
  id: number
  start_time: string
  end_time: string
  duration_hours: number
  photo_count: number

  // 位置信息
  gps_latitude?: number
  gps_longitude?: number
  location: string

  // 画像
  cover_photo_id?: number
  primary_category: string
  primary_tag: string

  // 展示权重
  event_score: number
  display_count: number
  last_displayed_at?: string

  created_at: string
  updated_at: string
}
