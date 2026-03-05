// 用户信息
export interface UserInfo {
  id: number
  username: string
}

// 登录请求
export interface LoginRequest {
  username: string
  Password: string
}

// 登录响应
export interface LoginResponse {
  token: string
  expires_at: string
  user: UserInfo
  is_first_login: boolean
}

// 修改密码请求
export interface ChangePasswordRequest {
  old_Password: string
  new_Password: string
  new_username?: string  // 可选：同时修改用户名
}

// 用户信息响应
export interface UserInfoResponse {
  id: number
  username: string
  is_first_login: boolean
}
