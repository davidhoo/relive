import http from '@/utils/request'
import type { AIAnalyzeProgress, AIAnalyzeBatchResponse, AIAnalyzeTask, AIProviderInfo } from '@/types/ai'
import type { ApiResponse } from '@/types/api'

export const aiApi = {
  // 分析单张照片
  analyze(photoId: number) {
    return http.post<ApiResponse<void>>('/ai/analyze', { photo_id: photoId })
  },

  // 批量分析
  analyzeBatch(limit: number = 100) {
    return http.post<ApiResponse<AIAnalyzeBatchResponse>>('/ai/analyze/batch', { limit })
  },

  // 获取分析进度
  getProgress() {
    return http.get<ApiResponse<AIAnalyzeProgress>>('/ai/progress')
  },

  // 重新分析
  reAnalyze(id: number) {
    return http.post<ApiResponse<void>>(`/ai/reanalyze/${id}`)
  },

  // 获取 Provider 信息
  getProviderInfo() {
    return http.get<ApiResponse<AIProviderInfo>>('/ai/provider')
  },

  // 获取任务状态
  getTaskStatus() {
    return http.get<ApiResponse<AIAnalyzeTask>>('/ai/task')
  },
}
