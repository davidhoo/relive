import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { UserInfo, UserInfoResponse } from '@/types/user'
import { authApi } from '@/api/auth'

const TOKEN_KEY = 'relive_token'

export const useUserStore = defineStore('user', () => {
  // State
  const token = ref<string | null>(localStorage.getItem(TOKEN_KEY))
  const userInfo = ref<UserInfo | null>(null)
  const isFirstLogin = ref(false)
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

  const login = async (username: string, Password: string) => {
    loading.value = true
    try {
      const response = await authApi.login({ username, Password })
      setToken(response.token)
      userInfo.value = response.user
      isFirstLogin.value = response.is_first_login
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
      userInfo.value = null
      isFirstLogin.value = false
    }
  }

  const fetchUserInfo = async () => {
    if (!token.value) return null
    try {
      const data = await authApi.getCurrentUser()
      userInfo.value = {
        id: data.id,
        username: data.username
      }
      isFirstLogin.value = data.is_first_login
      return data
    } catch (error) {
      // Token 可能已过期，清除登录状态
      setToken(null)
      return null
    }
  }

  const changePassword = async (old_Password: string, new_Password: string) => {
    await authApi.changePassword({ old_Password, new_Password })
    isFirstLogin.value = false
  }

  const clearUserState = () => {
    setToken(null)
    userInfo.value = null
    isFirstLogin.value = false
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
