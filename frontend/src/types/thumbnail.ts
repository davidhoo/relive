export interface ThumbnailTask {
  status: string
  current_photo_id?: number
  current_file?: string
  processed_jobs: number
  started_at?: string
  stopped_at?: string
}

export interface ThumbnailStats {
  total: number
  pending: number
  queued: number
  processing: number
  completed: number
  failed: number
  cancelled: number
}

export interface ThumbnailBackgroundLogsResponse {
  lines: string[]
}
