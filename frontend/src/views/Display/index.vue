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
        <p>根据不同的算法策略，设备（电子相框/手机等）将从照片库中选择合适的照片进行展示。</p>
      </el-alert>

      <el-form :model="form" label-width="150px" style="max-width: 800px">
        <el-form-item label="展示策略">
          <el-select v-model="form.algorithm" placeholder="请选择策略" style="width: 100%">
            <el-option label="随机选择" value="random" />
            <el-option label="智能推荐" value="smart" />
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

    <el-card shadow="never" class="preview-card">
      <template #header>
        <div class="preview-header">
          <span><el-icon><Picture /></el-icon> 策略预览</span>
          <el-tag type="info">已找到 {{ previewPhotos.length }} / {{ form.dailyCount }} 张</el-tag>
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
        description="没有找到符合当前阈值条件的照片"
      />

      <div v-else class="preview-grid">
        <div
          v-for="photo in previewPhotos"
          :key="photo.id"
          class="preview-item"
        >
          <img
            class="preview-image"
            :src="getPhotoThumbnailUrl(photo.id)"
            :alt="photo.caption || getFileName(photo.file_path)"
          />
          <div class="preview-meta">
            <div class="preview-title">
              {{ photo.caption || getFileName(photo.file_path) }}
            </div>
            <div class="preview-subtitle">
              {{ formatDate(photo.taken_at) || '未知时间' }}
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
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { displayStrategyApi, defaultDisplayStrategyConfig } from '@/api/config'
import type { DisplayStrategyConfig } from '@/api/config'
import type { Photo } from '@/types/photo'
import { useUserStore } from '@/stores/user'

const userStore = useUserStore()

const form = ref<DisplayStrategyConfig>({ ...defaultDisplayStrategyConfig })

const saving = ref(false)
const loading = ref(false)
const previewLoading = ref(false)
const previewPhotos = ref<Photo[]>([])
let previewTimer: number | undefined

const previewSupported = computed(() => supportedAlgorithms.includes(form.value.algorithm))
const supportedAlgorithms = ['random', 'smart']

const getPhotoThumbnailUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const token = userStore.token
  return `${baseUrl}/photos/${photoId}/thumbnail${token ? `?token=${token}` : ''}`
}

const getFileName = (filePath: string) => filePath.split('/').pop() || filePath

const formatDate = (dateStr?: string) => {
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
    previewPhotos.value = []
    return
  }

  previewLoading.value = true
  try {
    const response = await displayStrategyApi.previewConfig(form.value)
    previewPhotos.value = response.photos || []
  } catch (error: any) {
    previewPhotos.value = []
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

.preview-card {
  min-height: 240px;
}

.preview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
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
</style>
