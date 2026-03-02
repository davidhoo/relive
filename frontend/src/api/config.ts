import { http } from '@/utils/request'

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
      const response = await http.get<BackendConfigResponse>('/config/photos.scan_paths')
      // http.get returns ApiResponse, response.data is the BackendConfigResponse
      if (response.data && response.data.value) {
        return JSON.parse(response.data.value)
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

  // Validate a scan path
  validatePath: async (path: string): Promise<{ valid: boolean; error?: string }> => {
    const response = await http.post<{ valid: boolean; error?: string }>('/photos/validate-path', { path })
    return response.data || { valid: false, error: 'Unknown error' }
  },

  // Get geocode configuration
  getGeocodeConfig: async (): Promise<GeocodeConfig> => {
    try {
      const response = await http.get<BackendConfigResponse>('/config/geocode')
      if (response.data && response.data.value) {
        return JSON.parse(response.data.value)
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
    hybrid_primary: '',
    hybrid_fallback: '',
    hybrid_retry_on_error: true
  }),

  // Get AI configuration
  getAIConfig: async (): Promise<AIConfig> => {
    try {
      const response = await http.get<BackendConfigResponse>('/config/ai')
      if (response.data && response.data.value) {
        const savedConfig = JSON.parse(response.data.value)
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
