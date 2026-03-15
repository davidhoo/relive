<template>
  <div class="display-page">
    <PageHeader title="展示策略" subtitle="配置每日展示批次、渲染规格与设备展示内容" :gradient="true">
      <template #actions>
        <el-button type="primary" @click="handleSave" :loading="saving">
          保存配置
        </el-button>
        <el-button @click="handleReset">重置</el-button>
        <el-button @click="handlePreview" :loading="previewLoading">
          刷新预览
        </el-button>
      </template>
    </PageHeader>

    <el-card shadow="never">
      <template #header>
        <SectionHeader :icon="View" title="展示策略" />
      </template>

      <el-form :model="form" label-width="150px" class="display-form">
        <el-form-item label="展示策略">
          <el-select v-model="form.algorithm" placeholder="请选择策略" class="full-width">
            <el-option label="随机选择" value="random" />
            <el-option label="往年今日" value="on_this_day" />
          </el-select>
        </el-form-item>

        <el-form-item label="每日挑选数量">
          <el-input-number
            v-model="form.dailyCount"
            :min="1"
            :max="20"
            :step="1"
            class="input-number-width-lg"
          />
          <span class="help-text">每天为设备挑选展示的照片数量</span>
        </el-form-item>

        <el-form-item label="美学评分阈值">
          <el-slider
            v-model="form.minBeautyScore"
            :min="0"
            :max="100"
            :step="5"
            show-stops
            show-input
          />
        </el-form-item>

        <el-form-item label="回忆价值阈值">
          <el-slider
            v-model="form.minMemoryScore"
            :min="0"
            :max="100"
            :step="5"
            show-stops
            show-input
          />
        </el-form-item>

      </el-form>

      <div class="inline-note display-note">
        根据不同算法，设备会从照片库中挑选当天最适合展示的照片。下方日历可直接预览指定日期的展示结果。
      </div>
    </el-card>

    <div class="preview-layout">
      <el-card shadow="never" class="calendar-card">
        <template #header>
          <SectionHeader :icon="Calendar" title="日期预览">
            <template #actions>
              <el-tag type="info" effect="plain">{{ previewDateLabel }}</el-tag>
            </template>
          </SectionHeader>
        </template>

        <el-calendar ref="previewCalendarRef" v-model="previewCalendarDate" class="preview-calendar">
          <template #header>
            <div class="calendar-nav">
              <el-button text @click="selectCalendarDate('prev-month')">上月</el-button>
              <el-button text @click="selectCalendarDate('today')">今天</el-button>
              <el-button text @click="selectCalendarDate('next-month')">下月</el-button>
            </div>
          </template>
          <template #date-cell="{ data }">
            <div
              class="calendar-cell"
              :class="{ 'is-preview-date': data.day === previewDateValue }"
            >
              <span class="calendar-day">{{ getCalendarDay(data.day) }}</span>
            </div>
          </template>
        </el-calendar>
      </el-card>

      <el-card shadow="never" class="preview-card">
        <template #header>
          <SectionHeader :icon="Picture" title="策略预览">
            <template #actions>
              <div class="preview-tags">
                <el-tag type="success">已找到 {{ previewPhotos.length }} / {{ form.dailyCount }} 张</el-tag>
              </div>
            </template>
          </SectionHeader>
        </template>

        <el-alert
          v-if="!previewSupported"
          title="当前策略暂不支持预览。"
          type="warning"
          :closable="false"
        />

        <el-skeleton v-else-if="previewLoading" :rows="4" animated />

        <el-empty
          v-else-if="previewPhotos.length === 0"
          :description="emptyPreviewText"
        />

        <div v-else class="preview-grid">
          <div
            v-for="photo in previewPhotos"
            :key="photo.id"
            class="preview-item"
          >
            <button
              type="button"
              class="preview-image-trigger"
              @click="openFramePreview(photo)"
            >
              <img
                class="preview-image"
                :src="getPhotoFramePreviewUrl(photo.id, photo.updated_at)"
                :alt="photo.caption || getFileName(photo.file_path)"
              />
            </button>
            <div class="preview-meta">
              <div class="preview-title">
                {{ photo.caption || getFileName(photo.file_path) }}
              </div>
              <div class="preview-subtitle">
                {{ formatPhotoDate(photo.taken_at) || '未知时间' }}
                <span v-if="photo.location"> · {{ photo.location }}</span>
              </div>
              <div class="preview-score">
                回忆 {{ photo.memory_score ?? 0 }} / 美观 {{ photo.beauty_score ?? 0 }}
              </div>
            </div>
          </div>
        </div>
      </el-card>
    </div>


    <el-card shadow="never" class="daily-batch-card">
      <template #header>
        <SectionHeader :icon="Files" title="今日批次">
          <template #actions>
            <div class="header-actions-row">
              <el-button @click="loadDailyBatch" :loading="batchLoading">刷新</el-button>
              <el-button type="primary" @click="handleGenerateDailyBatch" :loading="batchGenerating">{{ dailyBatch ? '重新生成并覆盖' : '生成今日批次' }}</el-button>
            </div>
          </template>
        </SectionHeader>
      </template>

      <el-skeleton v-if="batchLoading" :rows="4" animated />
      <el-empty v-else-if="!dailyBatch" description="今日批次尚未生成" />
      <template v-else>
        <div class="batch-summary">
          <el-tag type="info">{{ dailyBatch.batch_date }}</el-tag>
          <el-tag type="success">{{ dailyBatch.item_count }} 张</el-tag>
          <el-tag>{{ dailyBatch.canvas_template }}</el-tag>
        </div>
        <div class="batch-grid">
          <div v-for="item in dailyBatch.items" :key="item.id" class="batch-item">
            <button type="button" class="batch-preview-trigger" @click="openDitherPreview(item)"><img class="batch-preview" :src="resolveProtectedUrl(item.preview_url)" :alt="item.photo?.caption || getFileName(item.photo?.file_path || '')"></button>
            <div class="batch-item-meta">
              <div class="batch-item-title">#{{ item.sequence }} {{ item.photo?.caption || getFileName(item.photo?.file_path || '') }}</div>
              <div class="batch-item-subtitle">{{ formatPhotoDate(item.photo?.taken_at) || '未知时间' }}<span v-if="item.photo?.location"> · {{ item.photo.location }}</span></div>
              <div class="batch-asset-tags">
                <el-tag v-for="asset in item.assets" :key="asset.id" size="small">{{ asset.render_profile }}</el-tag>
              </div>
            </div>
          </div>
        </div>
      </template>
    </el-card>

    <el-card shadow="never" class="history-card">
      <template #header>
        <SectionHeader :icon="Clock" title="历史批次">
          <template #actions>
            <el-button @click="loadBatchHistory" :loading="historyLoading">刷新历史</el-button>
          </template>
        </SectionHeader>
      </template>

      <el-skeleton v-if="historyLoading" :rows="4" animated />
      <el-empty v-else-if="batchHistory.length === 0" description="暂无历史批次" />
      <div v-else class="history-list">
        <el-collapse>
          <el-collapse-item v-for="batch in batchHistory" :key="batch.id" :name="batch.batch_date">
            <template #title>
              <div class="history-title">
                <span>{{ batch.batch_date }}</span>
                <span class="history-title-meta">{{ batch.item_count }} 张 · {{ batch.status }}</span>
              </div>
            </template>
            <div class="batch-grid compact">
              <div v-for="item in batch.items" :key="item.id" class="batch-item compact">
                <button type="button" class="batch-preview-trigger" @click="openDitherPreview(item)"><img class="batch-preview" :src="resolveProtectedUrl(item.preview_url)" :alt="item.photo?.caption || getFileName(item.photo?.file_path || '')"></button>
                <div class="batch-item-meta">
                  <div class="batch-item-title">#{{ item.sequence }} {{ item.photo?.caption || getFileName(item.photo?.file_path || '') }}</div>
                  <div class="batch-asset-links">
                    <a v-for="asset in item.assets" :key="asset.id" :href="resolveProtectedUrl(asset.bin_url || '')" target="_blank" rel="noreferrer">{{ asset.render_profile }}</a>
                  </div>
                </div>
              </div>
            </div>
          </el-collapse-item>
        </el-collapse>
      </div>
    </el-card>
    <el-dialog
      v-model="previewVisible"
      class="preview-dialog"
      width="min(760px, calc(100vw - 24px))"
      align-center
      :show-close="false"
      destroy-on-close
      @closed="resetPreview"
    >
      <div v-if="previewPhoto || previewAsset" class="unified-preview-body">
        <div class="preview-titlebar">
          <div class="unified-preview-toolbar" v-if="previewPhoto && availablePreviewProfiles.length">
            <button
              type="button"
              class="preview-mode-btn"
              :class="{ active: previewMode === 'frame' }"
              @click="previewMode = 'frame'"
            >
              全彩
            </button>
            <button
              v-for="profile in availablePreviewProfiles"
              :key="profile.key"
              type="button"
              class="preview-mode-btn"
              :class="{ active: previewMode === 'device' && previewProfileName === profile.profileName }"
              @click="selectPreviewProfile(profile.profileName, profile.assetId)"
            >
              {{ profile.label }}
            </button>
          </div>
        </div>

        <div class="frame-preview-body">
          <div class="frame-preview-frame">
            <div v-if="previewMode === 'device' && (previewAsset || previewProfileName)" class="dither-preview-stage">
              <div class="dither-preview-canvas">
                <el-image
                  class="dither-preview-image"
                  :src="previewDeviceImageUrl"
                  :alt="previewPhoto?.caption || getFileName(previewPhoto?.file_path || '')"
                  fit="fill"
                />
              </div>
            </div>
            <div v-else-if="previewPhoto" class="frame-preview-stage">
              <div class="frame-preview-canvas">
                <el-image
                  class="frame-preview-image"
                  :src="getPhotoFramePreviewUrl(previewPhoto.id, previewPhoto.updated_at)"
                  :alt="getDisplayTitle(previewPhoto)"
                  fit="cover"
                />
                <div class="frame-preview-info">
                  <div class="frame-preview-title">
                    {{ getDisplayTitle(previewPhoto) }}
                  </div>
                  <div class="frame-preview-subtitle">
                    {{ getDisplaySubtitle(previewPhoto) }}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div v-if="previewMode === 'device' && (currentPreviewBinUrl || currentPreviewHeaderUrl)" class="preview-downloads">
          <a
            v-if="currentPreviewBinUrl"
            class="preview-download-link"
            :href="currentPreviewBinUrl"
            target="_blank"
            rel="noreferrer"
            download
          >
            下载 bin
          </a>
          <a
            v-if="currentPreviewHeaderUrl"
            class="preview-download-link"
            :href="currentPreviewHeaderUrl"
            target="_blank"
            rel="noreferrer"
            download
          >
            下载 header
          </a>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { Calendar, Clock, Files, Picture, View } from '@element-plus/icons-vue'
import { displayStrategyApi, defaultDisplayStrategyConfig } from '@/api/config'
import type { DisplayPreviewResponse, DisplayStrategyConfig } from '@/api/config'
import { dailyDisplayApi, type DailyDisplayBatch, type RenderProfileOption } from '@/api/display'
import type { Photo } from '@/types/photo'


type CalendarControlAction = 'prev-month' | 'today' | 'next-month'

const form = ref<DisplayStrategyConfig>({ ...defaultDisplayStrategyConfig })
const previewCalendarRef = ref<{ selectDate: (action: CalendarControlAction) => void } | null>(null)
const previewCalendarDate = ref(new Date())

const saving = ref(false)
const loading = ref(false)
const previewLoading = ref(false)
const previewResult = ref<DisplayPreviewResponse | null>(null)
const dailyBatch = ref<DailyDisplayBatch | null>(null)
const batchHistory = ref<DailyDisplayBatch[]>([])
const batchLoading = ref(false)
const historyLoading = ref(false)
const batchGenerating = ref(false)
const previewVisible = ref(false)
const previewPhoto = ref<Photo | null>(null)
const previewAssets = ref<DailyDisplayBatch['items'][number]['assets']>([])
const previewAsset = ref<DailyDisplayBatch['items'][number]['assets'][number] | null>(null)
const previewMode = ref<'frame' | 'device'>('frame')
const renderProfiles = ref<RenderProfileOption[]>([])
const previewProfileName = ref<string | null>(null)
let previewTimer: number | undefined

const previewSupported = computed(() => supportedAlgorithms.includes(form.value.algorithm))
const supportedAlgorithms = ['random', 'on_this_day']
const previewPhotos = computed<Photo[]>(() => previewResult.value?.photos || [])
const previewDateValue = computed(() => toPreviewDateValue(previewCalendarDate.value))
const previewDateLabel = computed(() => formatDisplayDate(previewCalendarDate.value))
const previewHint = computed(() => {
  switch (form.value.algorithm) {
    case 'random':
      return '随机策略会按当前参数抽取一组照片。'
    case 'on_this_day':
      return '优先匹配往年同日附近的历史照片。'
    default:
      return '选择日期后查看该天的策略结果。'
  }
})
const emptyPreviewText = computed(() => {
  if (form.value.algorithm === 'on_this_day') {
    return '该日期附近及其智能兜底范围内没有找到可展示的照片'
  }
  return '没有找到符合当前策略条件的照片'
})
const availablePreviewProfiles = computed(() => {
  if (previewAssets.value.length > 0) {
    return previewAssets.value.map((asset) => ({
      key: `asset-${asset.id}`,
      label: asset.render_profile,
      profileName: asset.render_profile,
      assetId: asset.id,
    }))
  }
  return renderProfiles.value
    .filter((profile) => profile.width === 480 && profile.height === 800)
    .map((profile) => ({
      key: profile.name,
      label: profile.name,
      profileName: profile.name,
      assetId: undefined,
    }))
})

const previewDeviceImageUrl = computed(() => {
  if (previewAsset.value?.dither_preview_url) {
    return resolveProtectedUrl(previewAsset.value.dither_preview_url)
  }
  if (!previewPhoto.value || !previewProfileName.value) return ''
  return getPhotoDevicePreviewUrl(previewPhoto.value.id, previewProfileName.value, previewPhoto.value.updated_at)
})

const currentPreviewBinUrl = computed(() => {
  if (!previewAsset.value?.bin_url) return ''
  return resolveProtectedUrl(previewAsset.value.bin_url)
})

const currentPreviewHeaderUrl = computed(() => {
  if (!previewAsset.value?.header_url) return ''
  return resolveProtectedUrl(previewAsset.value.header_url)
})


const getPhotoAssetUrl = (photoId: number, asset: 'thumbnail' | 'frame-preview', version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const params = new URLSearchParams()
  if (version) params.set('v', version)
  const query = params.toString()
  return `${baseUrl}/photos/${photoId}/${asset}${query ? `?${query}` : ''}`
}

const getPhotoThumbnailUrl = (photoId: number, version?: string) => getPhotoAssetUrl(photoId, 'thumbnail', version)

const getPhotoFramePreviewUrl = (photoId: number, version?: string) => getPhotoAssetUrl(photoId, 'frame-preview', version)
const getPhotoDevicePreviewUrl = (photoId: number, profileName: string, version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const params = new URLSearchParams()
  params.set('profile', profileName)
  if (version) params.set('v', version)
  return `${baseUrl}/photos/${photoId}/device-preview?${params.toString()}`
}


const getFileName = (filePath: string) => filePath.split('/').pop() || filePath

const getDisplayTitle = (photo?: Photo | null) => {
  if (!photo) return ''
  const caption = photo.caption?.trim()
  if (caption) return caption
  const fileName = getFileName(photo.file_path || '')
  return fileName.replace(/\.[^.]+$/, '')
}

const getDisplaySubtitle = (photo?: Photo | null) => {
  if (!photo) return ''
  const parts: string[] = []
  const date = formatPhotoDate(photo.taken_at)
  if (date) parts.push(date.replace(/\//g, '.'))
  if (photo.location) parts.push(photo.location)
  return parts.join(' · ')
}

const resolveProtectedUrl = (path: string) => {
  if (!path) return ''
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const normalized = path.startsWith('/api/v1') ? path : `/api/v1${path.startsWith('/') ? path : `/${path}`}`
  return `${baseUrl.replace(/\/api\/v1$/, '')}${normalized}`
}

const openDitherPreview = (item: DailyDisplayBatch['items'][number]) => {
  previewPhoto.value = item.photo || null
  previewAssets.value = item.assets || []
  previewAsset.value = item.assets[0] || null
  previewProfileName.value = previewAsset.value?.render_profile || null
  previewMode.value = previewAsset.value ? 'device' : 'frame'
  previewVisible.value = true
}

const selectPreviewProfile = (profileName: string, assetId?: number) => {
  previewProfileName.value = profileName
  previewAsset.value = assetId ? previewAssets.value.find((asset) => asset.id === assetId) || null : null
  previewMode.value = 'device'
}

const openFramePreview = (photo: Photo) => {
  previewPhoto.value = photo
  previewAssets.value = []
  previewAsset.value = null
  previewProfileName.value = null
  previewMode.value = 'frame'
  previewVisible.value = true
}

const resetPreview = () => {
  previewPhoto.value = null
  previewAssets.value = []
  previewAsset.value = null
  previewProfileName.value = null
  previewMode.value = 'frame'
}

const toPreviewDateValue = (date: Date) => {
  const resolved = new Date(date)
  if (Number.isNaN(resolved.getTime())) {
    return toPreviewDateValue(new Date())
  }

  const year = resolved.getFullYear()
  const month = String(resolved.getMonth() + 1).padStart(2, '0')
  const day = String(resolved.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const formatDisplayDate = (date: Date | string) => {
  const resolved = new Date(date)
  if (Number.isNaN(resolved.getTime())) {
    return '预览日期未知'
  }

  return resolved.toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    weekday: 'long',
  })
}

const formatPhotoDate = (dateStr?: string) => {
  if (!dateStr) return ''
  try {
    return new Date(dateStr).toLocaleDateString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    })
  } catch {
    return ''
  }
}

const getCalendarDay = (day: string) => Number(day.split('-')[2] || 0)

const selectCalendarDate = (action: CalendarControlAction) => {
  previewCalendarRef.value?.selectDate(action)
}

const loadDailyBatch = async () => {
  batchLoading.value = true
  try {
    dailyBatch.value = await dailyDisplayApi.getTodayBatch()
  } finally {
    batchLoading.value = false
  }
}

const loadBatchHistory = async () => {
  historyLoading.value = true
  try {
    batchHistory.value = await dailyDisplayApi.listHistory(10)
  } catch (error: any) {
    ElMessage.error(error.message || '加载历史批次失败')
  } finally {
    historyLoading.value = false
  }
}

const generateDailyBatch = async (force: boolean) => {
  batchGenerating.value = true
  try {
    await dailyDisplayApi.startGenerateBatch({ force })
    // poll until ready or failed (max 4 minutes)
    for (let i = 0; i < 120; i++) {
      await new Promise(resolve => setTimeout(resolve, 2000))
      const batch = await dailyDisplayApi.getTodayBatch()
      if (batch?.status === 'ready') {
        ElMessage.success(force ? '今日批次已重新生成' : '今日批次生成成功')
        await loadDailyBatch()
        await loadBatchHistory()
        return
      }
      if (batch?.status === 'failed') {
        throw new Error(batch.error_message || '批次生成失败')
      }
    }
    throw new Error('批次生成超时，请检查服务器日志')
  } catch (error: any) {
    ElMessage.error(error.message || '生成今日批次失败')
  } finally {
    batchGenerating.value = false
  }
}

const handleGenerateDailyBatch = async () => {
  await generateDailyBatch(!!dailyBatch.value)
}

// 从 API 加载配置
const loadConfig = async () => {
  loading.value = true
  try {
    const config = await displayStrategyApi.getConfig()
    form.value = { ...defaultDisplayStrategyConfig, ...config }
    if (!supportedAlgorithms.includes(form.value.algorithm)) {
      form.value.algorithm = defaultDisplayStrategyConfig.algorithm
    }
    await handlePreview()
  } catch (error: any) {
    ElMessage.error('加载配置失败：' + (error.message || '未知错误'))
  } finally {
    loading.value = false
  }
}

// 保存配置
const handleSave = async () => {
  saving.value = true
  try {
    await displayStrategyApi.updateConfig(form.value)
    await handlePreview()
    ElMessage.success('配置已保存')
  } catch (error: any) {
    ElMessage.error(error.message || '保存配置失败')
  } finally {
    saving.value = false
  }
}

// 重置配置
const handleReset = async () => {
  try {
    await ElMessageBox.confirm(
      '确定要重置为默认配置吗？',
      '确认重置',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    // 先重置表单
    form.value = { ...defaultDisplayStrategyConfig }
    // 保存重置后的配置
    try {
      await displayStrategyApi.updateConfig(form.value)
      await handlePreview()
      ElMessage.success('配置已重置为默认值')
    } catch (apiError: any) {
      ElMessage.error(apiError.message || '保存重置配置失败')
    }
  } catch (error: any) {
    // 用户取消操作，不处理
    if (error === 'cancel') return
    // 其他错误（如弹窗异常）
    console.error('Reset dialog error:', error)
  }
}

const handlePreview = async () => {
  if (!previewSupported.value) {
    previewResult.value = null
    return
  }

  previewLoading.value = true
  try {
    previewResult.value = await displayStrategyApi.previewConfig(form.value, previewDateValue.value)
  } catch (error: any) {
    previewResult.value = {
      algorithm: form.value.algorithm,
      count: 0,
      previewDate: previewDateValue.value,
      photos: [],
    }
    ElMessage.error(error.message || '加载预览失败')
  } finally {
    previewLoading.value = false
  }
}

const schedulePreview = () => {
  if (typeof window === 'undefined') return
  if (previewTimer) {
    window.clearTimeout(previewTimer)
  }
  previewTimer = window.setTimeout(() => {
    handlePreview()
  }, 250)
}

watch(
  () => [
    form.value.algorithm,
    form.value.dailyCount,
    form.value.minBeautyScore,
    form.value.minMemoryScore,
    previewDateValue.value,
  ],
  () => {
    if (loading.value) return
    schedulePreview()
  }
)

const loadRenderProfiles = async () => {
  try {
    renderProfiles.value = await dailyDisplayApi.getRenderProfiles()
  } catch (error) {
    console.error('Failed to load render profiles:', error)
  }
}

onMounted(() => {
  loadConfig()
  loadDailyBatch()
  loadBatchHistory()
  loadRenderProfiles()
})

onUnmounted(() => {
  if (previewTimer && typeof window !== 'undefined') {
    window.clearTimeout(previewTimer)
  }
})
</script>

<style scoped>
.display-page {
  padding: var(--spacing-xl);
  display: grid;
  gap: 12px;
}

.help-text {
  margin-left: 10px;
  color: #909399;
  font-size: 12px;
}

.preview-layout {
  display: grid;
  grid-template-columns: minmax(300px, 340px) minmax(0, 1fr);
  gap: 20px;
  align-items: start;
}

.calendar-card,
.preview-card {
  min-height: 220px;
}

.calendar-card :deep(.el-card__header),
.preview-card :deep(.el-card__header) {
  padding: 12px 16px 8px;
}

.calendar-card :deep(.el-card__body),
.preview-card :deep(.el-card__body) {
  padding-top: 8px;
}

.calendar-card :deep(.el-card__body) {
  padding-bottom: 4px;
}

.header-actions-row {
  display: flex;
  gap: 12px;
}

.preview-tags {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.calendar-summary {
  margin-bottom: 10px;
}

.calendar-date {
  font-size: 16px;
  font-weight: 600;
  color: #303133;
}

.calendar-hint {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.4;
  color: #606266;
}

.preview-calendar :deep(.el-calendar__header) {
  padding: 0 0 10px;
}

.calendar-hint {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}


.calendar-nav {
  width: 100%;
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  min-height: 28px;
}

.calendar-nav :deep(.el-button) {
  padding-top: 0;
  padding-bottom: 0;
  min-height: 24px;
}

.calendar-nav .el-button:first-child {
  justify-self: start;
}

.calendar-nav .el-button:nth-child(2) {
  justify-self: center;
}

.calendar-nav .el-button:last-child {
  justify-self: end;
}

.preview-calendar :deep(.el-calendar-table td) {
  height: auto;
}

.preview-calendar :deep(.el-calendar-table) {
  margin-bottom: 0;
}

.preview-calendar :deep(.el-calendar__body) {
  padding-left: 0;
  padding-right: 0;
  padding-top: 10px;
  padding-bottom: 0;
}

.preview-calendar :deep(.el-calendar-table thead th) {
  padding-top: 6px;
  padding-bottom: 4px;
}

.preview-calendar :deep(.el-calendar-day) {
  height: auto;
  min-height: 0;
  padding: 0;
}

.calendar-cell {
  width: 100%;
  aspect-ratio: 1 / 1;
  box-sizing: border-box;
  padding: 4px;
  border-radius: 10px;
  display: flex;
  align-items: flex-start;
  background: transparent;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.calendar-cell.is-preview-date {
  background: #ecf5ff;
  color: #1d4ed8;
}

.calendar-day {
  font-size: 13px;
  font-weight: 600;
}

.preview-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 16px;
}

.preview-item {
  border: 1px solid #ebeef5;
  border-radius: 14px;
  overflow: hidden;
  background: #fff;
}

.preview-image-trigger {
  width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  cursor: zoom-in;
}

.preview-image {
  width: 100%;
  aspect-ratio: 3 / 5;
  object-fit: cover;
  display: block;
  background: #f5f7fa;
}

.preview-meta {
  padding: 10px;
}

.preview-title {
  font-size: 13px;
  font-weight: 600;
  color: #303133;
  line-height: 1.4;
}

.preview-subtitle,
.preview-score {
  margin-top: 4px;
  font-size: 11px;
  color: #909399;
  line-height: 1.4;
}

.preview-title {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.daily-batch-card,
.history-card {
  min-height: 220px;
}

.batch-summary {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.batch-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 16px;
}

.batch-grid.compact {
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
}

.batch-item {
  border: 1px solid #ebeef5;
  border-radius: 14px;
  overflow: hidden;
  background: #fff;
}

.batch-item.compact {
  border-radius: 12px;
}

.batch-preview-trigger {
  width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  cursor: zoom-in;
}

.batch-preview {
  width: 100%;
  aspect-ratio: 3 / 5;
  object-fit: cover;
  display: block;
  background: #f5f7fa;
}

.batch-item-meta {
  padding: 12px;
}

.batch-item-title {
  font-size: 13px;
  font-weight: 600;
  color: #303133;
  line-height: 1.5;
}

.batch-item-subtitle {
  margin-top: 6px;
  font-size: 12px;
  color: #909399;
}

.batch-asset-tags,
.batch-asset-links {
  margin-top: 8px;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.batch-asset-links a {
  color: var(--el-color-primary);
  text-decoration: none;
  font-size: 12px;
}

.history-list {
  display: grid;
  gap: 12px;
}

.history-title {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  padding-right: 8px;
}

.history-title-meta {
  font-size: 12px;
  color: #909399;
}

:deep(.preview-dialog .el-dialog) {
  padding: 0 !important;
  overflow: hidden;
}

:deep(.preview-dialog .el-dialog__header) {
  display: none !important;
  padding: 0 !important;
  margin: 0 !important;
  height: 0 !important;
  min-height: 0 !important;
}

:deep(.preview-dialog .el-dialog__body) {
  padding: 0 !important;
}

.unified-preview-body {
  --device-preview-width: min(480px, calc(100vw - 120px));
  --device-shell-padding: clamp(18px, 2.8vw, 26px);
  display: grid;
  justify-items: center;
  gap: 16px;
}

.preview-titlebar {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 12px 16px 0;
}

.unified-preview-toolbar {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 8px;
}

.preview-mode-btn {
  border: 1px solid var(--color-border);
  background: #fff;
  color: var(--color-text-secondary);
  height: 32px;
  padding: 0 14px;
  border-radius: 999px;
  font-size: 13px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.preview-mode-btn.active {
  border-color: var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  box-shadow: 0 8px 18px rgba(0, 184, 148, 0.18);
}

.dither-preview-tag {
  cursor: pointer;
}

.dither-preview-stage {
  --preview-stage-padding: 14px;
  position: relative;
  z-index: 1;
  width: calc(var(--device-preview-width) + (var(--preview-stage-padding) * 2));
  height: calc((var(--device-preview-width) * 5 / 3) + (var(--preview-stage-padding) * 2));
  padding: var(--preview-stage-padding);
  overflow: hidden;
  border-radius: 24px;
  background: linear-gradient(180deg, #fbfaf6 0%, #f1eadf 100%);
  box-shadow:
    inset 0 0 0 1px rgba(180, 157, 126, 0.28),
    inset 0 16px 24px rgba(255, 255, 255, 0.82),
    0 18px 34px rgba(15, 23, 42, 0.08);
  box-sizing: border-box;
}

.dither-preview-canvas {
  width: var(--device-preview-width);
  height: calc(var(--device-preview-width) * 5 / 3);
  overflow: hidden;
  border-radius: 14px;
  background: #f5f7fa;
}

.dither-preview-image {
  width: 100%;
  height: 100%;
  background: #f5f7fa;
}

.dither-preview-image :deep(img) {
  width: 100%;
  height: 100%;
  object-fit: fill;
}

.frame-preview-body {
  --frame-canvas-width: min(480px, calc(100vw - 168px));
  --frame-shell-padding: clamp(18px, 2.8vw, 26px);
  --preview-stage-padding: 14px;
  display: flex;
  justify-content: center;
}

.frame-preview-frame {
  position: relative;
  width: calc(var(--frame-canvas-width) + (var(--preview-stage-padding) * 2) + (var(--frame-shell-padding) * 2));
  padding: var(--frame-shell-padding);
  border-radius: 34px;
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.22) 0%, rgba(255, 255, 255, 0) 32%),
    linear-gradient(145deg, #d3a16d 0%, #b87743 24%, #8b552f 52%, #c98b58 76%, #8d5a32 100%);
  box-shadow:
    0 26px 54px rgba(52, 29, 11, 0.28),
    inset 0 1px 0 rgba(255, 244, 226, 0.5),
    inset 0 -1px 0 rgba(82, 45, 18, 0.3);
}

.frame-preview-frame::before,
.frame-preview-frame::after {
  content: '';
  position: absolute;
  pointer-events: none;
}

.frame-preview-frame::before {
  inset: 10px;
  border-radius: 26px;
  box-shadow:
    inset 0 0 0 1px rgba(255, 240, 220, 0.2),
    inset 0 12px 20px rgba(255, 230, 195, 0.24),
    inset 0 -14px 18px rgba(94, 53, 25, 0.28);
}

.frame-preview-frame::after {
  inset: calc(var(--frame-shell-padding) - 6px);
  border-radius: 24px;
  box-shadow:
    inset 0 0 0 1px rgba(111, 67, 36, 0.22),
    0 0 0 1px rgba(255, 244, 230, 0.08);
}

.frame-preview-stage {
  position: relative;
  z-index: 1;
  width: calc(var(--frame-canvas-width) + (var(--preview-stage-padding) * 2));
  height: calc((var(--frame-canvas-width) * 5 / 3) + (var(--preview-stage-padding) * 2));
  display: flex;
  overflow: hidden;
  padding: var(--preview-stage-padding);
  border-radius: 24px;
  background: linear-gradient(180deg, #fbfaf6 0%, #f1eadf 100%);
  box-shadow:
    inset 0 0 0 1px rgba(180, 157, 126, 0.28),
    inset 0 16px 24px rgba(255, 255, 255, 0.82),
    0 18px 34px rgba(15, 23, 42, 0.08);
  box-sizing: border-box;
}

.frame-preview-canvas {
  width: var(--frame-canvas-width);
  height: calc(var(--frame-canvas-width) * 5 / 3);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 14px;
  background: #f5f7fa;
}

.frame-preview-image {
  width: 100%;
  aspect-ratio: 3 / 4;
  flex: 0 0 auto;
  background: #f5f7fa;
}

.frame-preview-image :deep(img) {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.frame-preview-info {
  flex: 1;
  min-height: 0;
  display: grid;
  align-content: center;
  gap: 12px;
  padding: 20px 24px 24px;
  border-radius: 0 0 14px 14px;
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.98) 0%, #f8f5ee 100%);
  box-shadow: inset 0 1px 0 rgba(191, 172, 143, 0.22);
}

.frame-preview-title {
  font-size: 16px;
  font-weight: 600;
  color: #303133;
  text-align: center;
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  min-height: 48px;
}

.frame-preview-subtitle {
  font-size: 13px;
  color: #909399;
  text-align: center;
  line-height: 1.5;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-top: 8px;
}

.preview-downloads {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  margin-top: -4px;
  flex-wrap: wrap;
}

.preview-download-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 32px;
  padding: 0 14px;
  border: 1px solid var(--color-border);
  border-radius: 999px;
  background: #fff;
  color: var(--color-primary);
  font-size: 13px;
  font-weight: var(--font-weight-medium);
  text-decoration: none;
  transition: all var(--transition-fast);
}

.preview-download-link:hover {
  border-color: var(--color-primary);
  box-shadow: 0 8px 18px rgba(0, 184, 148, 0.12);
}

@media (max-width: 960px) {
  .preview-layout {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .unified-preview-body {
    --device-preview-width: min(480px, calc(100vw - 96px));
    --device-shell-padding: 16px;
  }

  .frame-preview-body {
    --frame-canvas-width: min(480px, calc(100vw - 112px));
    --frame-shell-padding: 16px;
  }
}
.display-note {
  margin-top: 16px;
  color: var(--color-text-secondary);
  font-size: 14px;
  line-height: 1.7;
}

.display-form {
  max-width: 800px;
}

.full-width {
  width: 100%;
}

.input-number-width-lg {
  width: 200px;
}
</style>
