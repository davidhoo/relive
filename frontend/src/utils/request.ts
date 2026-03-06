import axios, { AxiosError, type AxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'
import { useUserStore } from '@/stores/user'

// 创建 axios 实例
const http = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// 请求拦截器 - 添加 Token
http.interceptors.request.use(
  (config) => {
    const userStore = useUserStore()
    if (userStore.token) {
      config.headers.Authorization = `Bearer ${userStore.token}`
    }
    return config
  },
  (error) => {
    return Promise.reject(error)
  }
)

// 响应拦截器
http.interceptors.response.use(
  (response) => {
    return response
  },
  (error: AxiosError) => {
    if (error.response) {
      const status = error.response.status
      const data = error.response.data as any

      // 处理 401 未授权
      if (status === 401) {
        const userStore = useUserStore()
        userStore.clearUserState()
        ElMessage.error('登录已过期，请重新登录')
        window.location.href = '/login'
        return Promise.reject(error)
      }

      // 处理 403 首次登录需要修改密码
      if (status === 403 && data?.error?.code === 'FIRST_LOGIN_REQUIRED') {
        window.location.href = '/change-Password'
        return Promise.reject(error)
      }

      // 显示错误消息（排除某些特定的错误）
      // 404 错误对于配置类接口是预期的（如 geocode 配置不存在）
      const isConfigNotFound = status === 404 &&
        (error.config?.url?.includes('/config/') || data?.error?.code === 'CONFIG_NOT_FOUND')
      const isDisplayPreviewFallback = status === 404 &&
        error.config?.url?.includes('/display/preview')

      if (!isConfigNotFound && !isDisplayPreviewFallback) {
        const message = data?.error?.message || data?.message || `请求失败 (${status})`
        ElMessage.error(message)
      }
    } else if (error.request) {
      ElMessage.error('网络错误，请检查后端服务是否正常运行')
    } else {
      ElMessage.error('请求配置错误')
    }

    return Promise.reject(error)
  }
)

// 封装 GET 请求
export const get = <T>(url: string, config?: AxiosRequestConfig) => {
  return http.get<T>(url, config)
}

// 封装 POST 请求
export const post = <T>(url: string, data?: unknown, config?: AxiosRequestConfig) => {
  return http.post<T>(url, data, config)
}

// 封装 PUT 请求
export const put = <T>(url: string, data?: unknown, config?: AxiosRequestConfig) => {
  return http.put<T>(url, data, config)
}

// 封装 DELETE 请求
export const del = <T>(url: string, config?: AxiosRequestConfig) => {
  return http.delete<T>(url, config)
}

export default http
