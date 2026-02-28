import axios from 'axios'
import type { AxiosInstance, AxiosRequestConfig, AxiosResponse } from 'axios'
import { ElMessage } from 'element-plus'
import type { ApiResponse } from '@/types/api'

// 创建 axios 实例
const request: AxiosInstance = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// 请求拦截器
request.interceptors.request.use(
  (config) => {
    // 可以在这里添加 token
    // const token = localStorage.getItem('token')
    // if (token) {
    //   config.headers.Authorization = `Bearer ${token}`
    // }
    return config
  },
  (error) => {
    console.error('Request error:', error)
    return Promise.reject(error)
  }
)

// 响应拦截器
request.interceptors.response.use(
  (response: AxiosResponse<ApiResponse>) => {
    const res = response.data

    // 如果响应成功
    if (res.success) {
      return response
    }

    // 处理业务错误
    ElMessage.error(res.error?.message || res.message || '请求失败')
    return Promise.reject(new Error(res.error?.message || '请求失败'))
  },
  (error) => {
    console.error('Response error:', error)

    // 处理 HTTP 错误
    if (error.response) {
      const status = error.response.status
      const data = error.response.data

      // 对于 503 Service Unavailable，检查是否是 AI 服务未配置
      // 这是预期的情况，不需要显示错误提示
      if (status === 503 && data?.error?.code === 'SERVICE_UNAVAILABLE') {
        console.log('Service unavailable (expected):', data.message)
        return Promise.reject(error)
      }

      switch (status) {
        case 400:
          ElMessage.error('请求参数错误')
          break
        case 401:
          ElMessage.error('未授权，请登录')
          break
        case 403:
          ElMessage.error('拒绝访问')
          break
        case 404:
          ElMessage.error('请求资源不存在')
          break
        case 500:
          ElMessage.error('服务器错误')
          break
        case 503:
          ElMessage.error('服务暂时不可用')
          break
        default:
          ElMessage.error(`请求失败: ${status}`)
      }
    } else if (error.request) {
      ElMessage.error('网络错误，请检查网络连接')
    } else {
      ElMessage.error('请求配置错误')
    }

    return Promise.reject(error)
  }
)

// 封装请求方法
export const http = {
  get<T = any>(url: string, config?: AxiosRequestConfig): Promise<ApiResponse<T>> {
    return request.get(url, config).then((res) => res.data)
  },

  post<T = any>(url: string, data?: any, config?: AxiosRequestConfig): Promise<ApiResponse<T>> {
    return request.post(url, data, config).then((res) => res.data)
  },

  put<T = any>(url: string, data?: any, config?: AxiosRequestConfig): Promise<ApiResponse<T>> {
    return request.put(url, data, config).then((res) => res.data)
  },

  delete<T = any>(url: string, config?: AxiosRequestConfig): Promise<ApiResponse<T>> {
    return request.delete(url, config).then((res) => res.data)
  },
}

export default request
