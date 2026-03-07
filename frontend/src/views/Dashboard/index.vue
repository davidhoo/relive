<template>
  <div class="dashboard">
    <PageHeader title="仪表盘" subtitle="照片管理系统概览" :gradient="true" />

    <el-row :gutter="20" class="stats-row animate-fade-in">
      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover">
          <el-statistic title="总照片数" :value="systemStats?.total_photos || 0">
            <template #prefix>
              <el-icon class="photo-icon"><Picture /></el-icon>
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

    <el-row :gutter="20" class="progress-row">
      <el-col :span="24">
        <el-card shadow="never" class="progress-card animate-fade-in animate-delay-1">
          <template #header>
            <SectionHeader :icon="MagicStick" title="AI 分析进度">
              <template #actions>
                <div class="panel-actions">
                  <span class="count-pill">{{ aiProgress?.is_running ? '进行中' : '待机' }}</span>
                  <el-button
                    type="primary"
                    size="small"
                    @click="handleStartAnalysis"
                    :disabled="analyzing"
                    class="action-pill action-pill-primary"
                  >
                    <el-icon v-if="!analyzing"><VideoPlay /></el-icon>
                    {{ analyzing ? '分析中...' : '开始分析' }}
                  </el-button>
                </div>
              </template>
            </SectionHeader>
          </template>

          <div v-if="aiProgress" class="progress-content">
            <div class="progress-overview-panel">
              <div class="progress-hero">
                <div class="progress-hero-top">
                  <span class="progress-status-dot" :class="{ running: aiProgress.is_running }"></span>
                  <span class="progress-status-text">{{ progressStateText }}</span>
                </div>
                <div class="progress-hero-value">{{ progressPercentage }}<span>%</span></div>
                <div class="progress-hero-desc">{{ progressDescription }}</div>
              </div>

              <div class="progress-stats-grid">
                <div class="progress-stat-card">
                  <span class="progress-stat-card-label">总任务</span>
                  <strong class="progress-stat-card-value">{{ aiProgress.total }}</strong>
                </div>
                <div class="progress-stat-card">
                  <span class="progress-stat-card-label">已完成</span>
                  <strong class="progress-stat-card-value success">{{ aiProgress.completed }}</strong>
                </div>
                <div class="progress-stat-card">
                  <span class="progress-stat-card-label">待处理</span>
                  <strong class="progress-stat-card-value">{{ remainingCount }}</strong>
                </div>
                <div class="progress-stat-card" :class="{ warning: aiProgress.failed > 0 }">
                  <span class="progress-stat-card-label">失败</span>
                  <strong class="progress-stat-card-value danger">{{ aiProgress.failed }}</strong>
                </div>
              </div>
            </div>

            <div class="progress-track-panel">
              <div class="modern-progress">
                <div class="modern-progress-bar" :style="{ width: progressPercentage + '%' }"></div>
              </div>

              <div class="progress-info">
                <div class="progress-stat">
                  <span class="progress-label">当前照片</span>
                  <span class="progress-value compact">{{ currentPhotoText }}</span>
                </div>
                <div class="progress-stat">
                  <span class="progress-label">运行模式</span>
                  <span class="progress-value compact">{{ progressModeText }}</span>
                </div>
                <div class="progress-stat">
                  <span class="progress-label">开始时间</span>
                  <span class="progress-value compact">{{ startedAtText }}</span>
                </div>
                <div class="progress-stat">
                  <span class="progress-label">当前状态</span>
                  <span class="progress-value compact">{{ progressStatusText }}</span>
                </div>
              </div>
            </div>

            <div class="progress-meta-row" v-if="aiProgress.current_message">
              <span class="progress-meta-label">运行消息</span>
              <span class="progress-meta-text">{{ aiProgress.current_message }}</span>
            </div>
          </div>

          <el-empty v-else description="暂无分析任务" :image-size="80">
            <el-button type="primary" size="small" @click="handleStartAnalysis" class="action-pill action-pill-primary">
              <el-icon><VideoPlay /></el-icon>
              开始分析
            </el-button>
          </el-empty>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="20" class="photos-row">
      <el-col :span="24">
        <el-card shadow="never" class="photos-card animate-fade-in animate-delay-2">
          <template #header>
            <SectionHeader :icon="Picture" title="最近照片">
              <template #actions>
                <div class="photos-title-actions">
                  <span class="count-pill">共 {{ recentPhotos.length }} 张</span>
                  <el-button size="small" plain @click="gotoPhotos" class="view-all-btn">
                    查看全部
                    <el-icon><ArrowRight /></el-icon>
                  </el-button>
                </div>
              </template>
            </SectionHeader>
          </template>

          <div v-if="recentPhotos.length" class="recent-photos-grid">
              <div
                v-for="(photo, index) in recentPhotos"
                :key="photo.id"
                class="recent-photo-card animate-scale-in"
                :style="{ animationDelay: `${index * 30}ms` }"
                @click="gotoPhotoDetail(photo.id)"
              >
                <div class="recent-photo-cover">
                  <el-image
                    :src="getPhotoThumbnailUrl(photo.id, photo.updated_at)"
                    :preview-src-list="[getPhotoUrl(photo.id)]"
                    fit="cover"
                    class="recent-photo-image"
                    loading="lazy"
                  />
                  <div v-if="photo.ai_analyzed && photo.overall_score !== undefined" class="recent-photo-badge score">
                    <el-icon><Star /></el-icon>
                    {{ photo.overall_score.toFixed(1) }}
                  </div>
                  <div v-else class="recent-photo-badge pending">
                    <el-icon><QuestionFilled /></el-icon>
                    未分析
                  </div>
                </div>

              </div>
          </div>

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
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  ArrowRight,
  DataLine,
  FolderOpened,
  MagicStick,
  Monitor,
  Picture,
  QuestionFilled,
  Star,
  VideoPlay,
} from '@element-plus/icons-vue'
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

const systemStats = computed(() => systemStore.stats)

const analysisRate = computed(() => {
  if (!systemStats.value?.total_photos) return 0
  return Math.round((systemStats.value.analyzed_photos / systemStats.value.total_photos) * 100)
})

const storageSize = computed(() => {
  const size = systemStats.value?.storage_size || 0
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(2)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(2)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
})

const progressPercentage = computed(() => {
  if (!aiProgress.value?.total) return 0
  return Math.round((aiProgress.value.completed / aiProgress.value.total) * 100)
})

const remainingCount = computed(() => {
  if (!aiProgress.value) return 0
  return Math.max(aiProgress.value.total - aiProgress.value.completed - aiProgress.value.failed, 0)
})

const progressModeText = computed(() => {
  switch (aiProgress.value?.mode) {
    case 'batch':
      return '批量分析'
    case 'background':
      return '后台分析'
    default:
      return aiProgress.value?.mode || '未指定'
  }
})

const progressStatusText = computed(() => {
  switch (aiProgress.value?.status) {
    case 'running':
      return '运行中'
    case 'completed':
      return '已完成'
    case 'failed':
      return '失败'
    case 'pending':
      return '排队中'
    default:
      return aiProgress.value?.is_running ? '处理中' : '待机'
  }
})

const progressStateText = computed(() => {
  if (!aiProgress.value) return '待机'
  if (aiProgress.value.is_running) return '分析进行中'
  if (aiProgress.value.total === 0) return '暂无待处理任务'
  if (remainingCount.value === 0) return aiProgress.value.failed > 0 ? '任务已结束' : '任务已完成'
  return '等待开始'
})

const progressDescription = computed(() => {
  if (!aiProgress.value) return '当前没有运行中的分析任务'
  if (aiProgress.value.current_message) return aiProgress.value.current_message
  if (aiProgress.value.is_running) {
    return `已完成 ${aiProgress.value.completed} 张，剩余 ${remainingCount.value} 张待处理`
  }
  if (aiProgress.value.total === 0) return '当前没有待分析照片'
  if (aiProgress.value.failed > 0) return `本轮任务已停止，失败 ${aiProgress.value.failed} 张`
  return '当前批量分析任务已经完成'
})

const currentPhotoText = computed(() => {
  if (!aiProgress.value?.current_photo_id) return '—'
  return `#${aiProgress.value.current_photo_id}`
})

const startedAtText = computed(() => {
  return formatDateTime(aiProgress.value?.started_at)
})

const getPhotoThumbnailUrl = (photoId: number, version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const token = userStore.token
  const params = new URLSearchParams()
  if (token) params.set('token', token)
  if (version) params.set('v', version)
  const query = params.toString()
  return `${baseUrl}/photos/${photoId}/thumbnail${query ? `?${query}` : ''}`
}

const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const token = userStore.token
  return `${baseUrl}/photos/${photoId}/image${token ? `?token=${token}` : ''}`
}



const formatDateTime = (dateStr?: string) => {
  if (!dateStr) return '—'
  try {
    const date = new Date(dateStr)
    return date.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return '—'
  }
}


const loadRecentPhotos = async () => {
  try {
    const res = await photoApi.getList({ page: 1, page_size: 12 })
    recentPhotos.value = res.data?.data?.items || []
  } catch (error) {
    console.error('Failed to load recent photos:', error)
  }
}

const loadAIProgress = async () => {
  try {
    const res = await aiApi.getProgress()
    aiProgress.value = res.data?.data || null
    analyzing.value = Boolean(aiProgress.value?.is_running)
  } catch (error: any) {
    if (error?.response?.status === 503) {
      console.log('AI service is not configured')
      aiProgress.value = null
      analyzing.value = false
    } else {
      console.error('Failed to load AI progress:', error)
    }
  }
}

const handleStartAnalysis = async () => {
  try {
    analyzing.value = true
    await aiApi.startBackground()
    await loadAIProgress()
    ElMessage.success('后台分析已启动')

    const timer = window.setInterval(async () => {
      await loadAIProgress()
      if (!aiProgress.value?.is_running) {
        clearInterval(timer)
        analyzing.value = false
        await systemStore.fetchStats()
        ElMessage.success('后台分析已停止')
      }
    }, 2000)
  } catch (error: any) {
    analyzing.value = false
    ElMessage.error(error.message || '启动后台分析失败')
  }
}

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
      } catch {
        clearInterval(timer)
      }
    }, 2000)
  } catch (error: any) {
    ElMessage.error(error.message || '启动扫描任务失败')
  }
}

const gotoPhotos = () => {
  router.push('/photos')
}

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
.dashboard {
  padding: var(--spacing-xl);
  background: var(--color-bg-primary);
  min-height: 100vh;
}

.stats-row,
.progress-row,
.photos-row {
  margin-bottom: var(--spacing-xl);
}

.photo-icon {
  color: var(--color-primary);
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

.progress-card,
.photos-card {
  border-radius: 24px;
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.progress-card :deep(.el-card__header),
.photos-card :deep(.el-card__header) {
  padding: 22px 28px;
  border-bottom: 1px solid var(--color-border);
}

.progress-card :deep(.el-card__body),
.photos-card :deep(.el-card__body) {
  padding: 28px;
}

.panel-actions,
.photos-title-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.count-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 32px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid #d9d9d9;
  background: #fff;
  color: var(--color-text-secondary);
  font-size: 13px;
  font-weight: var(--font-weight-medium);
}

.action-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 32px;
  padding-inline: 16px;
  border-radius: 999px;
  font-weight: var(--font-weight-semibold);
}

.action-pill-primary {
  background: var(--color-primary);
  border-color: var(--color-primary);
}

.view-all-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  height: 32px;
  padding-inline: 14px;
  border-radius: 999px;
  border-color: var(--color-border);
  color: var(--color-text-secondary);
  background: #fff;
  font-weight: var(--font-weight-medium);
}

.view-all-btn:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: #fff;
}

.progress-content {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.progress-overview-panel {
  display: grid;
  grid-template-columns: minmax(240px, 320px) 1fr;
  gap: 20px;
  padding: 24px;
  background: linear-gradient(180deg, #f9fbfd 0%, #f5f7fa 100%);
  border: 1px solid var(--color-border);
  border-radius: 20px;
}

.progress-hero {
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 8px 4px;
}

.progress-hero-top {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.progress-status-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: #cfd8dc;
}

.progress-status-dot.running {
  background: #52c41a;
  box-shadow: 0 0 0 6px rgba(82, 196, 26, 0.12);
}

.progress-status-text {
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
}

.progress-hero-value {
  font-size: 52px;
  line-height: 1;
  font-weight: 700;
  color: var(--color-text-primary);
}

.progress-hero-value span {
  margin-left: 4px;
  font-size: 22px;
  color: var(--color-text-secondary);
}

.progress-hero-desc {
  margin-top: 12px;
  color: var(--color-text-secondary);
  font-size: 14px;
  line-height: 1.7;
}

.progress-stats-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.progress-stat-card {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 8px;
  min-height: 112px;
  padding: 18px 20px;
  background: #fff;
  border: 1px solid var(--color-border);
  border-radius: 18px;
}

.progress-stat-card.warning {
  border-color: #ffd591;
  background: #fffaf2;
}

.progress-stat-card-label {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.progress-stat-card-value {
  color: var(--color-text-primary);
  font-size: 32px;
  line-height: 1;
}

.progress-stat-card-value.success {
  color: #0d8a4f;
}

.progress-stat-card-value.danger {
  color: #cf1322;
}

.progress-track-panel {
  padding: 22px 24px;
  border-radius: 20px;
  border: 1px solid var(--color-border);
  background: #fff;
}

.modern-progress {
  height: 12px;
  margin-bottom: 20px;
  background: #eef2f6;
  border-radius: 999px;
  overflow: hidden;
}

.modern-progress-bar {
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(90deg, #33c18d 0%, #20b2aa 100%);
  transition: width var(--transition-slow);
}

.progress-info {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
}

.progress-stat {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px 18px;
  border-radius: 16px;
  background: var(--color-bg-secondary);
}

.progress-label {
  font-size: 12px;
  color: var(--color-text-tertiary);
  font-weight: var(--font-weight-medium);
}

.progress-value {
  color: var(--color-text-primary);
  font-size: 20px;
  font-weight: var(--font-weight-semibold);
}

.progress-value.compact {
  font-size: 15px;
  line-height: 1.5;
}

.progress-meta-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 16px 18px;
  background: #f8fafc;
  border: 1px dashed #d9e2ec;
  border-radius: 16px;
}

.progress-meta-label {
  flex-shrink: 0;
  color: var(--color-text-secondary);
  font-size: 13px;
  font-weight: var(--font-weight-semibold);
}

.progress-meta-text {
  color: var(--color-text-primary);
  font-size: 14px;
  line-height: 1.6;
}







.recent-photos-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
  gap: 18px;
}

.recent-photo-card {
  overflow: hidden;
  border-radius: 18px;
  border: 1px solid var(--color-border);
  background: #fff;
  cursor: pointer;
  transition: transform var(--transition-fast), box-shadow var(--transition-fast), border-color var(--transition-fast);
}

.recent-photo-card:hover {
  transform: translateY(-2px);
  border-color: rgba(0, 184, 148, 0.28);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.08);
}

.recent-photo-cover {
  position: relative;
  aspect-ratio: 4 / 3;
  background: var(--color-bg-secondary);
}

.recent-photo-image {
  display: block;
  width: 100%;
  height: 100%;
}

.recent-photo-image :deep(.el-image__inner) {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.recent-photo-badge {
  position: absolute;
  top: 12px;
  right: 12px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: var(--font-weight-semibold);
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12);
}

.recent-photo-badge.score {
  background: rgba(19, 194, 194, 0.94);
  color: #fff;
}

.recent-photo-badge.pending {
  background: rgba(255, 255, 255, 0.96);
  color: var(--color-text-secondary);
}


.photos-filter-empty {
  padding-block: 16px;
}

@media (max-width: 1200px) {
  .progress-overview-panel {
    grid-template-columns: 1fr;
  }

  .progress-info {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .dashboard {
    padding: var(--spacing-lg);
  }

  .progress-card :deep(.el-card__header),
  .photos-card :deep(.el-card__header),
  .progress-card :deep(.el-card__body),
  .photos-card :deep(.el-card__body) {
    padding: 20px;
  }

  .progress-stats-grid,
  .progress-info,
  .recent-photos-grid {
    grid-template-columns: 1fr;
  }

}

@media (max-width: 480px) {
  .dashboard {
    padding: var(--spacing-md);
  }

  .panel-actions,
  .photos-title-actions {
    align-items: stretch;
  }
}
</style>
