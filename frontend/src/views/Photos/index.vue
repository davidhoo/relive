<template>
  <div class="photos-page">
    <!-- 页面标题 -->
    <div class="page-header animate-fade-in">
      <h1 class="page-title">
        <span class="text-gradient">照片管理</span>
      </h1>
      <p class="page-subtitle">浏览和管理您的照片集合</p>
    </div>

    <!-- 工具栏 -->
    <div class="toolbar-card modern-card animate-fade-in">
      <el-row :gutter="20" align="middle">
        <el-col :xs="24" :sm="12" :md="10">
          <el-input
            v-model="searchQuery"
            placeholder="搜索照片 (路径、设备ID、标签...)"
            clearable
            size="large"
            @clear="handleSearch"
            @keyup.enter="handleSearch"
            class="search-input"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
        </el-col>
        <el-col :xs="24" :sm="12" :md="8">
          <el-radio-group v-model="filterAnalyzed" @change="handleSearch" size="large" class="filter-group">
            <el-radio-button label="">全部</el-radio-button>
            <el-radio-button label="true">已分析</el-radio-button>
            <el-radio-button label="false">未分析</el-radio-button>
          </el-radio-group>
        </el-col>
        <el-col :xs="24" :sm="24" :md="6" class="action-col">
          <el-button type="primary" size="large" @click="handleScan" :loading="loading" class="scan-button">
            <el-icon><FolderOpened /></el-icon>
            扫描照片
          </el-button>
        </el-col>
      </el-row>
    </div>

    <!-- 照片网格 -->
    <div class="photos-grid-card modern-card animate-fade-in" v-loading="loading">
      <!-- 空状态 -->
      <el-empty v-if="!photos.length && !loading" description="暂无照片" :image-size="120">
        <el-button type="primary" @click="handleScan" :loading="loading">
          <el-icon><FolderOpened /></el-icon>
          扫描照片
        </el-button>
      </el-empty>

      <!-- 照片网格 -->
      <div v-else>
        <!-- 统计信息 -->
        <div class="photos-stats">
          <div class="stat-item">
            <el-icon class="stat-icon"><Picture /></el-icon>
            <span class="stat-text">共 <strong>{{ total }}</strong> 张照片</span>
          </div>
          <div class="stat-item" v-if="filterAnalyzed">
            <el-icon class="stat-icon"><Filter /></el-icon>
            <span class="stat-text">筛选结果</span>
          </div>
        </div>

        <el-row :gutter="16" class="photo-grid">
          <el-col
            :xs="12"
            :sm="8"
            :md="6"
            :lg="4"
            v-for="(photo, index) in photos"
            :key="photo.id"
            class="photo-col"
          >
            <div
              class="photo-card photo-card-parallax animate-scale-in"
              :style="{ animationDelay: `${index * 30}ms` }"
              @click="gotoDetail(photo.id)"
            >
              <div class="photo-image-wrapper">
                <el-image
                  :src="getPhotoUrl(photo.id)"
                  :preview-src-list="[getPhotoUrl(photo.id)]"
                  fit="cover"
                  class="photo-image"
                  loading="lazy"
                >
                  <template #error>
                    <div class="image-error">
                      <el-icon><PictureFilled /></el-icon>
                      <span>加载失败</span>
                    </div>
                  </template>
                  <template #placeholder>
                    <div class="image-loading">
                      <el-icon class="is-loading"><Loading /></el-icon>
                    </div>
                  </template>
                </el-image>

                <!-- 分析状态徽章 -->
                <div class="photo-badge" v-if="photo.is_analyzed" :class="getScoreClass(photo.overall_score)">
                  <el-icon><Star /></el-icon>
                  <span>{{ photo.overall_score?.toFixed(1) }}</span>
                </div>
                <div class="photo-badge badge-unanalyzed" v-else>
                  <el-icon><QuestionFilled /></el-icon>
                  <span>未分析</span>
                </div>

                <!-- 悬停信息 -->
                <div class="photo-overlay">
                  <div class="photo-info">
                    <div class="photo-name" :title="getFileName(photo.file_path)">
                      {{ getFileName(photo.file_path) }}
                    </div>
                    <div class="photo-meta">
                      <span v-if="photo.taken_at" class="meta-item">
                        <el-icon><Clock /></el-icon>
                        {{ formatDate(photo.taken_at) }}
                      </span>
                      <span v-if="photo.width && photo.height" class="meta-item">
                        <el-icon><FullScreen /></el-icon>
                        {{ photo.width }}×{{ photo.height }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </el-col>
        </el-row>

        <!-- 分页 -->
        <div class="pagination-wrapper">
          <el-pagination
            v-model:current-page="currentPage"
            v-model:page-size="pageSize"
            :page-sizes="[20, 50, 100]"
            :total="total"
            layout="total, sizes, prev, pager, next, jumper"
            @size-change="handlePageChange"
            @current-change="handlePageChange"
            background
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { photoApi } from '@/api/photo'
import type { Photo } from '@/types/photo'

const router = useRouter()

const photos = ref<Photo[]>([])
const loading = ref(false)
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const searchQuery = ref('')
const filterAnalyzed = ref('')

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

// 获取分数等级样式
const getScoreClass = (score?: number) => {
  if (!score) return 'badge-low'
  if (score >= 8) return 'badge-excellent'
  if (score >= 6) return 'badge-good'
  if (score >= 4) return 'badge-medium'
  return 'badge-low'
}

// 加载照片列表
const loadPhotos = async () => {
  loading.value = true
  try {
    const params: any = {
      page: currentPage.value,
      page_size: pageSize.value,
    }

    if (searchQuery.value) {
      params.search = searchQuery.value
    }

    if (filterAnalyzed.value) {
      params.is_analyzed = filterAnalyzed.value === 'true'
    }

    const res = await photoApi.getList(params)
    photos.value = res.data?.items || []
    total.value = res.data?.total || 0
  } catch (error: any) {
    ElMessage.error(error.message || '加载照片列表失败')
  } finally {
    loading.value = false
  }
}

// 搜索处理
const handleSearch = () => {
  currentPage.value = 1
  loadPhotos()
}

// 分页处理
const handlePageChange = () => {
  loadPhotos()
}

// 扫描照片
const handleScan = async () => {
  try {
    loading.value = true
    const res = await photoApi.scan()
    ElMessage.success(`扫描完成，新增 ${res.data?.new_count || 0} 张照片`)
    await loadPhotos()
  } catch (error: any) {
    ElMessage.error(error.message || '扫描照片失败')
  } finally {
    loading.value = false
  }
}

// 跳转到详情页
const gotoDetail = (photoId: number) => {
  router.push(`/photos/${photoId}`)
}

onMounted(() => {
  loadPhotos()
})
</script>

<style scoped>
/* ============ Photos 页面容器 ============ */
.photos-page {
  padding: var(--spacing-xl);
  background: var(--color-bg-secondary);
  min-height: 100vh;
}

/* ============ 页面标题 ============ */
.page-header {
  margin-bottom: var(--spacing-2xl);
}

.page-title {
  font-size: var(--font-size-4xl);
  font-weight: var(--font-weight-bold);
  margin-bottom: var(--spacing-sm);
  line-height: 1.2;
}

.page-subtitle {
  font-size: var(--font-size-lg);
  color: var(--color-text-secondary);
}

/* ============ 工具栏 ============ */
.toolbar-card {
  margin-bottom: var(--spacing-xl);
  padding: var(--spacing-xl) !important;
}

.search-input {
  border-radius: var(--radius-lg);
}

.search-input :deep(.el-input__wrapper) {
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-sm);
  transition: all var(--transition-base);
}

.search-input :deep(.el-input__wrapper:hover) {
  box-shadow: var(--shadow-md);
}

.search-input :deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 2px rgba(91, 127, 255, 0.2);
}

.filter-group {
  width: 100%;
  display: flex;
}

.filter-group :deep(.el-radio-button) {
  flex: 1;
}

.filter-group :deep(.el-radio-button__inner) {
  width: 100%;
  border-radius: var(--radius-lg);
}

.action-col {
  display: flex;
  justify-content: flex-end;
}

.scan-button {
  width: 100%;
  background: var(--gradient-primary);
  border: none;
  border-radius: var(--radius-lg);
  font-weight: var(--font-weight-medium);
  transition: all var(--transition-spring);
  will-change: transform;
}

.scan-button:hover {
  background: var(--gradient-primary-hover);
  transform: translateY(-3px);
  box-shadow: 0 12px 24px -8px rgba(16, 185, 129, 0.5);
}

/* ============ 照片网格卡片 ============ */
.photos-grid-card {
  padding: var(--spacing-2xl) !important;
}

/* 统计信息 */
.photos-stats {
  display: flex;
  align-items: center;
  gap: var(--spacing-xl);
  margin-bottom: var(--spacing-xl);
  padding: var(--spacing-lg);
  background: var(--color-bg-secondary);
  border-radius: var(--radius-lg);
}

.stat-item {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  color: var(--color-text-secondary);
  font-size: var(--font-size-base);
}

.stat-icon {
  font-size: 20px;
  color: var(--color-primary);
}

.stat-text strong {
  color: var(--color-text-primary);
  font-weight: var(--font-weight-bold);
  font-size: var(--font-size-lg);
}

/* ============ 照片网格 ============ */
.photo-grid {
  margin-top: var(--spacing-lg);
}

.photo-col {
  margin-bottom: var(--spacing-lg);
}

.photo-card {
  cursor: pointer;
  transition: all var(--transition-spring);
  will-change: transform;
}

/* 视差 3D 倾斜效果 */
.photo-card-parallax {
  transform-style: preserve-3d;
  perspective: 1000px;
}

.photo-card-parallax:hover {
  transform: translateY(-12px) rotateX(5deg) rotateY(5deg) scale(1.02);
}

.photo-image-wrapper {
  position: relative;
  width: 100%;
  height: 280px;
  border-radius: var(--radius-2xl);
  overflow: hidden;
  background: var(--color-bg-tertiary);
  box-shadow: var(--shadow-sm);
  transition: all var(--transition-spring);
  will-change: transform, box-shadow;
}

.photo-card:hover .photo-image-wrapper {
  box-shadow: 0 25px 50px -12px rgba(16, 185, 129, 0.3);
}

.photo-image {
  width: 100%;
  height: 100%;
  transition: all var(--transition-slow);
  will-change: transform, filter;
}

.photo-card:hover .photo-image {
  transform: scale(1.15);
  filter: brightness(0.7);
}

/* 图片加载状态 */
.image-loading,
.image-error {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--spacing-sm);
  color: var(--color-text-tertiary);
  background: var(--color-bg-tertiary);
}

.image-loading .el-icon,
.image-error .el-icon {
  font-size: 48px;
}

/* 分析状态徽章 - 更精致的设计 */
.photo-badge {
  position: absolute;
  top: var(--spacing-sm);
  right: var(--spacing-sm);
  padding: 8px 16px;
  border-radius: var(--radius-full);
  backdrop-filter: blur(12px);
  color: white;
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-bold);
  display: flex;
  align-items: center;
  gap: 6px;
  z-index: 2;
  transition: all var(--transition-spring);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.2);
  will-change: transform;
}

.photo-card:hover .photo-badge {
  transform: scale(1.15) translateY(-4px);
}

.badge-excellent {
  background: linear-gradient(135deg, rgba(16, 185, 129, 0.95), rgba(52, 211, 153, 0.95));
}

.badge-good {
  background: linear-gradient(135deg, rgba(6, 182, 212, 0.95), rgba(34, 211, 238, 0.95));
}

.badge-medium {
  background: linear-gradient(135deg, rgba(245, 158, 11, 0.95), rgba(251, 191, 36, 0.95));
}

.badge-low {
  background: linear-gradient(135deg, rgba(244, 63, 94, 0.95), rgba(251, 113, 133, 0.95));
}

.badge-unanalyzed {
  background: rgba(107, 114, 128, 0.95);
}

/* 悬停信息遮罩 */
.photo-overlay {
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

.photo-card:hover .photo-overlay {
  transform: translateY(0);
}

.photo-info {
  color: white;
}

.photo-name {
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-semibold);
  margin-bottom: var(--spacing-sm);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.photo-meta {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
  font-size: var(--font-size-xs);
  color: rgba(255, 255, 255, 0.8);
}

.meta-item {
  display: flex;
  align-items: center;
  gap: 4px;
}

/* ============ 分页 ============ */
.pagination-wrapper {
  display: flex;
  justify-content: center;
  margin-top: var(--spacing-2xl);
  padding-top: var(--spacing-xl);
  border-top: 1px solid var(--color-border);
}

.pagination-wrapper :deep(.el-pagination) {
  gap: var(--spacing-sm);
}

.pagination-wrapper :deep(.el-pager li) {
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.pagination-wrapper :deep(.el-pager li:hover) {
  background: var(--gradient-primary);
  color: white;
}

.pagination-wrapper :deep(.el-pager li.is-active) {
  background: var(--gradient-primary);
}

/* ============ 响应式设计 ============ */
@media (max-width: 1200px) {
  .photos-page {
    padding: var(--spacing-lg);
  }

  .photo-image-wrapper {
    height: 240px;
  }
}

@media (max-width: 768px) {
  .photos-page {
    padding: var(--spacing-md);
  }

  .page-title {
    font-size: var(--font-size-2xl);
  }

  .toolbar-card {
    padding: var(--spacing-lg) !important;
  }

  .toolbar-card .el-col {
    margin-bottom: var(--spacing-md);
  }

  .action-col {
    justify-content: stretch;
  }

  .photos-grid-card {
    padding: var(--spacing-lg) !important;
  }

  .photos-stats {
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-sm);
  }

  .photo-image-wrapper {
    height: 200px;
  }

  .pagination-wrapper {
    overflow-x: auto;
  }

  .pagination-wrapper :deep(.el-pagination) {
    flex-wrap: nowrap;
  }
}

@media (max-width: 480px) {
  .page-title {
    font-size: var(--font-size-xl);
  }

  .page-subtitle {
    font-size: var(--font-size-base);
  }

  .photo-image-wrapper {
    height: 180px;
  }
}
</style>
