import { http } from '@/utils/request'
import type { AIAnalyzeProgress, AIAnalyzeBatchResponse, AIAnalyzeTask, AIProviderInfo } from '@/types/ai'

export const aiApi = {
  // 分析单张照片
  analyze(photoId: number) {
    return http.post('/ai/analyze', { photo_id: photoId })
  },

  // 批量分析
  analyzeBatch(limit: number = 100) {
    return http.post<AIAnalyzeBatchResponse>('/ai/analyze/batch', { limit })
  },

  // 获取分析进度
  getProgress() {
    return http.get<AIAnalyzeProgress>('/ai/progress')
  },

  // 重新分析
  reAnalyze(id: number) {
    return http.post(`/ai/reanalyze/${id}`)
  },

  // 获取 Provider 信息
  getProviderInfo() {
    return http.get<AIProviderInfo>('/ai/provider')
  },

  // 获取任务状态
  getTaskStatus() {
    return http.get<AIAnalyzeTask>('/ai/task')
  },
}
