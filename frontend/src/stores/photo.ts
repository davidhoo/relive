import { defineStore } from 'pinia'
import { ref } from 'vue'
import { photoApi } from '@/api/photo'
import { configApi, type ScanPathConfig, type AutoScanConfig } from '@/api/config'

const STALE_MS = 60_000

export const usePhotoStore = defineStore('photo', () => {
  const categories = ref<string[]>([])
  const categoriesLoadedAt = ref(0)

  const hotTags = ref<{ tag: string; count: number }[]>([])
  const totalTagCount = ref(0)
  const tagsLoadedAt = ref(0)

  const photoCounts = ref({ active: 0, excluded: 0 })
  const photoCountsLoadedAt = ref(0)

  const scanPaths = ref<ScanPathConfig[]>([])
  const scanPathsLoadedAt = ref(0)

  const autoScanConfig = ref<AutoScanConfig>(configApi.getDefaultAutoScanConfig())
  const autoScanConfigLoadedAt = ref(0)

  const fetchCategories = async (force = false) => {
    if (!force && Date.now() - categoriesLoadedAt.value < STALE_MS && categories.value.length > 0) return
    const res = await photoApi.getCategories()
    categories.value = res.data?.data || []
    categoriesLoadedAt.value = Date.now()
  }

  const fetchTags = async (force = false) => {
    if (!force && Date.now() - tagsLoadedAt.value < STALE_MS && hotTags.value.length > 0) return
    const res = await photoApi.getTags({ limit: 15 })
    const data = res.data?.data
    hotTags.value = data?.items || []
    totalTagCount.value = data?.total || 0
    tagsLoadedAt.value = Date.now()
  }

  const fetchPhotoCounts = async (force = false) => {
    if (!force && Date.now() - photoCountsLoadedAt.value < STALE_MS) return
    const res = await photoApi.getCounts()
    const data = res.data?.data
    photoCounts.value = {
      active: data?.active_count || 0,
      excluded: data?.excluded_count || 0,
    }
    photoCountsLoadedAt.value = Date.now()
  }

  const fetchScanPaths = async (force = false) => {
    if (!force && Date.now() - scanPathsLoadedAt.value < STALE_MS && scanPaths.value.length > 0) return
    const config = await configApi.getScanPaths()
    scanPaths.value = config.paths || []
    scanPathsLoadedAt.value = Date.now()
  }

  const fetchAutoScanConfig = async (force = false) => {
    if (!force && Date.now() - autoScanConfigLoadedAt.value < STALE_MS) return
    autoScanConfig.value = await configApi.getAutoScanConfig()
    autoScanConfigLoadedAt.value = Date.now()
  }

  const invalidateAll = () => {
    categoriesLoadedAt.value = 0
    tagsLoadedAt.value = 0
    photoCountsLoadedAt.value = 0
    scanPathsLoadedAt.value = 0
    autoScanConfigLoadedAt.value = 0
  }

  return {
    categories, hotTags, totalTagCount, photoCounts, scanPaths, autoScanConfig,
    fetchCategories, fetchTags, fetchPhotoCounts, fetchScanPaths, fetchAutoScanConfig,
    invalidateAll,
  }
})
