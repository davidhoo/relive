<template>
  <div class="person-card" :class="{ 'is-selected': selected, 'is-hidden': person.hidden }">
    <!-- 批量选择复选框（仅批量管理模式显示） -->
    <div
      v-if="selectable"
      class="person-card-select"
      @click.stop="handleSelectionClick"
    >
      <el-checkbox
        :model-value="selected"
        :aria-label="selected ? `取消选择「${displayName}」` : `选择「${displayName}」`"
      />
    </div>

    <!-- 可见性切换：眼睛图标，点击隐藏 / 恢复显示 -->
    <el-tooltip :content="person.hidden ? '恢复显示' : '隐藏人物'" placement="bottom" :show-after="300">
      <button
        type="button"
        class="person-card-menu-btn"
        :aria-label="person.hidden ? '恢复显示' : '隐藏人物'"
        @click.stop="emit('visibility', person.id, !person.hidden)"
      >
        <el-icon v-if="!person.hidden"><View /></el-icon>
        <el-icon v-else><Hide /></el-icon>
      </button>
    </el-tooltip>

    <!-- 头像：点击进入人物详情页 -->
    <button
      type="button"
      class="person-card-avatar-btn"
      :aria-label="avatarAriaLabel"
      @click="emit('detail', person.id)"
    >
      <div class="person-card-avatar">
        <img
          v-if="person.representative_face_id && !avatarFailed.has(person.representative_face_id)"
          :src="getFaceThumbnail(person.representative_face_id)"
          loading="lazy"
          decoding="async"
          alt=""
          @error="emit('avatar-failed', person.representative_face_id!)"
        />
        <span v-else class="person-card-avatar-fallback">{{ getPersonAvatarFallback(person) }}</span>
      </div>
    </button>

    <div class="person-card-body">
      <!-- 姓名：点击打开编辑弹框，未命名显示“这是谁？” -->
      <button
        type="button"
        class="person-card-name-btn"
        :class="{ 'is-unnamed': !hasName }"
        :title="hasName ? `${displayName} · 点击编辑` : '点击设置人物姓名'"
        :aria-label="nameAriaLabel"
        @click="emit('edit', person)"
      >
        <span class="person-card-name-text">{{ displayName }}</span>
      </button>

      <!-- 照片数（左）+ 类别（右）：纯展示，不可点击 -->
      <div class="person-card-meta">
        <span class="person-card-counts">{{ person.photo_count }} 张照片</span>
        <span class="person-card-category" :class="`is-${person.category}`">
          {{ getPersonCategoryLabel(person.category) }}
        </span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Hide, View } from '@element-plus/icons-vue'
import type { Person } from '@/types/people'
import { getPersonAvatarFallback, getPersonCategoryLabel } from './peopleHelpers'

const props = defineProps<{
  person: Person
  /** 头像加载失败的 faceId 集合（由父组件持有，便于刷新时统一重置） */
  avatarFailed: Set<number>
  /** 是否处于批量管理模式（显示复选框） */
  selectable?: boolean
  /** 当前是否被选中（批量管理模式） */
  selected?: boolean
}>()

const emit = defineEmits<{
  detail: [personId: number]
  edit: [person: Person]
  'avatar-failed': [faceId: number]
  'toggle-select': [personId: number, shiftKey: boolean]
  /** 设置人物可见性，payload 为目标隐藏状态 */
  visibility: [personId: number, hidden: boolean]
}>()

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const getFaceThumbnail = (faceId?: number) => {
  if (!faceId) return ''
  return `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`
}

const hasName = computed(() => !!props.person.name?.trim())
const displayName = computed(() => (hasName.value ? props.person.name!.trim() : '这是谁？'))
const avatarAriaLabel = computed(() =>
  hasName.value ? `查看「${displayName.value}」的人物详情` : '查看未命名人物详情',
)
const nameAriaLabel = computed(() =>
  hasName.value ? `编辑「${displayName.value}」的人物信息` : '设置人物姓名',
)

/**
 * 复选框外层接管点击：携带原始 MouseEvent 的 shiftKey 传给父组件，
 * 由父组件决定是普通切换还是 Shift 区间连选。
 * 不调用 preventDefault()，避免破坏复选框自身的焦点与键盘行为。
 */
const handleSelectionClick = (event: MouseEvent) => {
  emit('toggle-select', props.person.id, event.shiftKey)
}
</script>

<style scoped>
.person-card {
  position: relative;
  width: 100%;
  border: 1px solid var(--color-border);
  border-radius: 16px;
  padding: 10px;
  background: #fff;
  display: flex;
  flex-direction: column;
  gap: 10px;
  text-align: left;
  /* 网格行内等高：拉伸到行高，内部纵向排列 */
  height: 100%;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.person-card.is-selected {
  border-color: var(--color-primary, #d46b08);
  box-shadow: 0 0 0 2px rgba(212, 107, 8, 0.18);
}

.person-card.is-hidden {
  opacity: 0.62;
}

.person-card-select {
  position: absolute;
  top: 6px;
  left: 6px;
  z-index: 2;
  background: rgba(255, 255, 255, 0.92);
  border-radius: 6px;
  padding: 2px;
}

.person-card-menu-btn {
  position: absolute;
  top: 4px;
  right: 4px;
  z-index: 2;
  width: 26px;
  height: 26px;
  border: none;
  border-radius: 8px;
  padding: 0;
  margin: 0;
  background: rgba(255, 255, 255, 0.85);
  color: var(--color-text-secondary);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.2s ease, color 0.2s ease;
}

.person-card-menu-btn:hover {
  background: var(--color-bg-soft);
  color: var(--color-primary, #d46b08);
}

/* 头像按钮：1:1 圆角方形，懒加载，失败显示兜底 */
.person-card-avatar-btn {
  width: 100%;
  border: none;
  padding: 0;
  margin: 0;
  background: transparent;
  cursor: pointer;
  border-radius: 12px;
  overflow: hidden;
  display: block;
}

.person-card-avatar-btn:focus-visible {
  outline: 2px solid var(--color-primary, #d46b08);
  outline-offset: 2px;
}

.person-card-avatar {
  width: 100%;
  aspect-ratio: 1 / 1;
  border-radius: 12px;
  overflow: hidden;
  background: var(--color-bg-soft);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: transform 0.2s ease;
}

.person-card-avatar-btn:hover .person-card-avatar,
.person-card-avatar-btn:focus-visible .person-card-avatar {
  transform: scale(1.02);
}

.person-card-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.person-card-avatar-fallback {
  font-size: 30px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.person-card-body {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 0 2px 2px;
}

/* 姓名按钮：单行居中，过长省略 */
.person-card-name-btn {
  width: 100%;
  border: none;
  padding: 0;
  margin: 0;
  background: transparent;
  cursor: pointer;
  text-align: center;
  font-weight: 600;
  font-size: 14px;
  line-height: 1.4;
  color: var(--color-text-primary);
  border-radius: 6px;
  transition: color 0.2s ease, background 0.2s ease;
}

.person-card-name-btn:focus-visible {
  outline: 2px solid var(--color-primary, #d46b08);
  outline-offset: 2px;
}

.person-card-name-btn:hover {
  color: var(--color-primary, #d46b08);
}

.person-card-name-text {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 未命名人物：浅色但保持可辨识对比度 */
.person-card-name-btn.is-unnamed {
  color: var(--color-text-secondary);
  font-weight: 500;
}

.person-card-name-btn.is-unnamed:hover {
  color: var(--color-primary, #d46b08);
}

/* 照片数（左）+ 类别（右）同一行，纯展示不可点击 */
.person-card-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.person-card-counts {
  font-size: 12px;
  color: var(--color-text-secondary);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 类别标签：低饱和度配色，完整不省略 */
.person-card-category {
  flex-shrink: 0;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.5;
  white-space: nowrap;
}

.person-card-category.is-family {
  background: rgba(245, 108, 108, 0.12);
  color: #c45656;
}

.person-card-category.is-friend {
  background: rgba(103, 194, 58, 0.14);
  color: #5a9a3a;
}

.person-card-category.is-acquaintance {
  background: rgba(230, 162, 60, 0.14);
  color: #b8821f;
}

.person-card-category.is-stranger {
  background: rgba(144, 147, 153, 0.14);
  color: #8a8d93;
}
</style>
