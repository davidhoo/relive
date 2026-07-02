<template>
  <el-dialog
    :model-value="modelValue"
    title="审核人物合并建议"
    width="720px"
    destroy-on-close
    @close="emit('update:modelValue', false)"
  >
    <div v-loading="loading" class="review-dialog">
      <template v-if="suggestion">
        <div class="review-target">
          <div class="review-target-label">目标人物</div>
          <div class="review-target-header">
            <el-avatar
              :size="48"
              :src="getFaceThumbnail(suggestion.target_person?.representative_face_id)"
              class="candidate-avatar clickable"
              @click="goToPersonDetail(suggestion.target_person_id)"
            >
              {{ getPersonAvatarFallback(suggestion.target_person || { category: suggestion.target_category_snapshot as PersonCategory }) }}
            </el-avatar>
            <div class="review-target-name">
              {{ suggestion.target_person?.name?.trim() || `未命名人物 #${suggestion.target_person?.id || suggestion.target_person_id}` }}
            </div>
          </div>
          <div class="review-target-meta">
            <span>{{ getPersonCategoryLabel(suggestion.target_person?.category || suggestion.target_category_snapshot) }}</span>
            <span>{{ suggestion.candidate_count }} 个候选</span>
            <span>{{ `最高相似度 ${(suggestion.top_similarity * 100).toFixed(1)}%` }}</span>
          </div>
        </div>

        <el-checkbox-group v-model="selectedIds" class="candidate-list">
          <label
            v-for="item in sortedItems"
            :key="item.candidate_person_id"
            class="candidate-card"
            :class="{ 'candidate-card--warning': isMergeSuggestionWarning(item) }"
          >
            <el-checkbox :value="item.candidate_person_id" />
            <el-avatar
              :size="40"
              :src="getFaceThumbnail(item.candidate_person?.representative_face_id)"
              class="candidate-avatar clickable"
              @click.stop="goToPersonDetail(item.candidate_person_id)"
            >
              {{ getPersonAvatarFallback(item.candidate_person || { category: 'stranger' }) }}
            </el-avatar>
            <div class="candidate-card-body">
              <div class="candidate-name">
                {{ item.candidate_person?.name?.trim() || `候选人物 #${item.candidate_person_id}` }}
              </div>
              <div class="candidate-meta">
                <span>{{ getPersonCategoryLabel(item.candidate_person?.category) }}</span>
                <span>{{ item.candidate_person?.photo_count || 0 }} 照片</span>
                <span>{{ item.candidate_person?.face_count || 0 }} 人脸</span>
                <span>{{ `相似度 ${(item.similarity_score * 100).toFixed(1)}%` }}</span>
              </div>
              <div v-if="isMergeSuggestionWarning(item)" class="candidate-warning">
                <el-icon><WarningFilled /></el-icon>
                <span>{{ MERGE_SUGGESTION_SAME_PHOTO_WARNING }}</span>
              </div>
            </div>
          </label>
        </el-checkbox-group>
      </template>

      <el-empty v-else description="建议详情不存在或已处理" />
    </div>

    <template #footer>
      <div class="review-footer">
        <div class="review-footer-left">
          <el-button :disabled="selectableIds.length === 0" @click="selectAll">
            {{ isAllSelected ? '取消全选' : '全选' }}
          </el-button>
          <el-button :disabled="selectableIds.length === 0" @click="invertSelection">反选</el-button>
        </div>
        <div class="review-footer-right">
          <el-button @click="emit('update:modelValue', false)">关闭</el-button>
        <el-button
          type="warning"
          :disabled="selectedIds.length === 0 || submitting"
          :loading="submitting"
          @click="emit('exclude', [...selectedIds])"
        >
          剔除所选
        </el-button>
        <el-button
          type="primary"
          :disabled="selectedIds.length === 0 || submitting"
          :loading="submitting"
          @click="emit('apply', [...selectedIds])"
        >
          确认合并所选
          </el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { WarningFilled } from '@element-plus/icons-vue'

import type { PersonCategory, PersonMergeSuggestion } from '@/types/people'
import { MERGE_SUGGESTION_SAME_PHOTO_WARNING, getPersonAvatarFallback, getPersonCategoryLabel, isMergeSuggestionWarning, sortMergeSuggestionCandidates } from './peopleHelpers'

const router = useRouter()

const props = defineProps<{
  modelValue: boolean
  suggestion: PersonMergeSuggestion | null
  loading?: boolean
  submitting?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  exclude: [candidateIds: number[]]
  apply: [candidateIds: number[]]
}>()

const selectedIds = ref<number[]>([])
const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const sortedItems = computed(() => sortMergeSuggestionCandidates(props.suggestion?.items || []))

// 全选/反选仅在无 warning 候选范围内操作；warning 候选可手动勾选但不被批量默认选中。
const selectableIds = computed(() => sortedItems.value.filter(item => !isMergeSuggestionWarning(item)).map(i => i.candidate_person_id))

const isAllSelected = computed(() => selectableIds.value.length > 0 && selectableIds.value.every(id => selectedIds.value.includes(id)))

function selectAll() {
  const selectableSet = new Set(selectableIds.value)
  if (isAllSelected.value) {
    // 取消全选：仅移除无 warning 候选，保留用户手动选中的 warning 候选。
    selectedIds.value = selectedIds.value.filter(id => !selectableSet.has(id))
  } else {
    // 全选：选中全部无 warning 候选，保留已选 warning 候选。
    const merged = new Set(selectedIds.value)
    for (const id of selectableIds.value) {
      merged.add(id)
    }
    selectedIds.value = [...merged]
  }
}

function invertSelection() {
  // 反选只在无 warning 候选范围内操作；warning 候选的选中状态保持不变。
  const selectableSet = new Set(selectableIds.value)
  const inverted = new Set<number>(selectedIds.value.filter(id => !selectableSet.has(id)))
  for (const id of selectableIds.value) {
    if (inverted.has(id)) {
      inverted.delete(id)
    } else {
      inverted.add(id)
    }
  }
  selectedIds.value = [...inverted]
}

function goToPersonDetail(personId: number) {
  router.push({ name: 'PeopleDetail', params: { id: personId } })
}

const getFaceThumbnail = (faceId?: number) => {
  if (!faceId) return ''
  return `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`
}

watch(
  () => [props.modelValue, props.suggestion?.id],
  () => {
    selectedIds.value = []
  },
  { immediate: true },
)
</script>

<style scoped>
.review-dialog {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-height: 160px;
}

.review-target {
  padding: 16px 18px;
  border-radius: 14px;
  background: var(--color-bg-soft);
  border: 1px solid var(--color-border);
}

.review-target-label {
  font-size: 12px;
  color: var(--color-text-secondary);
  margin-bottom: 6px;
}

.review-target-name {
  font-size: 18px;
  font-weight: 700;
  color: var(--color-text-primary);
}

.review-target-header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.review-target-meta {
  display: flex;
  gap: 12px;
  margin-top: 8px;
  font-size: 13px;
  color: var(--color-text-secondary);
  flex-wrap: wrap;
}

.candidate-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.candidate-card {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  border: 1px solid var(--color-border);
  border-radius: 14px;
  padding: 14px 16px;
  cursor: pointer;
}

.candidate-avatar {
  flex-shrink: 0;
}

.candidate-card-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  flex: 1;
}

.candidate-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
  line-height: 1.4;
}

.candidate-meta {
  display: flex;
  gap: 12px;
  font-size: 13px;
  color: var(--color-text-secondary);
  flex-wrap: wrap;
  line-height: 1.4;
}

.review-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
}

.review-footer-left {
  display: flex;
  gap: 10px;
}

.review-footer-right {
  display: flex;
  gap: 10px;
}

.candidate-avatar.clickable {
  cursor: pointer;
  transition: transform 0.15s;
}

.candidate-avatar.clickable:hover {
  transform: scale(1.1);
}

.candidate-card--warning {
  border-color: var(--el-color-warning);
  background: var(--el-color-warning-light-9);
}

.candidate-warning {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  font-size: 12px;
  color: var(--el-color-warning);
  line-height: 1.4;
}

.candidate-warning .el-icon {
  flex-shrink: 0;
  margin-top: 1px;
}
</style>
