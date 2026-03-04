import http from '@/utils/request'
import type { ApiResponse } from '@/types/api'

export interface ScanPathConfig {
  id: string
  name: string
  path: string
  is_default: boolean
  enabled: boolean
  created_at: string
  last_scanned_at?: string
}

export interface ScanPathsConfig {
  paths: ScanPathConfig[]
}

// Geocode provider configuration
export interface GeocodeConfig {
  provider: string          // Current active provider: offline / amap / nominatim
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

export const configApi = {
  // Get scan paths configuration
  getScanPaths: async (): Promise<ScanPathsConfig> => {
    try {
      const response = await http.get<ApiResponse<BackendConfigResponse>>('/config/photos.scan_paths')
      // response.data is ApiResponse, response.data.data is BackendConfigResponse
      if (response.data?.data?.value) {
        return JSON.parse(response.data.data.value)
      }
      return { paths: [] }
    } catch (error) {
      // Config doesn't exist yet
      return { paths: [] }
    }
  },

  // Update scan paths configuration
  updateScanPaths: async (config: ScanPathsConfig): Promise<void> => {
    const value = JSON.stringify(config)
    await http.put('/config/photos.scan_paths', { value })
  },

  // Delete scan path and associated data
  deleteScanPath: async (pathId: string): Promise<{ success: boolean; message: string }> => {
    const response = await http.delete<ApiResponse<{ success: boolean; message: string }>>(`/config/scan-paths/${pathId}`)
    return response.data?.data || { success: false, message: 'Unknown error' }
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
        offline_max_distance: 100
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
        offline_max_distance: 100
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

// ==================== API Key interfaces ====================

export interface APIKey {
  id: number
  created_at: string
  updated_at: string
  name: string
  description: string
  is_active: boolean
  expires_at?: string
  last_used_at?: string
  use_count: number
}

export interface APIKeyWithKey extends APIKey {
  key: string
}

export interface CreateAPIKeyRequest {
  name: string
  description?: string
  expires_at?: string
}

export interface UpdateAPIKeyRequest {
  name?: string
  description?: string
  is_active?: boolean
  expires_at?: string | null
}

// API Key management functions
export const getAPIKeys = async (): Promise<APIKey[]> => {
  const response = await http.get<ApiResponse<APIKey[]>>('/config/api-keys')
  return response.data?.data || []
}

export const createAPIKey = async (req: CreateAPIKeyRequest): Promise<APIKeyWithKey> => {
  const response = await http.post<ApiResponse<APIKeyWithKey>>('/config/api-keys', req)
  if (!response.data?.data) {
    throw new Error('Failed to create API key')
  }
  return response.data.data
}

export const updateAPIKey = async (id: number, req: UpdateAPIKeyRequest): Promise<APIKey> => {
  const response = await http.put<ApiResponse<APIKey>>(`/config/api-keys/${id}`, req)
  if (!response.data?.data) {
    throw new Error('Failed to update API key')
  }
  return response.data.data
}

export const deleteAPIKey = async (id: number): Promise<void> => {
  await http.delete(`/config/api-keys/${id}`)
}

export const regenerateAPIKey = async (id: number): Promise<{ id: number; key: string }> => {
  const response = await http.post<ApiResponse<{ id: number; key: string }>>(`/config/api-keys/${id}/regenerate`)
  if (!response.data?.data) {
    throw new Error('Failed to regenerate API key')
  }
  return response.data.data
}
