import http from '@/utils/request'
import type { LoginRequest, LoginResponse, ChangePasswordRequest, UserInfoResponse } from '@/types/user'

export interface ApiResponse<T> {
  success: boolean
  data: T
  message?: string
  error?: {
    code: string
    message: string
  }
}

export const authApi = {
  // 登录
  login: async (data: LoginRequest): Promise<LoginResponse> => {
    const response = await http.post<ApiResponse<LoginResponse>>('/auth/login', data)
    return response.data.data
  },

  // 登出
  logout: async (): Promise<void> => {
    await http.post<ApiResponse<void>>('/auth/logout')
  },

  // 修改密码
  changePassword: async (data: ChangePasswordRequest): Promise<void> => {
    await http.post<ApiResponse<void>>('/auth/change-Password', data)
  },

  // 获取当前用户信息
  getCurrentUser: async (): Promise<UserInfoResponse> => {
    const response = await http.get<ApiResponse<UserInfoResponse>>('/auth/user')
    return response.data.data
  }
}
