import { defineStore } from 'pinia'
import { ref } from 'vue'
import { systemApi } from '@/api/system'
import type { SystemStats, SystemHealth } from '@/types/system'

export const useSystemStore = defineStore('system', () => {
  const stats = ref<SystemStats | null>(null)
  const health = ref<SystemHealth | null>(null)
  const loading = ref(false)

  // 获取系统统计
  const fetchStats = async () => {
    loading.value = true
    try {
      const res = await systemApi.getStats()
      stats.value = res.data?.data || null
    } catch (error) {
      console.error('Failed to fetch system stats:', error)
      stats.value = null
    } finally {
      loading.value = false
    }
  }

  // 获取系统健康状态
  const fetchHealth = async () => {
    try {
      const res = await systemApi.getHealth()
      health.value = res.data?.data || null
    } catch (error) {
      console.error('Failed to fetch system health:', error)
      health.value = null
    }
  }

  return {
    stats,
    health,
    loading,
    fetchStats,
    fetchHealth,
  }
})
