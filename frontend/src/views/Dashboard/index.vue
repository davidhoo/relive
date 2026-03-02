<template>
  <div class="dashboard">
    <!-- 页面标题 -->
    <div class="page-header animate-fade-in">
      <h1 class="page-title">
        <span class="text-gradient">Dashboard</span>
      </h1>
      <p class="page-subtitle">照片管理系统概览</p>
    </div>

    <!-- 统计卡片网格 - WeDance 风格 -->
    <div class="stats-grid animate-fade-in">
      <!-- 总照片数 -->
      <div class="stat-card">
        <div class="stat-card-icon stat-icon-primary">
          <el-icon><Picture /></el-icon>
        </div>
        <div class="stat-card-title">总照片数</div>
        <div class="stat-card-value">{{ systemStats?.total_photos || 0 }}</div>
        <div class="stat-card-subtitle">所有照片</div>
      </div>

      <!-- 已分析 -->
      <div class="stat-card">
        <div class="stat-card-icon stat-icon-success">
          <el-icon><MagicStick /></el-icon>
        </div>
        <div class="stat-card-title">已分析</div>
        <div class="stat-card-value">{{ systemStats?.analyzed_photos || 0 }}</div>
        <div class="stat-card-subtitle">
          分析率 <span class="stat-highlight">{{ analysisRate }}%</span>
        </div>
      </div>

      <!-- 在线设备 -->
      <div class="stat-card">
        <div class="stat-card-icon stat-icon-warning">
          <el-icon><Monitor /></el-icon>
        </div>
        <div class="stat-card-title">在线设备</div>
        <div class="stat-card-value">{{ systemStats?.online_devices || 0 }}</div>
        <div class="stat-card-subtitle">
          共 {{ systemStats?.total_devices || 0 }} 台
        </div>
      </div>

      <!-- 存储空间 -->
      <div class="stat-card">
        <div class="stat-card-icon stat-icon-info">
          <el-icon><DataLine /></el-icon>
        </div>
        <div class="stat-card-title">存储空间</div>
        <div class="stat-card-value storage-value">{{ storageSize }}</div>
        <div class="stat-card-subtitle">
          {{ systemStats?.total_photos || 0 }} 张
        </div>
      </div>
    </div>

    <!-- AI 分析进度 - 简洁卡片 -->
    <el-row :gutter="20" class="progress-row">
      <el-col :span="24">
        <div class="progress-card modern-card animate-fade-in animate-delay-1">
          <div class="progress-card-header">
            <div class="progress-title">
              <el-icon class="progress-icon"><MagicStick /></el-icon>
              <span>AI 分析进度</span>
            </div>
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
          </div>
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
        </div>
      </el-col>
    </el-row>

    <!-- 最近照片 - 简洁卡片 -->
    <el-row :gutter="20" class="photos-row">
      <el-col :span="24">
        <div class="photos-card modern-card animate-fade-in animate-delay-2">
          <div class="photos-card-header">
            <div class="photos-title">
              <el-icon class="photos-icon"><Picture /></el-icon>
              <span>最近照片</span>
              <span class="photos-count">{{ recentPhotos.length }} 张</span>
            </div>
            <el-button link @click="gotoPhotos" class="view-all-btn" style="color: var(--color-primary); font-weight: 500;">
              查看全部
              <el-icon><ArrowRight /></el-icon>
            </el-button>
          </div>
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
                  :src="getPhotoUrl(photo.id)"
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
        </div>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useSystemStore } from '@/stores/system'
import { photoApi } from '@/api/photo'
import { aiApi } from '@/api/ai'
import type { Photo } from '@/types/photo'
import type { AIAnalyzeProgress } from '@/types/ai'

const router = useRouter()
const systemStore = useSystemStore()

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

// 获取照片 URL
const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return `${baseUrl}/photos/${photoId}/image`
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
    recentPhotos.value = res.data?.items || []
  } catch (error) {
    console.error('Failed to load recent photos:', error)
  }
}

// 加载 AI 进度
const loadAIProgress = async () => {
  try {
    const res = await aiApi.getProgress()
    aiProgress.value = res.data || null
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
    // 使用环境变量中配置的照片路径
    const photoPath = '/Volumes/home/Photos/MobileBackup/iPhone/2025/11'
    const res = await photoApi.scan({ path: photoPath })
    ElMessage.success(`扫描完成，新增 ${res.data?.new_count || 0} 张照片`)
    await loadRecentPhotos()
    await systemStore.fetchStats()
  } catch (error: any) {
    ElMessage.error(error.message || '扫描照片失败')
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
  padding: 48px;
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

/* ============ AI 进度卡片 ============ */
.progress-row {
  margin-bottom: var(--spacing-xl);
}

.progress-card {
  padding: var(--spacing-xl) !important;
}

.progress-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: var(--spacing-xl);
  flex-wrap: wrap;
  gap: var(--spacing-md);
}

.progress-title {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
}

.progress-icon {
  font-size: 24px;
  color: var(--color-primary);
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

.photos-card {
  padding: var(--spacing-xl) !important;
}

.photos-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: var(--spacing-xl);
  flex-wrap: wrap;
  gap: var(--spacing-md);
}

.photos-title {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
}

.photos-icon {
  font-size: 24px;
  color: var(--color-primary);
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

  .stats-grid {
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  }

  .page-title {
    font-size: var(--font-size-3xl);
  }
}

@media (max-width: 768px) {
  .dashboard {
    padding: var(--spacing-lg);
  }

  .stats-grid {
    grid-template-columns: repeat(2, 1fr);
    gap: 16px;
  }

  .stat-card .stat-card-value {
    font-size: 36px;
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

  .stats-grid {
    grid-template-columns: 1fr;
  }

  .stat-card .stat-card-value {
    font-size: 32px;
  }

  .progress-title,
  .photos-title {
    font-size: var(--font-size-base);
  }

  .image-card {
    height: 180px;
  }
}
</style>
