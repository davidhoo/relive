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
  }
}
