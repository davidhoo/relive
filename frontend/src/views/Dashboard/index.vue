<template>
  <div class="dashboard">
    <PageHeader title="仪表盘" subtitle="照片管理系统概览" :gradient="true" />

    <!-- 统计卡片 -->
    <el-row :gutter="20" class="stats-row animate-fade-in">
      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover">
          <el-statistic title="总照片数" :value="systemStats?.total_photos || 0">
            <template #prefix>
              <el-icon><Picture /></el-icon>
            </template>
            <template #suffix>
              <span class="stat-suffix">张</span>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover">
          <el-statistic title="已分析" :value="systemStats?.analyzed_photos || 0">
            <template #prefix>
              <el-icon class="success-icon"><MagicStick /></el-icon>
            </template>
            <template #suffix>
              <span class="stat-suffix success-text">{{ analysisRate }}%</span>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover">
          <el-statistic title="在线设备" :value="systemStats?.online_devices || 0">
            <template #prefix>
              <el-icon class="warning-icon"><Monitor /></el-icon>
            </template>
            <template #suffix>
              <span class="stat-suffix">/ {{ systemStats?.total_devices || 0 }}</span>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover">
          <el-statistic title="存储空间" :value="storageSize">
            <template #prefix>
              <el-icon class="info-icon"><DataLine /></el-icon>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
    </el-row>

    <!-- AI 分析进度 - 简洁卡片 -->
    <el-row :gutter="20" class="progress-row">
      <el-col :span="24">
        <el-card shadow="never" class="progress-card animate-fade-in animate-delay-1">
          <SectionHeader :icon="MagicStick" title="AI 分析进度">
            <template #actions>
              <el-button
                type="primary"
                size="default"
                @click="handleStartAnalysis"
                :disabled="analyzing"
                class="start-button"
              >
                <el-icon v-if="!analyzing"><VideoPlay /></el-icon>
                {{ analyzing ? '分析中...' : '开始批量分析' }}
              </el-button>
            </template>
          </SectionHeader>
          <div v-if="aiProgress" class="progress-content">
            <div class="modern-progress">
              <div
                class="modern-progress-bar"
                :style="{ width: progressPercentage + '%' }"
              ></div>
            </div>
            <div class="progress-info">
              <div class="progress-stat">
                <span class="progress-label">已完成</span>
                <span class="progress-value">{{ aiProgress.completed }}/{{ aiProgress.total }}</span>
              </div>
              <div class="progress-stat">
                <span class="progress-label">进度</span>
                <span class="progress-value">{{ progressPercentage }}%</span>
              </div>
              <div class="progress-stat" v-if="aiProgress.failed > 0">
                <span class="progress-label">失败</span>
                <span class="progress-value error">{{ aiProgress.failed }}</span>
              </div>
              <div class="progress-stat" v-if="aiProgress.current_photo_id">
                <span class="progress-label">当前照片</span>
                <span class="progress-value">#{{ aiProgress.current_photo_id }}</span>
              </div>
            </div>
          </div>
          <el-empty v-else description="暂无分析任务" :image-size="80" />
        </el-card>
      </el-col>
    </el-row>

    <!-- 最近照片 - 简洁卡片 -->
    <el-row :gutter="20" class="photos-row">
      <el-col :span="24">
        <el-card shadow="never" class="photos-card animate-fade-in animate-delay-2">
          <SectionHeader :icon="Picture" title="最近照片">
            <template #actions>
              <div class="photos-title-actions">
                <span class="photos-count">{{ recentPhotos.length }} 张</span>
                <el-button link @click="gotoPhotos" class="view-all-btn">
                  查看全部
                  <el-icon><ArrowRight /></el-icon>
                </el-button>
              </div>
            </template>
          </SectionHeader>
          <el-row :gutter="16" v-if="recentPhotos.length" class="photos-grid">
            <el-col
              :xs="12"
              :sm="8"
              :md="6"
              :lg="4"
              v-for="(photo, index) in recentPhotos"
              :key="photo.id"
              class="photo-col"
            >
              <div
                class="image-card animate-scale-in"
                :style="{ animationDelay: `${index * 30}ms` }"
                @click="gotoPhotoDetail(photo.id)"
              >
                <el-image
                  :src="getPhotoThumbnailUrl(photo.id, photo.updated_at)"
                  :preview-src-list="[getPhotoUrl(photo.id)]"
                  fit="cover"
                  class="image-card-image"
                  loading="lazy"
                />
                <div class="image-card-badge score-badge" v-if="photo.ai_analyzed">
                  <el-icon><Star /></el-icon>
                  {{ photo.overall_score?.toFixed(1) }}
                </div>
                <div class="image-card-badge badge-info" v-else>
                  <el-icon><QuestionFilled /></el-icon>
                  未分析
                </div>
                <div class="image-card-overlay">
                  <div class="overlay-content">
                    <div class="photo-name">{{ getFileName(photo.file_path) }}</div>
                    <div class="photo-date" v-if="photo.taken_at">
                      {{ formatDate(photo.taken_at) }}
                    </div>
                  </div>
                </div>
              </div>
            </el-col>
          </el-row>
          <el-empty v-else description="暂无照片" :image-size="100">
            <el-button type="primary" @click="handleScan">
              <el-icon><FolderOpened /></el-icon>
              扫描照片
            </el-button>
          </el-empty>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowRight, DataLine, FolderOpened, MagicStick, Monitor, Picture, QuestionFilled, Star, VideoPlay } from '@element-plus/icons-vue'
import { useSystemStore } from '@/stores/system'
import { photoApi } from '@/api/photo'
import { aiApi } from '@/api/ai'
import type { Photo } from '@/types/photo'
import type { AIAnalyzeProgress } from '@/types/ai'
import { useUserStore } from '@/stores/user'
import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'

const router = useRouter()
const systemStore = useSystemStore()
const userStore = useUserStore()

const recentPhotos = ref<Photo[]>([])
const aiProgress = ref<AIAnalyzeProgress | null>(null)
const analyzing = ref(false)

// 系统统计
const systemStats = computed(() => systemStore.stats)

// 分析率
const analysisRate = computed(() => {
  if (!systemStats.value?.total_photos) return 0
  return Math.round(
    (systemStats.value.analyzed_photos / systemStats.value.total_photos) * 100
  )
})

// 存储大小格式化
const storageSize = computed(() => {
  const size = systemStats.value?.storage_size || 0
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(2)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(2)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
})

// AI 进度百分比
const progressPercentage = computed(() => {
  if (!aiProgress.value?.total) return 0
  return Math.round((aiProgress.value.completed / aiProgress.value.total) * 100)
})

// 进度状态
const progressStatus = computed(() => {
  if (!aiProgress.value) return undefined
  if (aiProgress.value.is_running) return undefined
  if (aiProgress.value.failed > 0) return 'warning'
  return 'success'
})

// 获取照片缩略图 URL
const getPhotoThumbnailUrl = (photoId: number, version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const token = userStore.token
  const params = new URLSearchParams()
  if (token) params.set('token', token)
  if (version) params.set('v', version)
  const query = params.toString()
  return `${baseUrl}/photos/${photoId}/thumbnail${query ? `?${query}` : ''}`
}

// 获取照片原图 URL（用于预览）
const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const token = userStore.token
  return `${baseUrl}/photos/${photoId}/image${token ? `?token=${token}` : ''}`
}

// 获取文件名
const getFileName = (filePath: string) => {
  return filePath.split('/').pop() || filePath
}

// 格式化日期
const formatDate = (dateStr: string) => {
  try {
    const date = new Date(dateStr)
    return date.toLocaleDateString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit'
    })
  } catch {
    return ''
  }
}

// 加载最近照片
const loadRecentPhotos = async () => {
  try {
    const res = await photoApi.getList({ page: 1, page_size: 12 })
    recentPhotos.value = res.data?.data?.items || []
  } catch (error) {
    console.error('Failed to load recent photos:', error)
  }
}

// 加载 AI 进度
const loadAIProgress = async () => {
  try {
    const res = await aiApi.getProgress()
    aiProgress.value = res.data?.data || null
  } catch (error: any) {
    // AI 服务未配置时返回 503，这是正常情况，不需要显示错误
    if (error?.response?.status === 503) {
      console.log('AI service is not configured')
      aiProgress.value = null
    } else {
      console.error('Failed to load AI progress:', error)
    }
  }
}

// 开始批量分析
const handleStartAnalysis = async () => {
  try {
    analyzing.value = true
    await aiApi.analyzeBatch(100)
    ElMessage.success('批量分析已开始')

    // 轮询进度
    const timer = setInterval(async () => {
      await loadAIProgress()
      if (!aiProgress.value?.is_running) {
        clearInterval(timer)
        analyzing.value = false
        await systemStore.fetchStats()
        ElMessage.success('批量分析已完成')
      }
    }, 2000)
  } catch (error: any) {
    analyzing.value = false
    ElMessage.error(error.message || '启动批量分析失败')
  }
}

// 扫描照片
const handleScan = async () => {
  try {
    await photoApi.startScan()
    ElMessage.success('扫描任务已启动，正在后台处理')

    const timer = window.setInterval(async () => {
      try {
        const res = await photoApi.getScanTask()
        const { task, is_running } = res.data?.data || {}
        if (!task || !is_running) {
          clearInterval(timer)
          await loadRecentPhotos()
          await systemStore.fetchStats()
          ElMessage.success('扫描任务已完成')
        }
      } catch (error) {
        clearInterval(timer)
      }
    }, 2000)
  } catch (error: any) {
    ElMessage.error(error.message || '启动扫描任务失败')
  }
}

// 跳转到照片列表
const gotoPhotos = () => {
  router.push('/photos')
}

// 跳转到照片详情
const gotoPhotoDetail = (photoId: number) => {
  router.push(`/photos/${photoId}`)
}

onMounted(async () => {
  await systemStore.fetchStats()
  await loadRecentPhotos()
  await loadAIProgress()
})
</script>

<style scoped>
/* ============ Dashboard 容器 - WeDance 风格 ============ */
.dashboard {
  padding: var(--spacing-xl);
  background: var(--color-bg-primary);
  min-height: 100vh;
}

/* ============ 页面标题 ============ */
.page-header {
  margin-bottom: 40px;
}

.page-title {
  font-size: var(--font-size-4xl);
  font-weight: var(--font-weight-bold);
  margin-bottom: var(--spacing-sm);
  line-height: 1.2;
  color: var(--color-text-primary);
}

.page-subtitle {
  font-size: var(--font-size-lg);
  color: var(--color-text-secondary);
}

.text-gradient {
  color: var(--color-primary);
}

/* ============ 统计卡片网格 ============ */
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 20px;
  margin-bottom: 40px;
}

/* 统计卡片样式 - 继承自 common.css */
.stat-card .stat-card-icon {
  width: 56px;
  height: 56px;
  font-size: 28px;
  margin-bottom: 16px;
}

.stat-card .stat-card-title {
  margin-bottom: 8px;
}

.stat-card .stat-card-value {
  font-size: 48px;
  font-weight: 600;
  margin-bottom: 8px;
}

.storage-value {
  font-size: 24px !important;
}

.stat-highlight {
  color: var(--color-primary);
  font-weight: var(--font-weight-semibold);
}

/* ============ 统计卡片 ============ */
.stats-row {
  margin-bottom: var(--spacing-xl);
}

.success-icon {
  color: var(--color-success);
}

.warning-icon {
  color: var(--color-warning);
}

.info-icon {
  color: var(--color-info);
}

.stat-suffix {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
}

.success-text {
  color: var(--color-success);
  font-weight: var(--font-weight-semibold);
}

/* ============ AI 进度卡片 ============ */
.progress-row {
  margin-bottom: var(--spacing-xl);
}

.progress-card :deep(.el-card__body) {
  padding: var(--spacing-xl);
}

.progress-card > :deep(.section-header) {
  margin-bottom: var(--spacing-lg);
}

.start-button {
  border-radius: var(--radius-sm);
  font-weight: var(--font-weight-semibold);
  transition: all var(--transition-base);
}

.progress-content {
  margin-top: var(--spacing-lg);
}

.modern-progress {
  margin-bottom: var(--spacing-lg);
  height: 8px;
  background: var(--color-bg-secondary);
  border-radius: var(--radius-full);
  overflow: hidden;
}

.modern-progress-bar {
  height: 100%;
  background: linear-gradient(to right, var(--color-success) 0%, var(--color-warning) 100%);
  border-radius: var(--radius-full);
  transition: width var(--transition-slow);
}

.progress-info {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--spacing-lg);
  padding: var(--spacing-lg);
  background: var(--color-bg-secondary);
  border-radius: var(--radius-sm);
}

.progress-stat {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
}

.progress-label {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  font-weight: var(--font-weight-medium);
}

.progress-value {
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
}

.progress-value.error {
  color: var(--color-error);
}

/* ============ 照片卡片 ============ */
.photos-row {
  margin-bottom: var(--spacing-xl);
}

.photos-card :deep(.el-card__body) {
  padding: var(--spacing-xl);
}

.photos-card > :deep(.section-header) {
  margin-bottom: var(--spacing-lg);
}

.photos-count {
  display: inline-flex;
  align-items: center;
  padding: 4px 12px;
  background: var(--color-primary);
  color: white;
  border-radius: var(--radius-full);
  font-size: var(--font-size-xs);
  font-weight: var(--font-weight-semibold);
}

.view-all-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  font-weight: var(--font-weight-medium);
  transition: all var(--transition-fast);
}

.view-all-btn:hover {
  transform: translateX(4px);
}

.photos-grid {
  margin-top: var(--spacing-lg);
}

.photo-col {
  margin-bottom: var(--spacing-md);
}

.image-card {
  height: 240px;
  position: relative;
  border-radius: var(--radius-md);
  overflow: hidden;
  cursor: pointer;
  transition: all var(--transition-base);
  background: var(--color-bg-secondary);
  box-shadow: var(--shadow-sm);
  border: 1px solid var(--color-border);
}

.image-card:hover {
  box-shadow: var(--shadow-lg);
  border-color: var(--color-primary);
}

.image-card-image {
  width: 100%;
  height: 100%;
  object-fit: cover;
  transition: transform var(--transition-base);
}

.image-card:hover .image-card-image {
  transform: scale(1.05);
}

/* 分数徽章 */
.image-card-badge {
  position: absolute;
  top: var(--spacing-sm);
  right: var(--spacing-sm);
  padding: 4px 12px;
  border-radius: var(--radius-full);
  background: rgba(255, 255, 255, 0.95);
  color: var(--color-text-primary);
  font-size: var(--font-size-xs);
  font-weight: var(--font-weight-semibold);
  display: flex;
  align-items: center;
  gap: 4px;
  z-index: 2;
  transition: transform var(--transition-base);
  box-shadow: var(--shadow-sm);
}

.score-badge {
  background: var(--color-primary);
  color: white;
}

.badge-info {
  background: var(--color-info);
  color: white;
}

.image-card:hover .image-card-badge {
  transform: scale(1.05);
}

.image-card-overlay {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  background: linear-gradient(to top, rgba(0, 0, 0, 0.8), transparent);
  padding: var(--spacing-md);
  transform: translateY(100%);
  transition: transform var(--transition-base);
  z-index: 1;
}

.image-card:hover .image-card-overlay {
  transform: translateY(0);
}

.overlay-content {
  color: white;
}

.photo-name {
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-semibold);
  margin-bottom: var(--spacing-xs);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.photo-date {
  font-size: var(--font-size-xs);
  color: rgba(255, 255, 255, 0.8);
}

/* ============ 响应式设计 ============ */
@media (max-width: 1200px) {
  .dashboard {
    padding: var(--spacing-xl);
  }

  .page-title {
    font-size: var(--font-size-3xl);
  }
}

@media (max-width: 768px) {
  .dashboard {
    padding: var(--spacing-lg);
  }


  .page-title {
    font-size: var(--font-size-2xl);
  }

  .page-subtitle {
    font-size: var(--font-size-base);
  }

  .progress-card,
  .photos-card {
    padding: var(--spacing-lg) !important;
  }

  .image-card {
    height: 200px;
  }
}

@media (max-width: 480px) {
  .dashboard {
    padding: var(--spacing-md);
  }



  .image-card {
    height: 180px;
  }
}
</style>
