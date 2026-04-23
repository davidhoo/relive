<template>
  <el-dialog
    :model-value="modelValue"
    :title="`审核旋转建议 - ${rotationLabel}`"
    width="800px"
    destroy-on-close
    @close="emit('update:modelValue', false)"
  >
    <div v-loading="loading" class="review-dialog">
      <template v-if="detail">
        <div class="review-info">
          <span>共 {{ detail.total }} 张照片需要{{ rotationLabel }}</span>
        </div>

        <el-checkbox-group v-model="selectedIds" class="photo-list">
          <label
            v-for="photo in detail.photos"
            :key="photo.id"
            class="photo-card"
          >
            <div class="photo-preview">
              <el-checkbox :value="photo.id" class="photo-checkbox" />
              <img
                :src="getThumbnailUrl(photo.id, photo.updated_at)"
                :style="{ transform: `rotate(${photo.suggested_rotation}deg)` }"
              />
            </div>
            <div class="photo-info">
              <span>置信度 {{ (photo.confidence * 100).toFixed(0) }}%</span>
              <span v-if="photo.low_confidence" class="low-confidence">低置信度</span>
            </div>
          </label>
        </el-checkbox-group>

        <div v-if="detail.total > detail.photos.length" class="pagination-info">
          显示 {{ detail.photos.length }} / {{ detail.total }} 张
        </div>
      </template>

      <el-empty v-else-if="!loading" description="没有待审核的旋转建议" />
    </div>

    <template #footer>
      <div class="review-footer">
        <el-button @click="emit('update:modelValue', false)">关闭</el-button>
        <el-button
          type="warning"
          :disabled="selectedIds.length === 0 || submitting"
          :loading="submitting"
          @click="handleDismiss"
        >
          忽略所选
        </el-button>
        <el-button
          type="primary"
          :disabled="selectedIds.length === 0 || submitting"
          :loading="submitting"
          @click="handleApply"
        >
          确认旋转所选
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { OrientationSuggestionDetail } from '@/types/photo'

const props = defineProps<{
  modelValue: boolean
  rotation: number
  detail: OrientationSuggestionDetail | null
  loading?: boolean
  submitting?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  apply: [photoIds: number[]]
  dismiss: [photoIds: number[]]
}>()

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const selectedIds = ref<number[]>([])

const rotationLabels: Record<number, string> = {
  0: '无需旋转',
  90: '顺时针 90°',
  180: '旋转 180°',
  270: '顺时针 270°',
}

const rotationLabel = computed(() => rotationLabels[props.rotation] || `${props.rotation}°`)

const getThumbnailUrl = (photoId: number, version?: string) => {
  let url = `${apiBaseUrl}/photos/${photoId}/thumbnail`
  if (version) {
    url += `?v=${encodeURIComponent(version)}`
  }
  return url
}

watch(
  () => [props.modelValue, props.rotation],
  () => {
    selectedIds.value = []
  },
  { immediate: true }
)

const handleApply = () => {
  emit('apply', [...selectedIds.value])
}

const handleDismiss = () => {
  emit('dismiss', [...selectedIds.value])
}
</script>

<style scoped>
.review-dialog {
  min-height: 200px;
}

.review-info {
  margin-bottom: 16px;
  font-size: 14px;
  color: var(--color-text-secondary);
}

.photo-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
  gap: 16px;
}

.photo-card {
  display: flex;
  flex-direction: column;
  cursor: pointer;
}

.photo-preview {
  position: relative;
  flex-shrink: 0;
}

.photo-preview img {
  width: 100%;
  aspect-ratio: 1;
  object-fit: cover;
  border-radius: 8px;
}

.photo-checkbox {
  position: absolute;
  top: 8px;
  left: 8px;
  z-index: 1;
}

.photo-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: 8px;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.low-confidence {
  color: #e6a23c;
}

.pagination-info {
  margin-top: 16px;
  text-align: center;
  font-size: 13px;
  color: var(--color-text-secondary);
}

.review-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
