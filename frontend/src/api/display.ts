import http from '@/utils/request'
import type { ApiResponse } from '@/types/api'
import type { Photo } from '@/types/photo'

export interface RenderProfileOption {
  name: string
  display_name: string
  width: number
  height: number
  palette: string
  dither_mode: string
  canvas_template: string
  default_for_device: boolean
}

export interface DailyDisplayAsset {
  id: number
  render_profile: string
  dither_preview_url?: string
  bin_url?: string
  header_url?: string
  checksum: string
  file_size: number
}

export interface DailyDisplayItem {
  id: number
  sequence: number
  photo_id: number
  preview_url: string
  curation_channel?: string
  photo?: Photo
  assets: DailyDisplayAsset[]
}

export interface DailyDisplayBatch {
  id: number
  batch_date: string
  status: string
  item_count: number
  canvas_template: string
  strategy_snapshot: string
  error_message?: string
  generated_at?: string
  updated_at: string
  items: DailyDisplayItem[]
}

export interface DailyDisplayBatchListResponse {
  items: DailyDisplayBatch[]
}

export interface GenerateDailyBatchRequest {
  date?: string
  force?: boolean
}

export const dailyDisplayApi = {
  getTodayBatch: async (date?: string): Promise<DailyDisplayBatch | null> => {
    try {
      const response = await http.get<ApiResponse<DailyDisplayBatch>>('/display/batch', { params: date ? { date } : undefined })
      return response.data?.data || null
    } catch {
      return null
    }
  },

  listHistory: async (limit = 15): Promise<DailyDisplayBatch[]> => {
    const response = await http.get<ApiResponse<DailyDisplayBatchListResponse>>('/display/history', { params: { limit } })
    return response.data?.data?.items || []
  },

  generateBatch: async (payload: GenerateDailyBatchRequest): Promise<DailyDisplayBatch> => {
    const response = await http.post<ApiResponse<DailyDisplayBatch>>('/display/batch/generate', payload)
    if (!response.data?.data) {
      throw new Error('批次生成失败')
    }
    return response.data.data
  },

  startGenerateBatch: async (payload: GenerateDailyBatchRequest): Promise<DailyDisplayBatch> => {
    const response = await http.post<ApiResponse<DailyDisplayBatch>>('/display/batch/generate/async', payload)
    if (!response.data?.data) {
      throw new Error('批次生成启动失败')
    }
    return response.data.data
  },

  getRenderProfiles: async (): Promise<RenderProfileOption[]> => {
    const response = await http.get<ApiResponse<RenderProfileOption[]>>('/display/render-profiles')
    return response.data?.data || []
  }
}
