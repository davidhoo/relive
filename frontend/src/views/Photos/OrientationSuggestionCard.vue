<template>
  <div class="orientation-card" @click="emit('review')">
    <div class="orientation-header">
      <span class="orientation-angle">{{ rotationLabel }}</span>
      <span class="orientation-count">{{ group.count }} 张</span>
    </div>
    <div class="orientation-preview">
      <img
        v-for="photo in previewPhotos"
        :key="photo.id"
        :src="getThumbnailUrl(photo.id, photo.updated_at)"
        class="preview-thumb"
        :style="{ transform: `rotate(${photo.suggested_rotation}deg)` }"
      />
    </div>
    <div class="orientation-meta">
      <span>平均置信度 {{ (group.avg_confidence * 100).toFixed(0) }}%</span>
      <span v-if="group.low_confidence_count > 0" class="low-confidence">
        {{ group.low_confidence_count }} 张低置信度
      </span>
    </div>
    <el-button type="primary" size="small" class="review-btn">审核</el-button>
  </div>
</template>

<script setup lang="ts">
import type { OrientationSuggestionGroup, OrientationSuggestionPhoto } from '@/types/photo'

const props = defineProps<{
  group: OrientationSuggestionGroup
  previewPhotos: OrientationSuggestionPhoto[]
}>()

const emit = defineEmits<{
  review: []
}>()

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const rotationLabels: Record<number, string> = {
  0: '无需旋转',
  90: '顺时针 90°',
  180: '旋转 180°',
  270: '顺时针 270°',
}

const rotationLabel = rotationLabels[props.group.suggested_rotation] || `${props.group.suggested_rotation}°`

const getThumbnailUrl = (photoId: number, version?: string) => {
  let url = `${apiBaseUrl}/photos/${photoId}/thumbnail`
  if (version) {
    url += `?v=${encodeURIComponent(version)}`
  }
  return url
}
</script>

<style scoped>
.orientation-card {
  border: 1px solid var(--color-border);
  border-radius: 14px;
  padding: 16px;
  background: linear-gradient(135deg, #f0f9ff 0%, #ffffff 100%);
  cursor: pointer;
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

.orientation-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 20px rgba(15, 23, 42, 0.1);
}

.orientation-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.orientation-angle {
  font-size: 16px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.orientation-count {
  font-size: 14px;
  color: var(--color-text-secondary);
}

.orientation-preview {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.preview-thumb {
  width: 60px;
  height: 60px;
  object-fit: cover;
  border-radius: 8px;
  border: 1px solid var(--color-border);
}

.orientation-meta {
  display: flex;
  gap: 12px;
  font-size: 12px;
  color: var(--color-text-secondary);
  margin-bottom: 12px;
}

.low-confidence {
  color: #e6a23c;
}

.review-btn {
  width: 100%;
}
</style>
