<template>
  <!--
    PhotoCard: 照片管理页卡片，翻页与连续浏览复用同一组件。
    - 连续浏览模式（virtual=true）禁用整页入场动画（animate-scale-in + animationDelay），
      避免虚拟行重新挂载时重复播放整页入场动画导致滚动闪烁。
    - 选中状态、勾选、Shift 连选、点击进入详情 行为与原内联卡片一致。
  -->
  <div
    class="photo-card photo-card-parallax"
    :class="[{ 'is-selected': selected, 'animate-scale-in': !virtual }, cardClass]"
    :style="!virtual ? { animationDelay: `${index * 30}ms` } : undefined"
    @click="selectedCount > 0 ? toggleSelect(photo.id, $event) : gotoDetail(photo.id)"
  >
    <div class="photo-image-wrapper">
      <!-- 选择按钮 -->
      <div
        class="photo-select-btn"
        :class="{ selected }"
        @click.stop="toggleSelect(photo.id, $event)"
      >
        <el-icon v-if="selected"><Select /></el-icon>
      </div>
      <el-image
        :src="getPhotoThumbnailUrl(photo.id, photo.updated_at)"
        :preview-src-list="[]"
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
      <div class="photo-badge" v-if="photo.ai_analyzed" :class="getScoreClass(photo.overall_score)">
        <el-icon><Star /></el-icon>
        <span>{{ photo.overall_score?.toFixed(1) }}</span>
      </div>

      <div class="photo-status-icons">
        <span
          class="photo-status-icon"
          :class="photo.ai_analyzed ? 'is-ready' : 'is-idle'"
          title="AI 分析状态"
        >
          <el-icon><MagicStick /></el-icon>
        </span>
        <span class="photo-status-icon" :class="photo.thumbnail_status === 'ready' ? 'is-ready' : 'is-idle'" title="缩略图状态">
          <el-icon><Files /></el-icon>
        </span>
        <span
          class="photo-status-icon"
          :class="photo.location ? 'is-ready' : 'is-idle'"
          :title="photo.gps_latitude && photo.gps_longitude ? 'GPS 位置状态' : '无 GPS 信息'"
        >
          <el-icon><Location /></el-icon>
        </span>
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
</template>

<script setup lang="ts">
import { Clock, Files, FullScreen, Loading, Location, MagicStick, PictureFilled, Select, Star } from '@element-plus/icons-vue'
import type { Photo } from '@/types/photo'

const props = defineProps<{
  photo: Photo
  index: number
  selected: boolean
  selectedCount: number
  /** 连续浏览（虚拟网格）模式：禁用整页入场动画。 */
  virtual?: boolean
  /** 卡片额外 class（batch-mode 等）。 */
  cardClass?: string
}>()

const emit = defineEmits<{
  (e: 'toggle', id: number, event?: MouseEvent): void
  (e: 'detail', id: number): void
}>()

const toggleSelect = (id: number, event?: MouseEvent) => emit('toggle', id, event)
const gotoDetail = (id: number) => emit('detail', id)

const getPhotoThumbnailUrl = (photoId: number, version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const params = new URLSearchParams()
  if (version) params.set('v', version)
  const query = params.toString()
  return `${baseUrl}/photos/${photoId}/thumbnail${query ? `?${query}` : ''}`
}

const getFileName = (filePath: string) => filePath.split('/').pop() || filePath

const formatDate = (dateStr: string) => {
  try {
    const date = new Date(dateStr)
    return date.toLocaleDateString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    })
  } catch {
    return ''
  }
}

const getScoreClass = (score?: number) => {
  if (!score) return 'badge-low'
  if (score >= 8) return 'badge-excellent'
  if (score >= 6) return 'badge-good'
  if (score >= 4) return 'badge-medium'
  return 'badge-low'
}

// 显式声明 props 用于模板（避免未使用警告）
void props
</script>

<style scoped>
@import './photoCard.css';
</style>
