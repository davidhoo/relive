export interface GeocodeTask {
  status: string
  current_photo_id?: number
  processed_jobs: number
  started_at?: string
  stopped_at?: string
}

export interface GeocodeStats {
  total: number
  pending: number
  queued: number
  processing: number
  completed: number
  failed: number
  cancelled: number
}

export interface GeocodeBackgroundLogsResponse {
  lines: string[]
}
