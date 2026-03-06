<template>
  <div class="display-page">
    <el-card shadow="never">
      <template #header>
        <span><el-icon><View /></el-icon> 展示策略</span>
      </template>

      <el-alert
        title="展示策略说明"
        type="info"
        :closable="false"
        style="margin-bottom: 20px"
      >
        <p>根据不同算法，设备会从照片库中挑选当天最适合展示的照片。下方日历可直接预览指定日期的展示结果。</p>
      </el-alert>

      <el-form :model="form" label-width="150px" style="max-width: 800px">
        <el-form-item label="展示策略">
          <el-select v-model="form.algorithm" placeholder="请选择策略" style="width: 100%">
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
            style="width: 200px"
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

        <el-form-item>
          <el-button type="primary" @click="handleSave" :loading="saving">
            保存配置
          </el-button>
          <el-button @click="handleReset">重置</el-button>
          <el-button @click="handlePreview" :loading="previewLoading">
            刷新预览
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <div class="preview-layout">
      <el-card shadow="never" class="calendar-card">
        <template #header>
          <div class="preview-header">
            <span><el-icon><Calendar /></el-icon> 日期预览</span>
          </div>
        </template>

        <div class="calendar-summary">
          <div class="calendar-date">{{ previewDateLabel }}</div>
          <div class="calendar-hint">{{ previewHint }}</div>
        </div>

        <el-calendar ref="previewCalendarRef" v-model="previewCalendarDate" class="preview-calendar">
          <template #header>
            <div class="calendar-nav">
              <el-button text @click="selectCalendarDate('prev-month')">Previous Month</el-button>
              <el-button text @click="selectCalendarDate('today')">Today</el-button>
              <el-button text @click="selectCalendarDate('next-month')">Next Month</el-button>
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
          <div class="preview-header">
            <span><el-icon><Picture /></el-icon> 策略预览</span>
            <div class="preview-tags">
              <el-tag type="info">{{ previewDateLabel }}</el-tag>
              <el-tag type="success">已找到 {{ previewPhotos.length }} / {{ form.dailyCount }} 张</el-tag>
            </div>
          </div>
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
                :src="getPhotoThumbnailUrl(photo.id)"
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

    <el-dialog
      v-model="framePreviewVisible"
      title="相框预览"
      width="min(680px, calc(100vw - 24px))"
      align-center
      destroy-on-close
      @closed="resetFramePreview"
    >
      <div v-if="framePreviewPhoto" class="frame-preview-body">
        <div class="frame-preview-frame">
          <div class="frame-preview-stage">
            <el-image
              class="frame-preview-image"
              :src="getPhotoFramePreviewUrl(framePreviewPhoto.id)"
              :alt="framePreviewPhoto.caption || getFileName(framePreviewPhoto.file_path)"
              fit="cover"
            />
            <div class="frame-preview-info">
              <div class="frame-preview-title">
                {{ framePreviewPhoto.caption || getFileName(framePreviewPhoto.file_path) }}
              </div>
              <div class="frame-preview-subtitle">
                {{ formatPhotoDate(framePreviewPhoto.taken_at) || '未知时间' }}
                <span v-if="framePreviewPhoto.location"> · {{ framePreviewPhoto.location }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { displayStrategyApi, defaultDisplayStrategyConfig } from '@/api/config'
import type { DisplayPreviewResponse, DisplayStrategyConfig } from '@/api/config'
import type { Photo } from '@/types/photo'
import { useUserStore } from '@/stores/user'

const userStore = useUserStore()

type CalendarControlAction = 'prev-month' | 'today' | 'next-month'

const form = ref<DisplayStrategyConfig>({ ...defaultDisplayStrategyConfig })
const previewCalendarRef = ref<{ selectDate: (action: CalendarControlAction) => void } | null>(null)
const previewCalendarDate = ref(new Date())

const saving = ref(false)
const loading = ref(false)
const previewLoading = ref(false)
const previewResult = ref<DisplayPreviewResponse | null>(null)
const framePreviewVisible = ref(false)
const framePreviewPhoto = ref<Photo | null>(null)
let previewTimer: number | undefined

const previewSupported = computed(() => supportedAlgorithms.includes(form.value.algorithm))
const supportedAlgorithms = ['random', 'on_this_day']
const previewPhotos = computed<Photo[]>(() => previewResult.value?.photos || [])
const previewDateValue = computed(() => toPreviewDateValue(previewCalendarDate.value))
const previewDateLabel = computed(() => formatDisplayDate(previewCalendarDate.value))
const previewHint = computed(() => {
  switch (form.value.algorithm) {
    case 'random':
      return '随机策略不依赖日期，日历用于固定一个预览时点；每次刷新仍可能选到不同照片。'
    case 'on_this_day':
      return '点击任意日期后，会优先寻找往年同日附近的照片；若没有，则自动回溯到最接近该日期的历史记忆。'
    default:
      return '点击日历中的任意日期，可预览该日期的展示结果。'
  }
})
const emptyPreviewText = computed(() => {
  if (form.value.algorithm === 'on_this_day') {
    return '该日期附近及其智能兜底范围内没有找到可展示的照片'
  }
  return '没有找到符合当前策略条件的照片'
})

const getPhotoAssetUrl = (photoId: number, asset: 'thumbnail' | 'frame-preview') => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const token = userStore.token
  return `${baseUrl}/photos/${photoId}/${asset}${token ? `?token=${token}` : ''}`
}

const getPhotoThumbnailUrl = (photoId: number) => getPhotoAssetUrl(photoId, 'thumbnail')

const getPhotoFramePreviewUrl = (photoId: number) => getPhotoAssetUrl(photoId, 'frame-preview')

const getFileName = (filePath: string) => filePath.split('/').pop() || filePath

const openFramePreview = (photo: Photo) => {
  framePreviewPhoto.value = photo
  framePreviewVisible.value = true
}

const resetFramePreview = () => {
  framePreviewPhoto.value = null
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

onMounted(() => {
  loadConfig()
})

onUnmounted(() => {
  if (previewTimer && typeof window !== 'undefined') {
    window.clearTimeout(previewTimer)
  }
})
</script>

<style scoped>
.display-page {
  padding: 20px;
  display: grid;
  gap: 20px;
}

.help-text {
  margin-left: 10px;
  color: #909399;
  font-size: 12px;
}

.preview-layout {
  display: grid;
  grid-template-columns: minmax(320px, 380px) minmax(0, 1fr);
  gap: 20px;
  align-items: start;
}

.calendar-card,
.preview-card {
  min-height: 240px;
}

.preview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.preview-tags {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.calendar-summary {
  margin-bottom: 16px;
}

.calendar-date {
  font-size: 18px;
  font-weight: 600;
  color: #303133;
}

.calendar-hint {
  margin-top: 8px;
  font-size: 13px;
  line-height: 1.6;
  color: #606266;
}

.preview-calendar :deep(.el-calendar__header) {
  padding-left: 0;
  padding-right: 0;
}

.calendar-nav {
  width: 100%;
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
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

.preview-calendar :deep(.el-calendar-day) {
  height: auto;
  padding: 0;
}

.calendar-cell {
  width: 100%;
  aspect-ratio: 1 / 1;
  box-sizing: border-box;
  padding: 8px;
  border-radius: 12px;
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
  font-size: 14px;
  font-weight: 600;
}

.preview-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
}

.preview-item {
  width: min(100%, 480px);
  flex: 0 1 480px;
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
  aspect-ratio: 4 / 3;
  object-fit: cover;
  display: block;
  background: #f5f7fa;
}

.preview-meta {
  padding: 12px;
}

.preview-title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
  line-height: 1.5;
}

.preview-subtitle,
.preview-score {
  margin-top: 6px;
  font-size: 12px;
  color: #909399;
}

.frame-preview-body {
  --frame-display-width: min(480px, calc(100vw - 168px));
  --frame-shell-padding: clamp(18px, 2.8vw, 26px);
  display: flex;
  justify-content: center;
}

.frame-preview-frame {
  position: relative;
  width: calc(var(--frame-display-width) + (var(--frame-shell-padding) * 2));
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
  width: var(--frame-display-width);
  aspect-ratio: 3 / 5;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 14px;
  border-radius: 24px;
  background: linear-gradient(180deg, #fbfaf6 0%, #f1eadf 100%);
  box-shadow:
    inset 0 0 0 1px rgba(180, 157, 126, 0.28),
    inset 0 16px 24px rgba(255, 255, 255, 0.82),
    0 18px 34px rgba(15, 23, 42, 0.08);
}

.frame-preview-image {
  width: 100%;
  aspect-ratio: 3 / 4;
  flex: 0 0 auto;
  border-radius: 14px 14px 0 0;
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
  font-size: 18px;
  font-weight: 600;
  color: #303133;
  text-align: center;
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.frame-preview-subtitle {
  font-size: 14px;
  color: #909399;
  text-align: center;
  line-height: 1.5;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 960px) {
  .preview-layout {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .frame-preview-body {
    --frame-display-width: min(480px, calc(100vw - 112px));
    --frame-shell-padding: 16px;
  }
}
</style>
