import http from '@/utils/request'
import type { ApiResponse } from '@/types/api'
import type { PagedResponse } from '@/types/api'
import type { Photo } from '@/types/photo'

export interface ScanPathConfig {
  id: string
  name: string
  path: string
  is_default: boolean
  enabled: boolean
  auto_scan_enabled: boolean
  created_at: string
  last_scanned_at?: string
}

export interface ScanPathsConfig {
  paths: ScanPathConfig[]
}

export interface AutoScanConfig {
  enabled: boolean
  interval_minutes: number
}

// Geocode provider configuration
export interface GeocodeConfig {
  provider: string          // Current active provider: offline / amap / nominatim / weibo
  fallback: string          // Fallback provider
  cache_enabled: boolean    // Enable caching
  cache_ttl: number        // Cache TTL in seconds

  // AMap configuration
  amap_api_key: string
  amap_timeout: number

  // Nominatim configuration
  nominatim_endpoint: string
  nominatim_timeout: number

  // Offline configuration
  offline_max_distance: number

  // Weibo configuration
  weibo_api_key: string
  weibo_timeout: number
}

// AI Provider configuration
export interface AIConfig {
  provider: string          // Current active provider: ollama / qwen / openai / vllm / hybrid
  temperature: number       // Temperature parameter (0.0-1.0)
  timeout: number           // Timeout in seconds

  // Ollama configuration
  ollama_endpoint: string
  ollama_model: string
  ollama_temperature: number
  ollama_timeout: number

  // Qwen configuration
  qwen_api_key: string
  qwen_endpoint: string
  qwen_model: string
  qwen_temperature: number
  qwen_timeout: number

  // OpenAI configuration
  openai_api_key: string
  openai_endpoint: string
  openai_model: string
  openai_temperature: number
  openai_max_tokens: number
  openai_timeout: number

  // VLLM configuration
  vllm_endpoint: string
  vllm_model: string
  vllm_temperature: number
  vllm_max_tokens: number
  vllm_timeout: number
  vllm_concurrency: number
  vllm_enable_thinking: boolean

  // Hybrid configuration
  hybrid_primary: string
  hybrid_fallback: string
  hybrid_retry_on_error: boolean
}

// Define the backend config response type
interface BackendConfigResponse {
  id: number
  created_at: string
  updated_at: string
  key: string
  value: string
}

export interface DisplayPreviewResponse {
  algorithm: string
  count: number
  previewDate?: string
  photos: Photo[]
}

interface DisplayPreviewRequest extends DisplayStrategyConfig {
  previewDate?: string
}

const shufflePhotos = (photos: Photo[]) => {
  const result = [...photos]
  for (let i = result.length - 1; i > 0; i -= 1) {
    const j = Math.floor(Math.random() * (i + 1))
    const current = result[i]
    result[i] = result[j] as Photo
    result[j] = current as Photo
  }
  return result
}

const getMonthDayKey = (date: Date) => {
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${month}-${day}`
}

const resolvePreviewDate = (previewDate?: string) => {
  if (!previewDate) return new Date()

  const resolved = new Date(`${previewDate}T00:00:00`)
  if (Number.isNaN(resolved.getTime())) {
    return new Date()
  }

  return resolved
}

const isAboveThreshold = (photo: Photo, config: DisplayStrategyConfig) => {
  const beautyScore = photo.beauty_score ?? 0
  const memoryScore = photo.memory_score ?? 0
  return photo.ai_analyzed && beautyScore >= config.minBeautyScore && memoryScore >= config.minMemoryScore
}

const pickTopMemoryPhoto = (photos: Photo[]) => {
  if (photos.length === 0) return []

  const best = [...photos].sort((a, b) => {
    const memoryDiff = (b.memory_score ?? 0) - (a.memory_score ?? 0)
    if (memoryDiff !== 0) return memoryDiff

    const overallDiff = (b.overall_score ?? 0) - (a.overall_score ?? 0)
    if (overallDiff !== 0) return overallDiff

    return new Date(b.taken_at || 0).getTime() - new Date(a.taken_at || 0).getTime()
  })[0]

  return best ? [best] : []
}

const previewTopPhotos = (photos: Photo[], limit: number) => {
  return [...photos]
    .sort((a, b) => {
      const overallDiff = (b.overall_score ?? 0) - (a.overall_score ?? 0)
      if (overallDiff !== 0) return overallDiff

      const memoryDiff = (b.memory_score ?? 0) - (a.memory_score ?? 0)
      if (memoryDiff !== 0) return memoryDiff

      return new Date(b.taken_at || 0).getTime() - new Date(a.taken_at || 0).getTime()
    })
    .slice(0, limit)
}

const previewSmartFallbackByPhotoList = (
  items: Photo[],
  config: DisplayStrategyConfig,
  previewDate?: string
): DisplayPreviewResponse => {
  const byMonthDay = new Map<string, Photo[]>()
  const analyzedPhotos = items.filter((photo) => photo.ai_analyzed)

  for (const photo of analyzedPhotos) {
    if (!photo.taken_at || !isAboveThreshold(photo, config)) continue

    const monthDay = getMonthDayKey(new Date(photo.taken_at))
    const existing = byMonthDay.get(monthDay) || []
    existing.push(photo)
    byMonthDay.set(monthDay, existing)
  }

  const targetDate = resolvePreviewDate(previewDate)
  for (let offset = 0; offset <= 365; offset += 1) {
    const target = new Date(targetDate)
    target.setDate(targetDate.getDate() - offset)

    const candidates = byMonthDay.get(getMonthDayKey(target)) || []
    if (candidates.length > 0) {
      const ranked = previewTopPhotos(candidates, candidates.length)
      const poolSize = Math.min(ranked.length, Math.max(config.dailyCount * 3, 6))
      const pool = shufflePhotos(ranked.slice(0, poolSize))
      const selected = previewTopPhotos(pool.slice(0, config.dailyCount), config.dailyCount)
      return {
        algorithm: 'on_this_day',
        count: selected.length,
        previewDate,
        photos: selected,
      }
    }
  }

  return {
    algorithm: 'on_this_day',
    count: 0,
    previewDate,
    photos: [],
  }
}

const previewRandomByPhotoList = (
  items: Photo[],
  config: DisplayStrategyConfig,
  previewDate?: string
): DisplayPreviewResponse => {
  const filtered = items.filter((photo) => isAboveThreshold(photo, config))
  const selected = shufflePhotos(filtered).slice(0, config.dailyCount)

  return {
    algorithm: config.algorithm,
    count: selected.length,
    previewDate,
    photos: selected,
  }
}

const previewOnThisDayByPhotoList = (
  items: Photo[],
  config: DisplayStrategyConfig,
  previewDate?: string
): DisplayPreviewResponse => {
  const targetDate = resolvePreviewDate(previewDate)
  const analyzedPhotos = items.filter((photo) => photo.ai_analyzed)
  const thresholdPhotos = analyzedPhotos.filter((photo) => isAboveThreshold(photo, config))
  const thresholdPhotosWithDate = thresholdPhotos.filter((photo) => photo.taken_at)
  const fallbackWindows = [3, 7, 30, 365]

  for (const windowDays of fallbackWindows) {
    for (let yearOffset = 1; yearOffset <= 100; yearOffset += 1) {
      const start = new Date(targetDate)
      start.setFullYear(start.getFullYear() - yearOffset)
      start.setDate(start.getDate() - windowDays)

      const end = new Date(targetDate)
      end.setFullYear(end.getFullYear() - yearOffset)
      end.setDate(end.getDate() + windowDays)

      const candidates = thresholdPhotosWithDate.filter((photo) => {
        const takenAt = new Date(photo.taken_at as string)
        return takenAt >= start && takenAt <= end
      })

      if (candidates.length > 0) {
        const selected = previewTopPhotos(candidates, config.dailyCount)
        return {
          algorithm: 'on_this_day',
          count: selected.length,
          previewDate,
          photos: selected,
        }
      }
    }
  }

  const smartFallback = previewSmartFallbackByPhotoList(items, config, previewDate)
  if (smartFallback.photos.length > 0) {
    return smartFallback
  }

  const fallback = previewTopPhotos(thresholdPhotos, config.dailyCount)
  if (fallback.length > 0) {
    return {
      algorithm: 'on_this_day',
      count: fallback.length,
      previewDate,
      photos: fallback,
    }
  }

  const unrestrictedFallback = previewTopPhotos(analyzedPhotos, config.dailyCount)
  return {
    algorithm: 'on_this_day',
    count: unrestrictedFallback.length,
    previewDate,
    photos: unrestrictedFallback,
  }
}

const fetchAllPhotosForDisplayPreview = async (): Promise<Photo[]> => {
  const pageSize = 100
  const items: Photo[] = []
  let page = 1
  let total = 0

  do {
    const response = await http.get<ApiResponse<PagedResponse<Photo>>>('/photos', {
      params: {
        page,
        page_size: pageSize,
        analyzed: true,
        sort_by: 'taken_at',
        sort_desc: true,
      }
    })

    const data = response.data?.data
    const pageItems = data?.items || []
    total = data?.total || 0
    items.push(...pageItems)
    page += 1
  } while (items.length < total)

  return items
}

const previewByPhotoListFallback = async (
  config: DisplayStrategyConfig,
  previewDate?: string
): Promise<DisplayPreviewResponse> => {
  const items = await fetchAllPhotosForDisplayPreview()

  switch (config.algorithm) {
    case 'random':
      return previewRandomByPhotoList(items, config, previewDate)
    case 'on_this_day':
    case 'smart':
      return previewOnThisDayByPhotoList(items, config, previewDate)
    default:
      return {
        algorithm: config.algorithm,
        count: 0,
        previewDate,
        photos: [],
      }
  }
}

export const configApi = {
  // Get scan paths configuration
  getScanPaths: async (): Promise<ScanPathsConfig> => {
    try {
      const response = await http.get<ApiResponse<BackendConfigResponse>>('/config/photos.scan_paths')
      if (response.data?.data?.value) {
        const parsed = JSON.parse(response.data.data.value) as ScanPathsConfig
        return {
          paths: (parsed.paths || []).map((path) => ({
            ...path,
            auto_scan_enabled: path.auto_scan_enabled ?? true,
          })),
        }
      }
      return { paths: [] }
    } catch (error) {
      return { paths: [] }
    }
  },

  getDefaultAutoScanConfig: (): AutoScanConfig => ({
    enabled: false,
    interval_minutes: 60,
  }),

  getAutoScanConfig: async (): Promise<AutoScanConfig> => {
    try {
      const response = await http.get<ApiResponse<BackendConfigResponse>>('/config/photos.auto_scan')
      if (response.data?.data?.value) {
        const parsed = JSON.parse(response.data.data.value)
        return {
          ...configApi.getDefaultAutoScanConfig(),
          ...parsed,
        }
      }
      return configApi.getDefaultAutoScanConfig()
    } catch (error) {
      return configApi.getDefaultAutoScanConfig()
    }
  },

  updateAutoScanConfig: async (config: AutoScanConfig): Promise<void> => {
    const value = JSON.stringify(config)
    await http.put('/config/photos.auto_scan', { value })
  },

  // Update scan paths configuration
  updateScanPaths: async (config: ScanPathsConfig): Promise<void> => {
    const value = JSON.stringify(config)
    await http.put('/config/photos.scan_paths', { value })
  },

  // Delete scan path and associated data
  deleteScanPath: async (pathId: string): Promise<{ success: boolean; message: string }> => {
    const response = await http.delete<ApiResponse<{ success: boolean; message: string }>>(`/config/scan-paths/${pathId}`)
    return {
      success: response.data?.success ?? false,
      message: response.data?.message || response.data?.data?.message || 'Unknown error',
    }
  },

  // Validate a scan path
  validatePath: async (path: string): Promise<{ valid: boolean; error?: string }> => {
    const response = await http.post<ApiResponse<{ valid: boolean; error?: string }>>('/photos/validate-path', { path })
    return response.data?.data || { valid: false, error: 'Unknown error' }
  },

  // List directories in a path (for path browser)
  listDirectories: async (path: string): Promise<{ entries: Array<{ name: string; path: string; is_dir: boolean }>; parent_path?: string; current_path: string }> => {
    const response = await http.post<ApiResponse<{ entries: Array<{ name: string; path: string; is_dir: boolean }>; parent_path?: string; current_path: string }>>('/photos/list-directories', { path })
    return response.data?.data || { entries: [], current_path: path }
  },

  // Get geocode configuration
  getGeocodeConfig: async (): Promise<GeocodeConfig> => {
    try {
      const response = await http.get<ApiResponse<BackendConfigResponse>>('/config/geocode')
      if (response.data?.data?.value) {
        return JSON.parse(response.data.data.value)
      }
      // Return default config
      return {
        provider: 'offline',
        fallback: 'nominatim',
        cache_enabled: true,
        cache_ttl: 86400,
        amap_api_key: '',
        amap_timeout: 10,
        nominatim_endpoint: 'https://nominatim.openstreetmap.org/reverse',
        nominatim_timeout: 10,
        offline_max_distance: 100,
        weibo_api_key: '',
        weibo_timeout: 10
      }
    } catch (error) {
      // Config doesn't exist yet, return defaults
      return {
        provider: 'offline',
        fallback: 'nominatim',
        cache_enabled: true,
        cache_ttl: 86400,
        amap_api_key: '',
        amap_timeout: 10,
        nominatim_endpoint: 'https://nominatim.openstreetmap.org/reverse',
        nominatim_timeout: 10,
        offline_max_distance: 100,
        weibo_api_key: '',
        weibo_timeout: 10
      }
    }
  },

  // Update geocode configuration
  updateGeocodeConfig: async (config: GeocodeConfig): Promise<void> => {
    const value = JSON.stringify(config)
    await http.put('/config/geocode', { value })
  },

  // Get default AI configuration
  getDefaultAIConfig: (): AIConfig => ({
    provider: '',
    temperature: 0.7,
    timeout: 60,
    ollama_endpoint: 'http://localhost:11434/api/generate',
    ollama_model: 'llava',
    ollama_temperature: 0.7,
    ollama_timeout: 60,
    qwen_api_key: '',
    qwen_endpoint: 'https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation',
    qwen_model: 'qwen-vl-max',
    qwen_temperature: 0.7,
    qwen_timeout: 60,
    openai_api_key: '',
    openai_endpoint: 'https://api.openai.com/v1/chat/completions',
    openai_model: 'gpt-4-vision-preview',
    openai_temperature: 0.7,
    openai_max_tokens: 1000,
    openai_timeout: 60,
    vllm_endpoint: 'http://localhost:8000/v1/chat/completions',
    vllm_model: '',
    vllm_temperature: 0.7,
    vllm_max_tokens: 1000,
    vllm_timeout: 60,
    vllm_concurrency: 5,
    vllm_enable_thinking: false,
    hybrid_primary: '',
    hybrid_fallback: '',
    hybrid_retry_on_error: true
  }),

  // Get AI configuration
  getAIConfig: async (): Promise<AIConfig> => {
    try {
      const response = await http.get<ApiResponse<BackendConfigResponse>>('/config/ai')
      if (response.data?.data?.value) {
        const savedConfig = JSON.parse(response.data.data.value)
        // Merge with defaults to ensure all fields exist
        return { ...configApi.getDefaultAIConfig(), ...savedConfig }
      }
      return configApi.getDefaultAIConfig()
    } catch (error) {
      return configApi.getDefaultAIConfig()
    }
  },

  // Update AI configuration
  updateAIConfig: async (config: AIConfig): Promise<void> => {
    const value = JSON.stringify(config)
    await http.put('/config/ai', { value })
  }
}

// ==================== Display Strategy interfaces ====================

export interface DisplayStrategyConfig {
  algorithm: string       // 展示策略: random / on_this_day
  minBeautyScore: number  // 最小美学评分阈值 (0-100)
  minMemoryScore: number  // 最小回忆价值评分阈值 (0-100)
  dailyCount: number      // 每日挑选数量 (1-20)
}

export const defaultDisplayStrategyConfig: DisplayStrategyConfig = {
  algorithm: 'on_this_day',
  minBeautyScore: 70,
  minMemoryScore: 60,
  dailyCount: 3
}

const normalizeDisplayStrategyConfig = (config?: Partial<DisplayStrategyConfig>): DisplayStrategyConfig => {
  const normalized: DisplayStrategyConfig = {
    ...defaultDisplayStrategyConfig,
    ...config,
  }

  if (normalized.algorithm === 'smart') {
    normalized.algorithm = 'on_this_day'
  }

  return normalized
}

// Display strategy API functions
export const displayStrategyApi = {
  // Get display strategy configuration
  getConfig: async (): Promise<DisplayStrategyConfig> => {
    try {
      const response = await http.get<ApiResponse<BackendConfigResponse>>('/config/display.strategy')
      if (response.data?.data?.value) {
        const savedConfig = JSON.parse(response.data.data.value)
        return normalizeDisplayStrategyConfig(savedConfig)
      }
      return { ...defaultDisplayStrategyConfig }
    } catch (error) {
      return { ...defaultDisplayStrategyConfig }
    }
  },

  // Update display strategy configuration
  updateConfig: async (config: DisplayStrategyConfig): Promise<void> => {
    const value = JSON.stringify(normalizeDisplayStrategyConfig(config))
    await http.put('/config/display.strategy', { value })
  },

  // Preview strategy with current form data
  previewConfig: async (
    config: DisplayStrategyConfig,
    previewDate?: string
  ): Promise<DisplayPreviewResponse> => {
    const request: DisplayPreviewRequest = {
      ...config,
      previewDate,
    }

    try {
      const response = await http.post<ApiResponse<DisplayPreviewResponse>>('/display/preview', request)
      return response.data?.data || { algorithm: config.algorithm, count: 0, previewDate, photos: [] }
    } catch (error: any) {
      if (error?.response?.status === 404) {
        return previewByPhotoListFallback(config, previewDate)
      }
      throw error
    }
  },

  // Reset to defaults
  resetConfig: async (): Promise<DisplayStrategyConfig> => {
    await http.delete('/config/display.strategy')
    return { ...defaultDisplayStrategyConfig }
  }
}

// ==================== Prompt Configuration interfaces ====================

export interface PromptConfig {
  analysis_prompt: string
  caption_prompt: string
  batch_prompt: string
}

// Default prompt configurations
export const defaultPrompts: PromptConfig = {
  analysis_prompt: `你是"个人相册照片评估助手"，擅长理解真实照片的内容，并从回忆价值和美观角度打分。
你会收到一张照片，请分析照片内容并严格按照以下 JSON 格式返回结果：

{
  "description": "详细描述照片内容（80-200字），包括人物、场景、活动、氛围等",
  "main_category": "人物",
  "tags": "标签1,标签2,标签3",
  "memory_score": 75.0,
  "beauty_score": 80.0,
  "reason": "评分理由（不超过40字）"
}

【字段说明】
1. description (string): 详细描述照片内容（80-200字），包括人物、场景、活动、氛围等
2. main_category (string): 必须从以下12个中只选其一（禁止使用英文）：
   - 人物、孩子、猫咪、家庭、旅行、风景、美食、宠物、日常、文档、杂物、其他
3. tags (string): 3-8个标签，用逗号分隔，如：旅游,美食,家人,朋友,户外,室内
4. memory_score (number): 0-100的"值得回忆度"，精确到一位小数
5. beauty_score (number): 0-100的"美观程度"，精确到一位小数
6. reason (string): 简短中文理由，解释评分原因（不超过40字）

【值得回忆度（memory_score）评分方法】
请先按照值得回忆的程度，确定照片的"得分区间"，再进行精调：

得分区间判定：
- 垃圾/随手拍/无意义记录：40.0分以下（常见为0-25；若还能勉强辨认但无故事，也不要超过39.9）
- 稍微有点可回忆价值：以65.0分为中心（大多落在58.1-70.3）
- 不错的回忆价值：以75分为中心（大多落在68.7-82.4）
- 特别精彩、强烈值得珍藏：以85分为中心（大多落在79.1-95.9）

精调加分项（可同时叠加）：
- 人物与关系：画面中含有面积较大的人脸，有人物互动，或属于合影 → 大幅提高评分
- 事件性：生日/聚会/仪式/舞台/明显事件 → 少许提高评分
- 稀缺性与不可复现：明显"这一刻很难再来一次" → 大幅提高评分
- 情绪强度：笑、哭、惊喜、拥抱、互动、氛围强 → 少许提高评分
- 优美风景：画面中含有壮丽的自然风光，或精美、有秩序感的构图 → 少许提高评分
- 旅行意义：异地、地标、旅途情景 → 少许提高评分
- 画质：画面不清晰、模糊、有残影、虚焦 → 微微降低评分

【重点照片处理】
如果画面中含有：孩子/猫咪/宠物题材，这些主题更容易产生高回忆价值，请直接以75分为中心，并大幅提高评分

【明显低价值图片处理】
以下低价值图片，必须将memory_score压低到0-25（最多不超过39）：
- 裸露、低俗、色情或违反公序良俗的图片
- 账单、收据、广告、随手拍的杂物、测试图片、屏幕截图等

【美观分（beauty_score）评分方法】
美观分只评价视觉：构图、光线、清晰度、色彩、主体突出。不要被"孩子/猫/旅行"主题绑架美观分，主题不等于好看。

【重要约束】
- 必须严格只输出 JSON，不要输出任何思考过程、解释或额外文字
- 禁止使用英文分类如 "event", "people", "landscape" 等
- 不要输出任何多余文字，不要加注释
- 不要输出 markdown 代码块标记（三个反引号+json 等）`,
  caption_prompt: `你是一位为「电子相框」撰写旁白短句的中文文案助手。
你的目标不是描述画面，而是为画面补上一点"画外之意"。

创作原则：
1. 避免使用以下词语：世界、梦、时光、岁月、温柔、治愈、刚刚好、悄悄、慢慢 等（但不是绝对禁止）
2. 严禁使用如下句式：
   - ……里……着整个世界/夏天
   - ……得像……（简单的比喻）
   - ……比……还…… / ……得比……更……
3. 只基于图片中能确定的信息进行联想，不要虚构时间、人物关系、事件背景
4. 文案应自然、有趣，带一点幽默或者诗意，但请避免煽情、鸡汤
5. 不要复述画面内容本身，而是写"看完画面后，心里多出来的一句话"
6. 可以偏向以下风格之一：
   - 日常中的微妙情绪
   - 轻微自嘲或冷幽默
   - 对时间、记忆、瞬间的含蓄感受
   - 看似平淡但有余味的一句判断
7. 避免小学生作文式的、套路式的模板化表达

格式要求：
1. 只输出一句中文短句，不要换行，不要引号，不要任何解释
2. 建议长度8-24个汉字，最多不超过30个汉字
3. 不要出现"这张照片""这一刻""那天"等指代照片本身的词

请为这张照片创作一句旁白短句：`,
  batch_prompt: `你是"个人相册照片评估助手"，擅长理解真实照片的内容，并从回忆价值和美观角度打分。
请分析上面的 %d 张照片，每张照片以 JSON 对象返回分析结果，所有结果放入一个 JSON 数组中。

每张照片的分析要求与单张分析完全一致：

1. description (string): 详细描述照片内容（80-200字），包括人物、场景、活动、氛围等
2. main_category (string): 必须从以下12个中只选其一（禁止使用英文）：
   - 人物、孩子、猫咪、家庭、旅行、风景、美食、宠物、日常、文档、杂物、其他
3. tags (string): 3-8个标签，用逗号分隔，如：旅游,美食,家人,朋友,户外,室内
4. memory_score (number): 0-100的"值得回忆度"，精确到一位小数
5. beauty_score (number): 0-100的"美观程度"，精确到一位小数
6. reason (string): 简短中文理由，解释评分原因（不超过40字）

【值得回忆度（memory_score）评分方法】
请先按照值得回忆的程度，确定照片的"得分区间"，再进行精调：

得分区间判定：
- 垃圾/随手拍/无意义记录：40.0分以下（常见为0-25；若还能勉强辨认但无故事，也不要超过39.9）
- 稍微有点可回忆价值：以65.0分为中心（大多落在58.1-70.3）
- 不错的回忆价值：以75分为中心（大多落在68.7-82.4）
- 特别精彩、强烈值得珍藏：以85分为中心（大多落在79.1-95.9）

精调加分项（可同时叠加）：
- 人物与关系：画面中含有面积较大的人脸，有人物互动，或属于合影 → 大幅提高评分
- 事件性：生日/聚会/仪式/舞台/明显事件 → 少许提高评分
- 稀缺性与不可复现：明显"这一刻很难再来一次" → 大幅提高评分
- 情绪强度：笑、哭、惊喜、拥抱、互动、氛围强 → 少许提高评分
- 优美风景：画面中含有壮丽的自然风光，或精美、有秩序感的构图 → 少许提高评分
- 旅行意义：异地、地标、旅途情景 → 少许提高评分
- 画质：画面不清晰、模糊、有残影、虚焦 → 微微降低评分

【重点照片处理】
如果画面中含有：孩子/猫咪/宠物题材，这些主题更容易产生高回忆价值，请直接以75分为中心，并大幅提高评分

【明显低价值图片处理】
以下低价值图片，必须将memory_score压低到0-25（最多不超过39）：
- 裸露、低俗、色情或违反公序良俗的图片
- 账单、收据、广告、随手拍的杂物、测试图片、屏幕截图等

【美观分（beauty_score）评分方法】
美观分只评价视觉：构图、光线、清晰度、色彩、主体突出。不要被"孩子/猫/旅行"主题绑架美观分，主题不等于好看。

【重要约束】
- 必须严格只输出 JSON 数组，不要输出任何思考过程、解释或额外文字
- 禁止使用英文分类如 "event", "people", "landscape" 等
- 不要输出任何多余文字，不要加注释
- 不要输出 markdown 代码块标记（三个反引号+json 等）

返回格式示例：
[
  {
    "description": "详细描述照片内容...",
    "main_category": "人物",
    "tags": "旅游,美食,家人",
    "memory_score": 75.0,
    "beauty_score": 80.0,
    "reason": "评分理由（不超过40字）"
  }
]`
}

// Prompt configuration API functions
export const promptApi = {
  // Get prompt configuration
  getPromptConfig: async (): Promise<PromptConfig> => {
    try {
      const response = await http.get<ApiResponse<PromptConfig>>('/config/prompts')
      if (response.data?.data) {
        return response.data.data
      }
      return defaultPrompts
    } catch (error) {
      return defaultPrompts
    }
  },

  // Update prompt configuration
  updatePromptConfig: async (config: PromptConfig): Promise<void> => {
    await http.put('/config/prompts', config)
  },

  // Reset prompt configuration to defaults
  resetPromptConfig: async (): Promise<PromptConfig> => {
    const response = await http.post<ApiResponse<PromptConfig>>('/config/prompts/reset')
    return response.data?.data || defaultPrompts
  }
}

// ==================== Cities Data interfaces ====================

export interface CitiesDataStatus {
  exists: boolean
  file_path: string
  file_size?: number
  city_count?: number
  download_url: string
}

// Cities data management functions
export const getCitiesDataStatus = async (): Promise<CitiesDataStatus> => {
  const response = await http.get<ApiResponse<CitiesDataStatus>>('/config/cities-data/status')
  return response.data?.data || { exists: false, file_path: '', download_url: '' }
}

export const downloadCitiesData = async (): Promise<{ message: string }> => {
  const response = await http.post<ApiResponse<{ message: string }>>('/config/cities-data/download')
  if (!response.data?.success) {
    throw new Error(response.data?.message || 'Failed to download cities data')
  }
  return response.data.data || { message: 'Download started' }
}
