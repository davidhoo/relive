import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { UserInfo, UserInfoResponse } from '@/types/user'
import { authApi } from '@/api/auth'

const TOKEN_KEY = 'relive_token'
const USER_INFO_KEY = 'relive_user_info'
const IS_FIRST_LOGIN_KEY = 'relive_is_first_login'

// 从 localStorage 恢复用户信息
const restoreUserInfo = (): UserInfo | null => {
  const stored = localStorage.getItem(USER_INFO_KEY)
  if (stored) {
    try {
      return JSON.parse(stored)
    } catch {
      return null
    }
  }
  return null
}

const restoreIsFirstLogin = (): boolean => {
  return localStorage.getItem(IS_FIRST_LOGIN_KEY) === 'true'
}

export const useUserStore = defineStore('user', () => {
  // State
  const token = ref<string | null>(localStorage.getItem(TOKEN_KEY))
  const userInfo = ref<UserInfo | null>(restoreUserInfo())
  const isFirstLogin = ref(restoreIsFirstLogin())
  const loading = ref(false)

  // Getters
  const isLoggedIn = computed(() => !!token.value)
  const username = computed(() => userInfo.value?.username || '')

  // Actions
  const setToken = (newToken: string | null) => {
    token.value = newToken
    if (newToken) {
      localStorage.setItem(TOKEN_KEY, newToken)
    } else {
      localStorage.removeItem(TOKEN_KEY)
    }
  }

  const setUserInfo = (info: UserInfo | null) => {
    userInfo.value = info
    if (info) {
      localStorage.setItem(USER_INFO_KEY, JSON.stringify(info))
    } else {
      localStorage.removeItem(USER_INFO_KEY)
    }
  }

  const setIsFirstLogin = (value: boolean) => {
    isFirstLogin.value = value
    if (value) {
      localStorage.setItem(IS_FIRST_LOGIN_KEY, 'true')
    } else {
      localStorage.removeItem(IS_FIRST_LOGIN_KEY)
    }
  }

  const login = async (username: string, Password: string) => {
    loading.value = true
    try {
      const response = await authApi.login({ username, Password })
      setToken(response.token)
      setUserInfo(response.user)
      setIsFirstLogin(response.is_first_login)
      return response
    } finally {
      loading.value = false
    }
  }

  const logout = async () => {
    try {
      await authApi.logout()
    } catch (error) {
      // 忽略错误
    } finally {
      setToken(null)
      setUserInfo(null)
      setIsFirstLogin(false)
    }
  }

  const fetchUserInfo = async () => {
    if (!token.value) return null
    try {
      const data = await authApi.getCurrentUser()
      setUserInfo({
        id: data.id,
        username: data.username
      })
      setIsFirstLogin(data.is_first_login)
      return data
    } catch (error) {
      // Token 可能已过期，清除登录状态
      setToken(null)
      setUserInfo(null)
      setIsFirstLogin(false)
      return null
    }
  }

  const changePassword = async (old_Password: string, new_Password: string, new_username?: string) => {
    const response = await authApi.changePassword({ old_Password, new_Password, new_username })
    setIsFirstLogin(false)
    // 如果修改了用户名，更新本地存储的用户信息
    if (new_username && userInfo.value) {
      setUserInfo({ ...userInfo.value, username: new_username })
    }
    return response
  }

  const clearUserState = () => {
    setToken(null)
    setUserInfo(null)
    setIsFirstLogin(false)
  }

  return {
    token,
    userInfo,
    isFirstLogin,
    loading,
    isLoggedIn,
    username,
    setToken,
    login,
    logout,
    fetchUserInfo,
    changePassword,
    clearUserState
  }
})
