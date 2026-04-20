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
          <div class="review-target-name">
            {{ suggestion.target_person?.name?.trim() || `未命名人物 #${suggestion.target_person?.id || suggestion.target_person_id}` }}
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
          >
            <el-checkbox :value="item.candidate_person_id" />
            <div class="candidate-card-body">
              <div class="candidate-name">
                {{ item.candidate_person?.name?.trim() || `候选人物 #${item.candidate_person_id}` }}
              </div>
              <div class="candidate-meta">
                <span>{{ getPersonCategoryLabel(item.candidate_person?.category) }}</span>
                <span>{{ `相似度 ${(item.similarity_score * 100).toFixed(1)}%` }}</span>
              </div>
            </div>
          </label>
        </el-checkbox-group>
      </template>

      <el-empty v-else description="建议详情不存在或已处理" />
    </div>

    <template #footer>
      <div class="review-footer">
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
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import type { PersonMergeSuggestion } from '@/types/people'
import { getPersonCategoryLabel, sortMergeSuggestionCandidates } from './peopleHelpers'

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

const sortedItems = computed(() => sortMergeSuggestionCandidates(props.suggestion?.items || []))

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

.candidate-card-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.candidate-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.candidate-meta {
  display: flex;
  gap: 12px;
  font-size: 13px;
  color: var(--color-text-secondary);
  flex-wrap: wrap;
}

.review-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
