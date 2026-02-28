<template>
  <div class="dashboard">
    <!-- 页面标题 -->
    <div class="page-header animate-fade-in">
      <h1 class="page-title">
        <span class="text-gradient">Dashboard</span>
      </h1>
      <p class="page-subtitle">照片管理系统概览</p>
    </div>

    <!-- Bento Grid 布局 - 现代非对称设计 -->
    <div class="bento-grid animate-fade-in">
      <!-- 总照片数 - 大卡片 (2x2) -->
      <div class="bento-card bento-card-large">
        <div class="bento-card-bg"></div>
        <div class="bento-card-content">
          <div class="stat-card-header">
            <div class="stat-card-icon stat-icon-emerald">
              <el-icon><Picture /></el-icon>
            </div>
          </div>
          <div class="stat-card-value-large">{{ systemStats?.total_photos || 0 }}</div>
          <div class="stat-card-title-large">总照片数</div>
          <div class="stat-card-subtitle">所有照片</div>
        </div>
      </div>

      <!-- 已分析 - 中等卡片 -->
      <div class="bento-card bento-card-medium animate-delay-1">
        <div class="bento-card-bg"></div>
        <div class="bento-card-content">
          <div class="stat-card-header">
            <div class="stat-card-icon stat-icon-cyan">
              <el-icon><MagicStick /></el-icon>
            </div>
          </div>
          <div class="stat-card-value">{{ systemStats?.analyzed_photos || 0 }}</div>
          <div class="stat-card-title">已分析</div>
          <div class="stat-card-subtitle">
            分析率 <span class="stat-highlight">{{ analysisRate }}%</span>
          </div>
          <div class="progress-mini">
            <div class="progress-mini-bar" :style="{ width: analysisRate + '%' }"></div>
          </div>
        </div>
      </div>

      <!-- 在线设备 - 小卡片 -->
      <div class="bento-card bento-card-small animate-delay-2">
        <div class="bento-card-bg"></div>
        <div class="bento-card-content">
          <div class="stat-card-icon stat-icon-amber">
            <el-icon><Monitor /></el-icon>
          </div>
          <div class="stat-card-value-small">{{ systemStats?.online_devices || 0 }}</div>
          <div class="stat-card-title-small">在线设备</div>
          <div class="stat-card-subtitle-small">
            共 {{ systemStats?.total_devices || 0 }} 台
          </div>
        </div>
      </div>

      <!-- 存储空间 - 小卡片 -->
      <div class="bento-card bento-card-small animate-delay-3">
        <div class="bento-card-bg"></div>
        <div class="bento-card-content">
          <div class="stat-card-icon stat-icon-rose">
            <el-icon><DataLine /></el-icon>
          </div>
          <div class="stat-card-value-small storage-value-small">{{ storageSize }}</div>
          <div class="stat-card-title-small">存储空间</div>
          <div class="stat-card-subtitle-small">
            {{ systemStats?.total_photos || 0 }} 张
          </div>
        </div>
      </div>
    </div>

    <!-- AI 分析进度 - 液态玻璃效果 -->
    <el-row :gutter="20" class="progress-row">
      <el-col :span="24">
        <div class="progress-card glass-card animate-fade-in animate-delay-4">
          <div class="progress-card-header">
            <div class="progress-title">
              <el-icon class="progress-icon"><MagicStick /></el-icon>
              <span>AI 分析进度</span>
            </div>
            <button
              class="magnetic-button"
              @click="handleStartAnalysis"
              :disabled="analyzing"
            >
              <span class="magnetic-button-bg"></span>
              <span class="magnetic-button-content">
                <el-icon v-if="!analyzing"><VideoPlay /></el-icon>
                {{ analyzing ? '分析中...' : '开始批量分析' }}
              </span>
            </button>
          </div>
          <div v-if="aiProgress" class="progress-content">
            <div class="modern-progress">
              <div
                class="modern-progress-bar flowing-bar"
                :style="{ width: progressPercentage + '%' }"
                :class="{
                  'progress-success': progressStatus === 'success',
                  'progress-warning': progressStatus === 'warning'
                }"
              ></div>
            </div>
            <div class="progress-info">
              <div class="progress-stat">
                <span class="progress-label">已完成</span>
                <span class="progress-value animated-number">{{ aiProgress.completed }}/{{ aiProgress.total }}</span>
              </div>
              <div class="progress-stat">
                <span class="progress-label">进度</span>
                <span class="progress-value animated-number">{{ progressPercentage }}%</span>
              </div>
              <div class="progress-stat" v-if="aiProgress.failed > 0">
                <span class="progress-label">失败</span>
                <span class="progress-value error animated-number">{{ aiProgress.failed }}</span>
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

    <!-- 最近照片 - 聚光灯边框效果 -->
    <el-row :gutter="20" class="photos-row">
      <el-col :span="24">
        <div class="photos-card spotlight-card animate-fade-in">
          <div class="spotlight-border"></div>
          <div class="photos-card-header">
            <div class="photos-title">
              <el-icon class="photos-icon"><Picture /></el-icon>
              <span>最近照片</span>
              <span class="photos-count">{{ recentPhotos.length }} 张</span>
            </div>
            <el-button type="primary" size="default" link @click="gotoPhotos" class="view-all-btn">
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
                class="image-card tilt-card animate-scale-in"
                :style="{ animationDelay: `${index * 50}ms` }"
                @click="gotoPhotoDetail(photo.id)"
              >
                <el-image
                  :src="getPhotoUrl(photo.id)"
                  :preview-src-list="[getPhotoUrl(photo.id)]"
                  fit="cover"
                  class="image-card-image"
                  loading="lazy"
                />
                <div class="image-card-badge score-badge" v-if="photo.is_analyzed">
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
    const res = await photoApi.scan()
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
/* ============ Dashboard 容器 - 深色主题 ============ */
.dashboard {
  padding: 48px;
  background: var(--color-bg-primary);
  min-height: 100vh;
  position: relative;
}

/* ============ 页面标题 ============ */
.page-header {
  margin-bottom: 48px;
}

.page-title {
  font-size: var(--font-size-5xl);
  font-weight: var(--font-weight-extrabold);
  margin-bottom: var(--spacing-sm);
  line-height: 1.1;
  letter-spacing: -0.02em;
}

.page-subtitle {
  font-size: var(--font-size-xl);
  color: var(--color-text-secondary);
  font-weight: var(--font-weight-normal);
}

/* ============ Bento Grid 布局 ============ */
.bento-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  grid-template-rows: repeat(2, 200px);
  gap: 24px;
  margin-bottom: 48px;
}

/* Bento 卡片基础样式 */
.bento-card {
  position: relative;
  border-radius: var(--radius-3xl);
  overflow: hidden;
  cursor: pointer;
  transition: all var(--transition-spring);
  will-change: transform;
}

.bento-card-bg {
  position: absolute;
  inset: 0;
  background: rgba(255, 255, 255, 0.03);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: var(--radius-3xl);
  transition: all var(--transition-spring);
  z-index: 0;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.bento-card::before {
  content: '';
  position: absolute;
  inset: -2px;
  border-radius: var(--radius-3xl);
  background: linear-gradient(135deg, var(--color-primary), var(--color-accent));
  opacity: 0;
  z-index: -1;
  filter: blur(30px);
  transition: opacity var(--transition-base);
}

.bento-card:hover .bento-card-bg {
  background: rgba(255, 255, 255, 0.05);
  border-color: rgba(102, 126, 234, 0.5);
  box-shadow:
    0 20px 48px rgba(0, 0, 0, 0.5),
    var(--shadow-glow);
}

.bento-card:hover::before {
  opacity: 0.6;
}

.bento-card:hover {
  transform: translateY(-12px) scale(1.02);
}

.bento-card-content {
  position: relative;
  height: 100%;
  padding: 32px;
  display: flex;
  flex-direction: column;
  z-index: 1;
}

/* 大卡片 - 2x2 */
.bento-card-large {
  grid-column: span 2;
  grid-row: span 2;
}

.bento-card-large .stat-card-value-large {
  font-size: var(--font-size-8xl);
  font-weight: var(--font-weight-extrabold);
  line-height: 1;
  background: var(--gradient-hero);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  margin-top: auto;
  margin-bottom: 16px;
  transition: all var(--transition-spring);
  text-shadow: 0 0 40px rgba(102, 126, 234, 0.5);
}

.bento-card-large:hover .stat-card-value-large {
  transform: scale(1.05);
  filter: drop-shadow(0 0 20px rgba(102, 126, 234, 0.8));
}

.bento-card-large .stat-card-title-large {
  font-size: var(--font-size-2xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
  margin-bottom: 8px;
  letter-spacing: -0.01em;
}

/* 中等卡片 - 2x1 */
.bento-card-medium {
  grid-column: span 2;
  grid-row: span 1;
}

/* 小卡片 - 1x1 */
.bento-card-small {
  grid-column: span 1;
  grid-row: span 1;
}

.bento-card-small .stat-card-value-small {
  font-size: var(--font-size-5xl);
  font-weight: var(--font-weight-extrabold);
  color: var(--color-text-primary);
  margin: auto 0;
  line-height: 1;
}

.bento-card-small .stat-card-title-small {
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
  margin-bottom: 4px;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.bento-card-small .stat-card-subtitle-small {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
}

/* 统计卡片组件 */
.stat-card-header {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  margin-bottom: var(--spacing-lg);
}

.stat-card-icon {
  width: 56px;
  height: 56px;
  border-radius: var(--radius-xl);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 28px;
  transition: all var(--transition-spring);
  will-change: transform;
  position: relative;
}

.stat-card-icon::after {
  content: '';
  position: absolute;
  inset: -4px;
  border-radius: var(--radius-xl);
  background: inherit;
  opacity: 0;
  filter: blur(15px);
  transition: opacity var(--transition-base);
  z-index: -1;
}

.bento-card:hover .stat-card-icon {
  transform: scale(1.2) rotate(10deg);
}

.bento-card:hover .stat-card-icon::after {
  opacity: 0.8;
}

/* 配色方案 - 紫蓝系 */
.stat-icon-emerald {
  background: linear-gradient(135deg, rgba(102, 126, 234, 0.2), rgba(118, 75, 162, 0.2));
  color: var(--color-primary-light);
  box-shadow: 0 0 20px rgba(102, 126, 234, 0.4);
}

.stat-icon-cyan {
  background: linear-gradient(135deg, rgba(79, 172, 254, 0.2), rgba(0, 242, 254, 0.2));
  color: var(--color-accent-light);
  box-shadow: 0 0 20px rgba(79, 172, 254, 0.4);
}

.stat-icon-amber {
  background: linear-gradient(135deg, rgba(245, 158, 11, 0.2), rgba(251, 191, 36, 0.2));
  color: var(--color-warning-light);
  box-shadow: 0 0 20px rgba(245, 158, 11, 0.4);
}

.stat-icon-rose {
  background: linear-gradient(135deg, rgba(240, 147, 251, 0.2), rgba(245, 87, 108, 0.2));
  color: var(--color-secondary-light);
  box-shadow: 0 0 20px rgba(240, 147, 251, 0.4);
}

.stat-card-title {
  font-size: var(--font-size-base);
  color: var(--color-text-secondary);
  font-weight: var(--font-weight-medium);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.stat-card-value {
  font-size: var(--font-size-4xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
  margin-bottom: var(--spacing-sm);
  line-height: 1;
}

.stat-card-subtitle {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
}

.stat-highlight {
  color: var(--color-primary);
  font-weight: var(--font-weight-bold);
}

/* 迷你进度条 */
.progress-mini {
  margin-top: auto;
  height: 6px;
  background: var(--color-bg-tertiary);
  border-radius: var(--radius-full);
  overflow: hidden;
}

.progress-mini-bar {
  height: 100%;
  background: var(--gradient-primary);
  border-radius: var(--radius-full);
  transition: width var(--transition-slow);
}

/* ============ 液态玻璃卡片效果 ============ */
.glass-card {
  background: rgba(255, 255, 255, 0.7);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border: 1px solid rgba(255, 255, 255, 0.3);
  border-radius: var(--radius-2xl);
  padding: 40px;
  box-shadow: 0 8px 32px 0 rgba(31, 38, 135, 0.15);
  transition: all var(--transition-base);
}

.glass-card:hover {
  background: rgba(255, 255, 255, 0.8);
  box-shadow: 0 20px 48px 0 rgba(31, 38, 135, 0.25);
}

/* ============ 磁性按钮效果 - 2026 风格 ============ */
.magnetic-button {
  position: relative;
  padding: 16px 32px;
  border: none;
  border-radius: var(--radius-xl);
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
  overflow: hidden;
  transition: all var(--transition-spring);
  will-change: transform;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
}

.magnetic-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.magnetic-button-bg {
  position: absolute;
  inset: 0;
  background: var(--gradient-hero);
  transition: all var(--transition-spring);
  z-index: 0;
}

.magnetic-button::before {
  content: '';
  position: absolute;
  inset: -2px;
  border-radius: var(--radius-xl);
  background: var(--gradient-hero);
  opacity: 0;
  filter: blur(20px);
  z-index: -1;
  transition: opacity var(--transition-base);
}

.magnetic-button:hover:not(:disabled) .magnetic-button-bg {
  transform: scale(1.05);
  filter: brightness(1.1);
}

.magnetic-button:hover:not(:disabled)::before {
  opacity: 0.8;
}

.magnetic-button:hover:not(:disabled) {
  transform: translateY(-4px);
  box-shadow: var(--shadow-glow-lg);
}

.magnetic-button:active:not(:disabled) {
  transform: translateY(-2px);
}

.magnetic-button-content {
  position: relative;
  display: flex;
  align-items: center;
  gap: 8px;
  color: white;
  z-index: 1;
}

/* ============ AI 进度卡片 ============ */
.progress-row {
  margin-bottom: var(--spacing-xl);
}

.progress-card {
  padding: var(--spacing-2xl) !important;
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
  font-size: 28px;
  color: var(--color-primary);
}

.progress-content {
  margin-top: var(--spacing-xl);
}

.modern-progress {
  margin-bottom: var(--spacing-xl);
  position: relative;
  height: 16px;
  background: var(--color-bg-tertiary);
  border-radius: var(--radius-full);
  overflow: hidden;
}

.modern-progress-bar {
  height: 100%;
  background: var(--gradient-primary);
  border-radius: var(--radius-full);
  transition: width var(--transition-slow);
  position: relative;
  overflow: hidden;
}

/* 流动效果 */
.flowing-bar::after {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  right: 0;
  background: linear-gradient(
    90deg,
    transparent,
    rgba(255, 255, 255, 0.4),
    transparent
  );
  animation: flow 2s linear infinite;
}

@keyframes flow {
  0% {
    transform: translateX(-100%);
  }
  100% {
    transform: translateX(100%);
  }
}

.progress-success {
  background: var(--gradient-success);
}

.progress-warning {
  background: var(--gradient-warning);
}

.progress-info {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--spacing-lg);
  padding: var(--spacing-lg);
  background: var(--color-bg-secondary);
  border-radius: var(--radius-lg);
}

.progress-stat {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
}

.progress-label {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
  font-weight: var(--font-weight-medium);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.progress-value {
  font-size: var(--font-size-2xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
}

.progress-value.error {
  color: var(--color-error);
}

/* 数字动画效果 */
.animated-number {
  transition: all var(--transition-spring);
}

/* ============ 聚光灯边框卡片 - Linear 风格 ============ */
.spotlight-card {
  position: relative;
  background: rgba(255, 255, 255, 0.03);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  border-radius: var(--radius-3xl);
  padding: 40px;
  overflow: hidden;
  border: 1px solid rgba(255, 255, 255, 0.1);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  transition: all var(--transition-base);
}

.spotlight-border {
  position: absolute;
  inset: -2px;
  border-radius: var(--radius-3xl);
  background: linear-gradient(135deg, var(--color-primary), var(--color-accent), var(--color-secondary));
  opacity: 0;
  transition: opacity var(--transition-base);
  animation: rotate 6s linear infinite;
  filter: blur(20px);
  z-index: -1;
}

.spotlight-card:hover {
  background: rgba(255, 255, 255, 0.05);
  border-color: rgba(102, 126, 234, 0.3);
  box-shadow: 0 20px 48px rgba(0, 0, 0, 0.5), var(--shadow-glow);
}

.spotlight-card:hover .spotlight-border {
  opacity: 0.8;
}

@keyframes rotate-border {
  0% {
    transform: rotate(0deg);
  }
  100% {
    transform: rotate(360deg);
  }
}

/* ============ 照片卡片 ============ */
.photos-row {
  margin-bottom: var(--spacing-xl);
}

.photos-card {
  padding: var(--spacing-2xl) !important;
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
  font-size: 28px;
  color: var(--color-primary);
}

.photos-count {
  display: inline-flex;
  align-items: center;
  padding: var(--spacing-xs) var(--spacing-md);
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-semibold);
}

.view-all-btn {
  display: flex;
  align-items: center;
  gap: var(--spacing-xs);
  font-weight: var(--font-weight-medium);
  transition: all var(--transition-fast);
}

.view-all-btn:hover {
  transform: translateX(4px);
}

.photos-grid {
  margin-top: var(--spacing-xl);
}

.photo-col {
  margin-bottom: var(--spacing-md);
}

/* 3D 倾斜卡片效果 */
.tilt-card {
  transform-style: preserve-3d;
  perspective: 1000px;
}

.tilt-card:hover {
  transform: translateY(-8px) rotateX(2deg) rotateY(2deg);
}

.image-card {
  height: 240px;
  position: relative;
  border-radius: var(--radius-2xl);
  overflow: hidden;
  cursor: pointer;
  transition: all var(--transition-spring);
  background: var(--color-bg-tertiary);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.5);
  will-change: transform;
}

.image-card::before {
  content: '';
  position: absolute;
  inset: -2px;
  border-radius: var(--radius-2xl);
  background: linear-gradient(135deg, var(--color-primary), var(--color-accent));
  opacity: 0;
  z-index: -1;
  filter: blur(20px);
  transition: opacity var(--transition-base);
}

.image-card:hover::before {
  opacity: 0.6;
}

.image-card:hover {
  box-shadow: 0 20px 40px rgba(0, 0, 0, 0.6), var(--shadow-glow);
}

.image-card-image {
  width: 100%;
  height: 100%;
  object-fit: cover;
  transition: all var(--transition-slow);
}

.image-card:hover .image-card-image {
  transform: scale(1.15);
  filter: brightness(0.7);
}

/* 分数徽章 - 更精致的设计 */
.image-card-badge {
  position: absolute;
  top: var(--spacing-sm);
  right: var(--spacing-sm);
  padding: 8px 16px;
  border-radius: var(--radius-full);
  backdrop-filter: blur(20px) saturate(180%);
  -webkit-backdrop-filter: blur(20px) saturate(180%);
  color: white;
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-bold);
  display: flex;
  align-items: center;
  gap: 6px;
  z-index: 2;
  transition: all var(--transition-spring);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
  border: 1px solid rgba(255, 255, 255, 0.2);
}

.score-badge {
  background: linear-gradient(135deg, rgba(102, 126, 234, 0.95), rgba(118, 75, 162, 0.95));
  box-shadow: 0 0 20px rgba(102, 126, 234, 0.6);
}

.badge-info {
  background: rgba(107, 114, 128, 0.95);
}

.image-card:hover .image-card-badge {
  transform: scale(1.2) translateY(-6px);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.5), 0 0 30px rgba(102, 126, 234, 0.8);
}

.image-card-overlay {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  background: linear-gradient(to top, rgba(0, 0, 0, 0.95), rgba(0, 0, 0, 0.5), transparent);
  padding: var(--spacing-lg);
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

  .bento-grid {
    grid-template-columns: repeat(3, 1fr);
    grid-template-rows: auto;
  }

  .bento-card-large {
    grid-column: span 2;
    grid-row: span 1;
  }

  .bento-card-medium {
    grid-column: span 3;
  }

  .page-title {
    font-size: var(--font-size-3xl);
  }
}

@media (max-width: 768px) {
  .dashboard {
    padding: var(--spacing-lg);
  }

  .bento-grid {
    grid-template-columns: 1fr;
    grid-template-rows: auto;
    gap: 16px;
  }

  .bento-card-large,
  .bento-card-medium,
  .bento-card-small {
    grid-column: span 1;
    grid-row: span 1;
  }

  .bento-card-large .stat-card-value-large {
    font-size: 80px;
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

  .bento-card-content {
    padding: 20px;
  }

  .bento-card-large .stat-card-value-large {
    font-size: 60px;
  }

  .progress-title,
  .photos-title {
    font-size: var(--font-size-base);
  }

  .magnetic-button {
    width: 100%;
    justify-content: center;
  }

  .image-card {
    height: 180px;
  }
}
</style>
