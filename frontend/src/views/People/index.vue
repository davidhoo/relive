<template>
  <div ref="peoplePageRef" class="people-page">
    <el-tabs v-model="activeTab" class="people-tabs">
      <el-tab-pane label="人物列表" name="people">
        <div class="section-stack">
          <el-card v-if="mergeSuggestionVisible" shadow="never" class="section-card merge-suggestion-card-wrap animate-fade-in" :class="{ 'is-collapsed': mergeSuggestionCollapsed }">
            <template #header>
              <SectionHeader :icon="Connection" :title="`人物合并建议（待审核 ${mergeSuggestionTotal}）`">
                <template #actions>
                  <el-button size="small" plain class="mini-action-btn" v-show="!mergeSuggestionCollapsed" @click="loadMergeSuggestions">刷新</el-button>
                  <el-button text size="small" @click="toggleMergeSuggestionCollapsed" class="collapse-btn">
                    <el-icon :class="{ 'is-collapsed': mergeSuggestionCollapsed }"><ArrowUp /></el-icon>
                  </el-button>
                </template>
              </SectionHeader>
            </template>

            <div v-show="!mergeSuggestionCollapsed">
              <div v-loading="mergeSuggestionLoading" class="merge-suggestion-list">
                  <div v-if="mergeSuggestions.length > 0" class="merge-suggestion-grid">
                    <div v-for="suggestion in mergeSuggestions" :key="suggestion.id" class="merge-suggestion-card">
                      <div class="merge-suggestion-header">
                    <div class="merge-suggestion-target">
                      <el-avatar
                        :size="40"
                        :src="getFaceThumbnail(suggestion.target_person?.representative_face_id)"
                        class="merge-suggestion-avatar"
                      >
                        {{ getPersonAvatarFallback(suggestion.target_person || { category: suggestion.target_category_snapshot as PersonCategory }) }}
                      </el-avatar>
                      <div>
                      <div class="merge-suggestion-title">
                        {{ suggestion.target_person?.name?.trim() || `未命名人物 #${suggestion.target_person_id}` }}
                      </div>
                      <div class="merge-suggestion-subtitle">
                        {{ getPersonCategoryLabel(suggestion.target_person?.category || suggestion.target_category_snapshot) }}
                      </div>
                      </div>
                    </div>
                    <span class="merge-suggestion-score">{{ `${(suggestion.top_similarity * 100).toFixed(1)}%` }}</span>
                  </div>

                  <div class="merge-suggestion-meta">
                    <span>{{ suggestion.candidate_count }} 个候选</span>
                    <span>{{ `最高相似度 ${(suggestion.top_similarity * 100).toFixed(1)}%` }}</span>
                  </div>

                  <div class="candidate-preview-list">
                    <el-avatar
                      v-for="item in suggestion.items?.slice(0, 4) || []"
                      :key="item.candidate_person_id"
                      :size="28"
                      :src="getFaceThumbnail(item.candidate_person?.representative_face_id)"
                      class="candidate-preview"
                    >
                      {{ getPersonAvatarFallback(item.candidate_person || { category: 'stranger' }) }}
                    </el-avatar>
                  </div>

                  <div class="merge-suggestion-actions">
                    <el-button size="small" type="primary" @click="openMergeSuggestionReview(suggestion.id)">
                      审核
                    </el-button>
                  </div>
                </div>
                  </div>
                  <el-empty v-else description="当前没有待审核的人物合并建议" />
                </div>
            </div>
          </el-card>

          <el-card shadow="never" class="section-card people-list-card animate-fade-in animate-delay-1">
            <template #header>
              <SectionHeader :icon="User" :title="`人物列表（共 ${displayTotal} 人）`">
                <template #actions>
                  <div class="people-header-filters">
                    <el-input
                      v-model="filters.search"
                      clearable
                      placeholder="搜索人物姓名 / ID / 类别"
                      class="header-filter-input"
                      @keyup.enter="handleSearch"
                      @clear="handleSearch"
                    />
                    <el-select v-model="filters.category" clearable placeholder="全部类别" class="header-filter-select">
                      <el-option v-for="option in categoryOptions" :key="option.value" :label="option.label" :value="option.value" />
                    </el-select>
                    <el-select v-model="filters.visibility" class="header-filter-select visibility-select" @change="handleVisibilityFilterChange">
                      <el-option v-for="option in visibilityOptions" :key="option.value" :label="option.label" :value="option.value" />
                    </el-select>
                    <el-button size="small" type="primary" @click="handleSearch">应用筛选</el-button>
                    <el-radio-group v-model="browseMode" size="small" class="mode-toggle" @change="handleModeChange">
                      <el-radio-button value="pagination">翻页</el-radio-button>
                      <el-radio-button value="continuous">连续浏览</el-radio-button>
                    </el-radio-group>
                    <el-button
                      size="small"
                      :type="batchMode ? 'warning' : 'default'"
                      class="mini-action-btn"
                      @click="toggleBatchMode"
                    >
                      {{ batchMode ? '退出批量' : '批量管理' }}
                    </el-button>
                    <el-button size="small" plain class="mini-action-btn" @click="handleManualRefresh">刷新</el-button>
                  </div>
                </template>
              </SectionHeader>
            </template>

            <!-- 批量操作栏：仅在批量管理模式且当前列表有数据时显示 -->
            <div v-if="batchMode && currentListPeople.length > 0" class="batch-action-bar">
              <el-checkbox :model-value="allCurrentSelected" @change="allCurrentSelected ? clearSelection() : selectAllCurrent()">
                {{ allCurrentSelected ? '取消全选' : '全选当前列表' }}
              </el-checkbox>
              <span class="batch-selected-count">已选 {{ selectedCount }} 人</span>
              <span class="batch-range-tip">Shift + 点击复选框可连续选择</span>
              <div class="batch-actions">
                <el-button
                  size="small"
                  type="default"
                  :disabled="selectedCount === 0 || visibilitySubmitting"
                  :loading="visibilitySubmitting"
                  @click="handleBatchVisibility(false)"
                >
                  批量恢复
                </el-button>
                <el-button
                  size="small"
                  type="warning"
                  :disabled="selectedCount === 0 || visibilitySubmitting"
                  :loading="visibilitySubmitting"
                  @click="handleBatchVisibility(true)"
                >
                  批量隐藏
                </el-button>
              </div>
            </div>

            <!-- 翻页模式 -->
            <div v-if="browseMode === 'pagination'" v-loading="peopleLoading" class="people-grid-wrap">
              <el-empty v-if="!peopleLoading && people.length === 0" description="暂无人物数据" />

              <div v-else class="people-card-grid">
                <PersonCard
                  v-for="personItem in people"
                  :key="personItem.id"
                  :person="personItem"
                  :avatar-failed="avatarFailed"
                  :selectable="batchMode"
                  :selected="selectedIds.has(personItem.id)"
                  @detail="goToDetail"
                  @edit="openEditDialog"
                  @avatar-failed="markAvatarFailed"
                  @toggle-select="toggleSelect"
                  @visibility="handleVisibilityChange"
                />
              </div>
            </div>

            <!-- 连续浏览模式 -->
            <div v-else class="people-grid-wrap">
              <el-empty
                v-if="!continuousLoading && !continuousError && continuousPeople.length === 0"
                description="暂无人物数据"
              />

              <div v-if="continuousPeople.length > 0" class="people-card-grid">
                <PersonCard
                  v-for="personItem in continuousPeople"
                  :key="personItem.id"
                  :person="personItem"
                  :avatar-failed="avatarFailed"
                  :selectable="batchMode"
                  :selected="selectedIds.has(personItem.id)"
                  @detail="goToDetail"
                  @edit="openEditDialog"
                  @avatar-failed="markAvatarFailed"
                  @toggle-select="toggleSelect"
                  @visibility="handleVisibilityChange"
                />
              </div>

              <!-- 触底哨兵 + 状态条 -->
              <div ref="sentinelRef" class="continuous-sentinel" />

              <div v-if="continuousLoading" class="continuous-status">加载中…</div>
              <div v-else-if="continuousError" class="continuous-status continuous-error">
                加载失败，<el-button text type="primary" class="retry-link" @click="loadMoreContinuous">重试</el-button>
              </div>
              <div v-else-if="continuousFinished && continuousPeople.length > 0" class="continuous-status">
                已加载全部 {{ continuousTotal }} 人
              </div>
            </div>

            <div v-if="browseMode === 'pagination' && total > 0" class="pagination-wrap">
              <el-pagination
                background
                layout="total, sizes, prev, pager, next"
                :current-page="filters.page"
                :page-size="filters.page_size"
                :page-sizes="[10, 20, 50, 100]"
                :total="total"
                @current-change="handlePageChange"
                @size-change="handlePageSizeChange"
              />
            </div>
          </el-card>
        </div>
      </el-tab-pane>

      <el-tab-pane label="后台任务" name="task">
        <div class="section-stack">
          <!-- 后台任务治理状态条：foreground 让路 / cooldown / advisory 负载，紧凑只读 -->
          <div v-if="backgroundStatus" class="background-pressure-bar">
            <span class="pressure-chip" :class="{ 'is-foreground': backgroundStatus.foreground_active }">
              {{ backgroundStatus.foreground_active ? `前台进行中 (${backgroundStatus.foreground_count})` : '前台空闲' }}
            </span>
            <span v-if="backgroundPauseReason" class="pressure-chip is-pause">{{ backgroundPauseReason }}</span>
            <span v-if="backgroundRunningLabel" class="pressure-chip">运行: {{ backgroundRunningLabel }}</span>
            <span v-if="backgroundLoadLabel" class="pressure-chip is-load">{{ backgroundLoadLabel }}</span>
            <span v-if="!backgroundStatus.auto_tasks_enabled" class="pressure-chip is-pause">自动任务已禁用</span>
          </div>
          <el-card shadow="never" class="section-card animate-fade-in">
            <template #header>
              <SectionHeader :icon="Clock" title="Worker 控制">
                <template #actions>
                  <span class="status-pill" :class="taskMeta.type">{{ taskMeta.label }}</span>
                  <el-button size="small" plain class="mini-action-btn" @click="refreshCurrentTab">刷新</el-button>
                  <el-button
                    v-if="!workerActive"
                    size="small"
                    type="primary"
                    :loading="starting"
                    @click="handleStart"
                  >
                    启动任务
                  </el-button>
                  <el-button
                    v-else
                    size="small"
                    type="danger"
                    :loading="stopping"
                    :disabled="taskStopping"
                    @click="handleStop"
                  >
                    {{ taskStopping ? '停止中...' : '停止任务' }}
                  </el-button>
                  <el-button
                    size="small"
                    type="primary"
                    :loading="enqueueing"
                    :disabled="taskStopping"
                    @click="handleEnqueueUnprocessed"
                  >
                    检测未处理照片
                  </el-button>
                  <el-button
                    size="small"
                    type="danger"
                    plain
                    :loading="resetting"
                    :disabled="taskStopping"
                    @click="handleReset"
                  >
                    全量重建
                  </el-button>
                </template>
              </SectionHeader>
            </template>

            <div class="task-body">
              <div v-if="queuePending > 0" class="queue-progress">
                <div class="queue-progress-header">
                  <span>照片检测</span>
                  <span class="queue-progress-numbers">{{ stats.detected_photos }} / {{ stats.detected_photos + queuePending }}</span>
                </div>
                <el-progress :percentage="queueProgressPercent" :stroke-width="10" :show-text="false" />
                <div class="queue-progress-detail">
                  剩余 {{ queuePending }} 张照片<template v-if="stats.failed > 0"> · <span class="danger">失败 {{ stats.failed }}</span></template>
                </div>
              </div>

              <div v-if="clusteringPending > 0" class="queue-progress">
                <div class="queue-progress-header">
                  <span>人脸聚类</span>
                  <span class="queue-progress-numbers">{{ clusteringProgressPercent }}%</span>
                </div>
                <el-progress :percentage="clusteringProgressPercent" :stroke-width="10" :show-text="false" />
                <div class="queue-progress-detail">
                  已归类 {{ stats.total_faces - clusteringPending }} / {{ stats.total_faces }} 张人脸
                </div>
              </div>

              <div v-if="queuePending === 0 && clusteringPending === 0" class="queue-empty">
                队列已清空，等待新任务入队
              </div>

              <div class="task-summary">
                <span>已检测 <strong>{{ stats.detected_photos }}</strong> 张照片</span>
                <span v-if="stats.failed > 0"> · 失败 <strong class="danger">{{ stats.failed }}</strong></span>
              </div>
            </div>
          </el-card>

          <el-card shadow="never" class="section-card animate-fade-in animate-delay-1">
            <template #header>
              <SectionHeader :icon="Connection" title="合并建议 Worker">
                <template #actions>
                  <span class="status-pill" :class="mergeSuggestionTaskMeta.type">{{ mergeSuggestionTaskMeta.label }}</span>
                  <el-button
                    v-if="mergeSuggestionTask?.status === 'paused'"
                    size="small"
                    type="primary"
                    :loading="mergeSuggestionAction === 'resume'"
                    @click="handleResumeMergeSuggestionTask"
                  >
                    恢复巡检
                  </el-button>
                  <el-button
                    v-else
                    size="small"
                    type="warning"
                    plain
                    :loading="mergeSuggestionAction === 'pause'"
                    @click="handlePauseMergeSuggestionTask"
                  >
                    暂停巡检
                  </el-button>
                  <el-button
                    size="small"
                    type="danger"
                    plain
                    :loading="mergeSuggestionAction === 'rebuild'"
                    @click="handleRebuildMergeSuggestionTask"
                  >
                    立即重跑
                  </el-button>
                </template>
              </SectionHeader>
            </template>

            <div class="task-body">
              <div class="merge-task-stats">
                <div class="merge-stat-card">
                  <span class="merge-stat-label">待审核建议</span>
                  <strong>{{ mergeSuggestionStats.pending }}</strong>
                </div>
                <div class="merge-stat-card">
                  <span class="merge-stat-label">已应用</span>
                  <strong>{{ mergeSuggestionStats.applied }}</strong>
                </div>
                <div class="merge-stat-card">
                  <span class="merge-stat-label">已忽略</span>
                  <strong>{{ mergeSuggestionStats.dismissed }}</strong>
                </div>
                <div class="merge-stat-card">
                  <span class="merge-stat-label">待处理候选</span>
                  <strong>{{ mergeSuggestionStats.pending_items }}</strong>
                </div>
              </div>

              <div v-if="mergeSuggestionTask?.current_message" class="task-phase">
                <span class="task-phase-label">当前状态</span>
                <span class="task-phase-message">{{ mergeSuggestionTask.current_message }}</span>
              </div>

              <div class="task-summary">
                <span>累计扫描候选对 <strong>{{ mergeSuggestionTask?.processed_pairs || 0 }}</strong></span>
              </div>
            </div>
          </el-card>

          <el-card shadow="never" class="section-card animate-fade-in animate-delay-1">
            <template #header>
              <SectionHeader :icon="Document" title="人物 Worker 最近活动">
                <template #actions>
                  <el-button size="small" plain class="mini-action-btn" @click="loadTaskData">刷新</el-button>
                </template>
              </SectionHeader>
            </template>

            <div ref="logContainerRef" class="background-log-body">
              <pre v-if="backgroundLogs.length">{{ backgroundLogs.join('\n') }}</pre>
              <div v-else class="background-log-empty">暂无最近活动记录</div>
            </div>
          </el-card>

          <el-card shadow="never" class="section-card animate-fade-in animate-delay-2">
            <template #header>
              <SectionHeader :icon="Document" title="合并建议 Worker 最近活动">
                <template #actions>
                  <el-button size="small" plain class="mini-action-btn" @click="loadTaskData">刷新</el-button>
                </template>
              </SectionHeader>
            </template>

            <div ref="mergeLogContainerRef" class="background-log-body">
              <pre v-if="mergeSuggestionLogs.length">{{ mergeSuggestionLogs.join('\n') }}</pre>
              <div v-else class="background-log-empty">暂无最近活动记录</div>
            </div>
          </el-card>
        </div>
      </el-tab-pane>
    </el-tabs>

    <MergeSuggestionReviewDialog
      v-model="mergeSuggestionDialogVisible"
      :suggestion="currentMergeSuggestion"
      :loading="mergeSuggestionDetailLoading"
      :submitting="mergeSuggestionSubmitting"
      @exclude="handleExcludeMergeSuggestion"
      @apply="handleApplyMergeSuggestion"
    />

    <PersonEditDialog
      v-model="editDialogVisible"
      :person="editingPerson"
      :loading="editSaving"
      @submit="handleEditSubmit"
      @merge="handleEditMergeRequest"
    />

    <PersonMergeConfirmDialog
      v-model="mergeConfirmVisible"
      :source="editingPerson"
      :target="mergeTarget"
      :loading="mergeSubmitting"
      :error="mergeError"
      @confirm="handleMergeConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowUp, Clock, Connection, Document, User } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import SectionHeader from '@/components/SectionHeader.vue'
import { peopleApi } from '@/api/people'
import { backgroundApi } from '@/api/background'
import type {
  PeopleStats,
  PeopleTask,
  PeopleVisibility,
  Person,
  PersonCategory,
  PersonMergeSuggestion,
  PersonMergeSuggestionStats,
  PersonMergeSuggestionTask,
} from '@/types/people'
import type { BackgroundTaskStatus } from '@/types/background'
import MergeSuggestionReviewDialog from './MergeSuggestionReviewDialog.vue'
import PersonCard from './PersonCard.vue'
import PersonEditDialog from './PersonEditDialog.vue'
import PersonMergeConfirmDialog from './PersonMergeConfirmDialog.vue'
import {
  getMergeSuggestionTaskStatusMeta,
  getMergeSuggestionVisibility,
  getPeopleTaskStatusMeta,
  getPersonAvatarFallback,
  getPersonCategoryLabel,
} from './peopleHelpers'
import {
  type BrowseMode,
  clearContinuousSnapshot,
  getContinuousSnapshot,
  isContinuousSnapshotUsable,
  loadBrowseMode,
  saveBrowseMode,
  saveContinuousSnapshot,
} from './peopleListViewState'

const route = useRoute()
const router = useRouter()
const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const peoplePageRef = ref<HTMLElement | null>(null)

const activeTab = ref<'people' | 'task'>('people')
const peopleLoading = ref(false)
const task = ref<PeopleTask | null>(null)
const stats = ref<PeopleStats>({
  total: 0,
  pending: 0,
  queued: 0,
  processing: 0,
  completed: 0,
  failed: 0,
  cancelled: 0,
  pending_faces_total: 0,
  pending_faces_never_clustered: 0,
  pending_faces_retried: 0,
  total_faces: 0,
  detected_photos: 0,
  pending_photos: 0,
})
const backgroundLogs = ref<string[]>([])
const people = ref<Person[]>([])
const total = ref(0)

// 合并建议折叠状态：从 localStorage 读取，无记录时默认展开（与扫描路径一致）
const mergeSuggestionCollapsed = ref(localStorage.getItem('people_mergeSuggestions_collapsed') === 'true')

const toggleMergeSuggestionCollapsed = () => {
  mergeSuggestionCollapsed.value = !mergeSuggestionCollapsed.value
  localStorage.setItem('people_mergeSuggestions_collapsed', String(mergeSuggestionCollapsed.value))
}

// 浏览模式：翻页 / 连续浏览，记忆用户最后选择
const browseMode = ref<BrowseMode>(loadBrowseMode())

// 连续浏览模式状态
const CONTINUOUS_PAGE_SIZE = 50
const continuousPeople = ref<Person[]>([])
const continuousPage = ref(1)
const continuousTotal = ref(0)
const continuousFinished = ref(false)
const continuousLoading = ref(false)
const continuousError = ref(false)
// 请求代际：切换筛选时递增，用于丢弃过期请求结果，避免旧数据覆盖新筛选结果
const requestEpoch = ref(0)
const sentinelRef = ref<HTMLElement | null>(null)
let scrollObserver: IntersectionObserver | null = null

// 头像加载失败的 faceId 集合，失败后显示兜底内容
const avatarFailed = ref(new Set<number>())
const markAvatarFailed = (faceId: number) => {
  avatarFailed.value.add(faceId)
}

// 批量管理模式：卡片显示复选框，可批量隐藏/恢复
const batchMode = ref(false)
const selectedIds = ref(new Set<number>())
// Shift 连选锚点：普通点击选中时建立，区间选择时作为起点
const selectionAnchorId = ref<number | null>(null)
const visibilitySubmitting = ref(false)

const clearSelection = () => {
  selectedIds.value = new Set()
  selectionAnchorId.value = null
}

/**
 * 将锚点到目标人物之间（闭区间）的所有当前列表人物加入选择，
 * 保留区间外已有选择，不取消任何项。锚点无效时返回 false。
 */
const selectRangeFromAnchor = (targetPersonId: number): boolean => {
  const anchorId = selectionAnchorId.value
  if (anchorId === null) return false
  const list = currentListPeople.value
  const anchorIndex = list.findIndex(person => person.id === anchorId)
  const targetIndex = list.findIndex(person => person.id === targetPersonId)
  if (anchorIndex === -1 || targetIndex === -1) return false
  const start = Math.min(anchorIndex, targetIndex)
  const end = Math.max(anchorIndex, targetIndex)
  const next = new Set(selectedIds.value)
  for (let i = start; i <= end; i++) {
    const person = list[i]
    if (person) {
      next.add(person.id)
    }
  }
  selectedIds.value = next
  return true
}

const toggleSelect = (personId: number, shiftKey = false) => {
  if (shiftKey && selectRangeFromAnchor(personId)) {
    // 区间连选成功：保留原锚点，不改变单选状态
    return
  }
  const next = new Set(selectedIds.value)
  if (next.has(personId)) {
    next.delete(personId)
    // 取消的是当前锚点时同时清除锚点
    if (selectionAnchorId.value === personId) {
      selectionAnchorId.value = null
    }
  } else {
    next.add(personId)
    // 新选中的人物成为连选锚点
    selectionAnchorId.value = personId
  }
  selectedIds.value = next
}

const selectedCount = computed(() => selectedIds.value.size)

// 当前列表中可见（已加载）的人物，用于全选范围
const currentListPeople = computed(() =>
  browseMode.value === 'continuous' ? continuousPeople.value : people.value,
)

const allCurrentSelected = computed(
  () => currentListPeople.value.length > 0 && currentListPeople.value.every(p => selectedIds.value.has(p.id)),
)

const selectAllCurrent = () => {
  const next = new Set(selectedIds.value)
  for (const p of currentListPeople.value) {
    next.add(p.id)
  }
  selectedIds.value = next
  // 全选不产生明确起点，清除锚点避免后续 Shift 含糊范围
  selectionAnchorId.value = null
}

const toggleBatchMode = () => {
  batchMode.value = !batchMode.value
  if (!batchMode.value) {
    clearSelection()
  }
}

/**
 * 切换可见性筛选 / 搜索 / 类别 / 浏览模式时清空批量选择，
 * 避免选中项与当前列表不再对应。
 */
const onFilterChange = () => {
  clearSelection()
}

/**
 * 人物隐藏状态变更后是否仍应保留在当前列表中。
 * visible 列表只保留显示中；hidden 列表只保留已隐藏；all 列表始终保留（仅翻转标记）。
 */
const belongsToCurrentVisibility = (person: Person, newHidden: boolean) => {
  switch (filters.visibility) {
    case 'visible':
      return !newHidden
    case 'hidden':
      return newHidden
    default:
      return true
  }
}

const mergeSuggestionTask = ref<PersonMergeSuggestionTask | null>(null)
const mergeSuggestionStats = ref<PersonMergeSuggestionStats>({
  total: 0,
  pending: 0,
  applied: 0,
  dismissed: 0,
  obsolete: 0,
  pending_items: 0,
  excluded_items: 0,
  merged_items: 0,
})
const mergeSuggestionLogs = ref<string[]>([])
const mergeSuggestions = ref<PersonMergeSuggestion[]>([])
const mergeSuggestionTotal = ref(0)
const mergeSuggestionLoading = ref(false)
const mergeSuggestionDialogVisible = ref(false)
const mergeSuggestionDetailLoading = ref(false)
const mergeSuggestionSubmitting = ref(false)
const currentMergeSuggestion = ref<PersonMergeSuggestion | null>(null)
const currentMergeSuggestionId = ref<number | null>(null)
const mergeSuggestionAction = ref<'pause' | 'resume' | 'rebuild' | ''>('')

const starting = ref(false)
const stopping = ref(false)
const resetting = ref(false)
const enqueueing = ref(false)
const logContainerRef = ref<HTMLElement | null>(null)
const mergeLogContainerRef = ref<HTMLElement | null>(null)
let taskTimer: number | null = null
// 后台任务数据按需懒加载保护：避免轮询、Tab 切换、操作回调并发触发重复请求
const taskDataLoading = ref(false)

// 后台任务治理状态（前台/后台让路 + advisory 负载）。仅 People 页面打开时轮询，最多 30s 一次。
const backgroundStatus = ref<BackgroundTaskStatus | null>(null)
let backgroundStatusTimer: number | null = null
const backgroundStatusLoading = ref(false)

const loadBackgroundStatus = async () => {
  if (backgroundStatusLoading.value) return
  backgroundStatusLoading.value = true
  try {
    const res = await backgroundApi.getStatus()
    backgroundStatus.value = res.data?.data || null
  } catch {
    // 静默失败：状态条是 advisory，不阻塞主流程
  } finally {
    backgroundStatusLoading.value = false
  }
}

// 紧凑状态条展示用 computed
const backgroundRunningLabel = computed(() => {
  const running = backgroundStatus.value?.running || []
  if (running.length === 0) return ''
  return running.map(r => r.class).join(', ')
})
const backgroundPauseReason = computed(() => {
  const s = backgroundStatus.value
  if (!s) return ''
  if (s.foreground_active) return '前台操作进行中'
  const classes = Object.keys(s.cooldowns || {})
  if (classes.length > 0) return `${classes.length} 项冷却中`
  if (!s.auto_tasks_enabled) return '自动任务已禁用'
  return ''
})
const backgroundLoadLabel = computed(() => {
  const load = backgroundStatus.value?.load
  if (!load) return ''
  const parts: string[] = []
  if (load.cpu_user_pct >= 0) parts.push(`CPU ${Math.round(load.cpu_user_pct)}%`)
  if (load.cpu_iowait_pct >= 0) parts.push(`IO ${Math.round(load.cpu_iowait_pct)}%`)
  if (load.mem_used_pct >= 0) parts.push(`MEM ${Math.round(load.mem_used_pct)}%`)
  return parts.join(' · ')
})

const workerActive = computed(() => {
  const s = task.value?.status
  return s === 'running' || s === 'idle' || s === 'stopping'
})
const taskStopping = computed(() => task.value?.status === 'stopping')

const queuePending = computed(() => stats.value.pending + stats.value.queued + stats.value.processing)
const clusteringPending = computed(() => stats.value.pending_faces_total)
const clusteringProgressPercent = computed(() => {
  const total = stats.value.total_faces
  if (total === 0) return 0
  const clustered = total - clusteringPending.value
  return Math.round((clustered / total) * 100)
})
const queueProgressPercent = computed(() => {
  const done = stats.value.detected_photos
  const totalCount = done + queuePending.value
  if (totalCount === 0) return 0
  return Math.round((done / totalCount) * 100)
})

const mergeSuggestionVisible = computed(() => getMergeSuggestionVisibility(mergeSuggestionTotal.value, mergeSuggestionLoading.value))
const mergeSuggestionTaskMeta = computed(() => getMergeSuggestionTaskStatusMeta(mergeSuggestionTask.value))

const parseVisibility = (raw: unknown): PeopleVisibility => {
  return raw === 'hidden' || raw === 'all' ? (raw as PeopleVisibility) : 'visible'
}

const filters = reactive<{
  page: number
  page_size: number
  search: string
  category?: PersonCategory
  visibility: PeopleVisibility
}>({
  page: Number(route.query.page) || 1,
  page_size: Number(route.query.page_size) || 20,
  search: (route.query.search as string) || '',
  category: (route.query.category as PersonCategory) || undefined,
  visibility: parseVisibility(route.query.visibility),
})

const syncFiltersToQuery = () => {
  const query: Record<string, string> = {}
  if (filters.page > 1) query.page = String(filters.page)
  if (filters.page_size !== 20) query.page_size = String(filters.page_size)
  if (filters.search) query.search = filters.search
  if (filters.category) query.category = filters.category
  if (filters.visibility !== 'visible') query.visibility = filters.visibility
  router.replace({ query })
}

const visibilityOptions = [
  { label: '显示中', value: 'visible' as PeopleVisibility },
  { label: '已隐藏', value: 'hidden' as PeopleVisibility },
  { label: '全部', value: 'all' as PeopleVisibility },
]

const categoryOptions = [
  { label: '家人', value: 'family' },
  { label: '亲友', value: 'friend' },
  { label: '熟人', value: 'acquaintance' },
  { label: '路人', value: 'stranger' },
] satisfies Array<{ label: string; value: PersonCategory }>

const taskMeta = computed(() => getPeopleTaskStatusMeta(task.value))
const taskPhaseLabel = computed(() => {
  switch (task.value?.current_phase) {
    case 'clustering':
      return '聚类阶段'
    case 'detecting':
      return '检测阶段'
    default:
      return '当前状态'
  }
})

const getFaceThumbnail = (faceId?: number) => {
  if (!faceId) return ''
  return `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`
}

const displayTotal = computed(() =>
  browseMode.value === 'continuous' ? continuousTotal.value : total.value,
)

/**
 * 获取内容区滚动容器（el-main）。连续浏览模式下用于触底检测与滚动位置恢复。
 */
const getScrollContainer = (): HTMLElement | null => {
  let el: HTMLElement | null = peoplePageRef.value
  while (el && el !== document.body) {
    if (el.scrollHeight > el.clientHeight && getComputedStyle(el).overflowY !== 'visible') {
      return el
    }
    el = el.parentElement
  }
  return document.scrollingElement as HTMLElement | null
}

const loadPeople = async () => {
  peopleLoading.value = true
  syncFiltersToQuery()
  try {
    const res = await peopleApi.getList({
      page: filters.page,
      page_size: filters.page_size,
      search: filters.search || undefined,
      category: filters.category,
      visibility: filters.visibility,
    })
    const payload = res.data?.data
    people.value = payload?.items || []
    total.value = payload?.total || 0
  } catch (error: any) {
    ElMessage.error(error.message || '加载人物列表失败')
  } finally {
    peopleLoading.value = false
  }
}

const loadMergeSuggestions = async (silent = false) => {
  if (!silent) {
    mergeSuggestionLoading.value = true
  }
  try {
    const res = await peopleApi.listMergeSuggestions({ page: 1, page_size: 12 })
    const payload = res.data?.data
    if (payload) {
      const newTotal = payload.total || 0
      const newItems = payload.items || []
      // silent 模式下检测变化，无变化则跳过更新
      if (silent) {
        const newIds = newItems.map((item: PersonMergeSuggestion) => item.id).join(',')
        const oldIds = mergeSuggestions.value.map(item => item.id).join(',')
        if (newTotal === mergeSuggestionTotal.value && newIds === oldIds) {
          return
        }
      }
      mergeSuggestions.value = newItems
      mergeSuggestionTotal.value = newTotal
    }
  } catch (error: any) {
    if (!silent) {
      ElMessage.error(error.message || '加载人物合并建议失败')
    }
  } finally {
    if (!silent) {
      mergeSuggestionLoading.value = false
    }
  }
}

const loadMergeSuggestionTaskData = async () => {
  const [taskRes, statsRes, logsRes] = await Promise.all([
    peopleApi.getMergeSuggestionTask(),
    peopleApi.getMergeSuggestionStats(),
    peopleApi.getMergeSuggestionLogs(),
  ])
  mergeSuggestionTask.value = taskRes.data?.data || null
  mergeSuggestionStats.value = statsRes.data?.data || mergeSuggestionStats.value
  mergeSuggestionLogs.value = logsRes.data?.data?.lines || []
}

const loadTaskData = async () => {
  // 请求进行中保护：快速切换 Tab 或轮询叠加时跳过，避免重叠/成倍并发请求
  if (taskDataLoading.value) return
  taskDataLoading.value = true
  try {
    const [taskRes, statsRes, logsRes] = await Promise.all([
      peopleApi.getTask(),
      peopleApi.getStats(),
      peopleApi.getBackgroundLogs(),
    ])
    task.value = taskRes.data?.data || null
    stats.value = statsRes.data?.data || stats.value
    backgroundLogs.value = logsRes.data?.data?.lines || []
    await loadMergeSuggestionTaskData()
  } catch (error: any) {
    ElMessage.error(error.message || '加载人物任务状态失败')
  } finally {
    taskDataLoading.value = false
  }
}

const loadMergeSuggestionDetail = async (id: number, silent = false) => {
  mergeSuggestionDetailLoading.value = true
  try {
    const res = await peopleApi.getMergeSuggestion(id)
    currentMergeSuggestion.value = res.data?.data || null
    currentMergeSuggestionId.value = currentMergeSuggestion.value?.id || null
  } catch (error: any) {
    currentMergeSuggestion.value = null
    currentMergeSuggestionId.value = null
    if (!silent) {
      ElMessage.error(error.response?.data?.error?.message || error.message || '加载建议详情失败')
    }
  } finally {
    mergeSuggestionDetailLoading.value = false
  }
}

const handleSearch = async () => {
  filters.page = 1
  avatarFailed.value = new Set()
  onFilterChange()
  if (browseMode.value === 'continuous') {
    // 切换筛选条件后清空旧数据，从第一页重新加载
    resetContinuousList()
    await loadMoreContinuous()
    return
  }
  await loadPeople()
}

const handleVisibilityFilterChange = async () => {
  filters.page = 1
  avatarFailed.value = new Set()
  onFilterChange()
  if (browseMode.value === 'continuous') {
    clearContinuousSnapshot()
    resetContinuousList()
    await loadMoreContinuous()
    return
  }
  await loadPeople()
}

const handlePageChange = async (page: number) => {
  filters.page = page
  // 翻页可保留已选，但清除锚点，防止 Shift 在不同页面间产生含糊范围
  selectionAnchorId.value = null
  await loadPeople()
}

const handlePageSizeChange = async (pageSize: number) => {
  filters.page_size = pageSize
  filters.page = 1
  selectionAnchorId.value = null
  await loadPeople()
}

const handleManualRefresh = async () => {
  avatarFailed.value = new Set()
  // 手动刷新列表：清除连选锚点，防止刷新后 Shift 产生含糊范围
  selectionAnchorId.value = null
  if (browseMode.value === 'continuous') {
    resetContinuousList()
    await loadMoreContinuous()
    return
  }
  await loadPeople()
}

/**
 * 重置连续浏览列表为空，并递增请求代际，使在途请求结果作废。
 */
const resetContinuousList = () => {
  requestEpoch.value += 1
  continuousPeople.value = []
  continuousPage.value = 1
  continuousTotal.value = 0
  continuousFinished.value = false
  continuousError.value = false
  continuousLoading.value = false
}

/**
 * 加载连续浏览的下一批人物。
 * - 通过 requestEpoch 丢弃过期请求，避免快速切换筛选时旧结果覆盖新结果。
 * - 追加时按人物 ID 去重，防止同一页重复追加。
 */
const loadMoreContinuous = async () => {
  if (continuousLoading.value || continuousFinished.value) return
  continuousLoading.value = true
  continuousError.value = false
  const myEpoch = requestEpoch.value
  const page = continuousPage.value
  try {
    const res = await peopleApi.getList({
      page,
      page_size: CONTINUOUS_PAGE_SIZE,
      search: filters.search || undefined,
      category: filters.category,
      visibility: filters.visibility,
    })
    // 请求返回后若代际已变（筛选已切换），丢弃结果
    if (myEpoch !== requestEpoch.value) return
    const payload = res.data?.data
    const items = payload?.items || []
    const totalCount = payload?.total || 0
    const existing = new Set(continuousPeople.value.map(person => person.id))
    const fresh = items.filter(person => !existing.has(person.id))
    continuousPeople.value = [...continuousPeople.value, ...fresh]
    continuousTotal.value = totalCount
    continuousPage.value = page + 1
    // 本页返回少于每页数量，或已加载达到总数，视为加载完毕
    if (items.length < CONTINUOUS_PAGE_SIZE || continuousPeople.value.length >= totalCount) {
      continuousFinished.value = true
    }
  } catch (error: any) {
    if (myEpoch !== requestEpoch.value) return
    continuousError.value = true
    ElMessage.error(error.message || '加载人物列表失败')
  } finally {
    if (myEpoch === requestEpoch.value) {
      continuousLoading.value = false
    }
  }
}

/**
 * 进入连续浏览模式：若存在与当前筛选匹配的快照（从详情页返回），恢复已加载列表与滚动位置；
 * 否则从第一页开始加载。
 */
const initContinuousMode = async () => {
  if (isContinuousSnapshotUsable(filters.search, filters.category, filters.visibility, CONTINUOUS_PAGE_SIZE)) {
    const snap = getContinuousSnapshot()
    continuousPeople.value = [...snap.items]
    continuousPage.value = snap.nextPage
    continuousTotal.value = snap.total
    continuousFinished.value = snap.finished
    continuousError.value = false
    continuousLoading.value = false
    // 恢复滚动位置（等待 DOM 渲染完成）
    await nextTick()
    const container = getScrollContainer()
    if (container && snap.scrollTop > 0) {
      container.scrollTop = snap.scrollTop
    }
    // 若恢复后仍未加载完且哨兵可见，继续加载
    setupScrollObserver()
    return
  }
  clearContinuousSnapshot()
  resetContinuousList()
  await loadMoreContinuous()
  setupScrollObserver()
}

const handleModeChange = async (mode: BrowseMode | string) => {
  const nextMode = mode as BrowseMode
  // 切换模式前先拆除旧的触底监听，避免悬挂在已卸载的哨兵节点上
  teardownScrollObserver()
  saveBrowseMode(nextMode)
  avatarFailed.value = new Set()
  onFilterChange()
  if (nextMode === 'continuous') {
    await initContinuousMode()
  } else {
    // 切回翻页模式：加载当前分页
    await loadPeople()
  }
}

/**
 * 连续浏览模式下，触底哨兵进入视口时加载下一批。
 */
const setupScrollObserver = () => {
  teardownScrollObserver()
  if (browseMode.value !== 'continuous') return
  const container = getScrollContainer()
  if (!container || !sentinelRef.value) {
    // 容器或哨兵尚未就绪，稍后重试
    return
  }
  scrollObserver = new IntersectionObserver(
    entries => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          void loadMoreContinuous()
        }
      }
    },
    { root: container, rootMargin: '200px' },
  )
  scrollObserver.observe(sentinelRef.value)
}

const teardownScrollObserver = () => {
  if (scrollObserver) {
    scrollObserver.disconnect()
    scrollObserver = null
  }
}

const goToDetail = (personId: number) => {
  // 连续浏览模式：离开前保存已加载列表与滚动位置，便于返回恢复
  if (browseMode.value === 'continuous') {
    const container = getScrollContainer()
    saveContinuousSnapshot({
      items: continuousPeople.value,
      nextPage: continuousPage.value,
      total: continuousTotal.value,
      finished: continuousFinished.value,
      search: filters.search,
      category: filters.category,
      visibility: filters.visibility,
      pageSize: CONTINUOUS_PAGE_SIZE,
      scrollTop: container?.scrollTop ?? 0,
    })
  }
  router.push({
    path: `/people/${personId}`,
    query: { ...route.query }
  })
}

// 人物信息快捷编辑弹框
const editDialogVisible = ref(false)
const editingPerson = ref<Person | null>(null)
const editSaving = ref(false)

const openEditDialog = (person: Person) => {
  editingPerson.value = person
  editDialogVisible.value = true
}

/**
 * 将编辑结果同步到翻页与连续浏览两份列表，不重置已加载数据与滚动位置。
 * 若类别变更后不再符合当前类别筛选，则将该人物从列表移除并更新显示数量。
 */
const applyPersonEdit = (
  personId: number,
  updates: { name?: string; category?: PersonCategory },
) => {
  const patch = (person: Person): Person => (person.id === personId ? { ...person, ...updates } : person)
  people.value = people.value.map(patch)
  continuousPeople.value = continuousPeople.value.map(patch)

  if (updates.category !== undefined && filters.category && updates.category !== filters.category) {
    const inPeople = people.value.some(person => person.id === personId)
    const inContinuous = continuousPeople.value.some(person => person.id === personId)
    if (inPeople) {
      people.value = people.value.filter(person => person.id !== personId)
      total.value = Math.max(0, total.value - 1)
    }
    if (inContinuous) {
      continuousPeople.value = continuousPeople.value.filter(person => person.id !== personId)
      continuousTotal.value = Math.max(0, continuousTotal.value - 1)
    }
  }
}

const handleEditSubmit = async (payload: { name?: string; category?: PersonCategory }) => {
  const person = editingPerson.value
  if (!person) return
  editSaving.value = true
  try {
    const tasks: Promise<unknown>[] = []
    if (payload.name !== undefined) {
      tasks.push(peopleApi.updateName(person.id, payload.name))
    }
    if (payload.category !== undefined) {
      tasks.push(peopleApi.updateCategory(person.id, payload.category))
    }
    await Promise.all(tasks)
    applyPersonEdit(person.id, payload)
    ElMessage.success('人物信息已更新')
    editDialogVisible.value = false
  } catch (error: any) {
    // 保留弹框内容，仅提示错误，便于用户重试
    ElMessage.error(error.response?.data?.error?.message || error.message || '保存失败')
  } finally {
    editSaving.value = false
  }
}

// ---- 编辑弹框内发起的人物合并 ----
// 合并方向固定：当前人物（来源）→ 搜索结果中所选人物（目标）
const mergeConfirmVisible = ref(false)
const mergeTarget = ref<Person | null>(null)
const mergeSubmitting = ref(false)
const mergeError = ref('')

/**
 * 编辑弹框中点击搜索结果：不自动保存编辑内容，打开合并确认弹框。
 * 来源人物为当前编辑的人物（使用已保存信息，未提交的姓名/类别修改不参与合并）。
 */
const handleEditMergeRequest = (target: Person) => {
  mergeTarget.value = target
  mergeError.value = ''
  mergeConfirmVisible.value = true
}

/**
 * 将合并结果同步到翻页与连续浏览两份列表：
 * - 移除来源人物；
 * - 若目标人物已在列表中，刷新其照片数/人脸数统计；
 * - 不重置已加载数据、筛选条件与滚动位置。
 */
const applyPersonMerge = async (sourceId: number, targetId: number) => {
  const removeSource = (list: Person[]) => list.filter(person => person.id !== sourceId)
  const sourceInPeople = people.value.some(person => person.id === sourceId)
  const sourceInContinuous = continuousPeople.value.some(person => person.id === sourceId)
  people.value = removeSource(people.value)
  continuousPeople.value = removeSource(continuousPeople.value)
  if (sourceInPeople) {
    total.value = Math.max(0, total.value - 1)
  }
  if (sourceInContinuous) {
    continuousTotal.value = Math.max(0, continuousTotal.value - 1)
  }

  // 目标人物可能在当前列表中：拉取最新统计并就地更新
  const targetInPeople = people.value.some(person => person.id === targetId)
  const targetInContinuous = continuousPeople.value.some(person => person.id === targetId)
  if (targetInPeople || targetInContinuous) {
    try {
      const res = await peopleApi.getById(targetId)
      const updated = res.data?.data
      if (updated) {
        const patchTarget = (person: Person): Person =>
          person.id === targetId
            ? { ...person, photo_count: updated.photo_count, face_count: updated.face_count }
            : person
        people.value = people.value.map(patchTarget)
        continuousPeople.value = continuousPeople.value.map(patchTarget)
      }
    } catch {
      // 统计刷新失败不影响合并已完成的事实，静默忽略
    }
  }
}

/**
 * 轮询合并任务直到完成/失败/超时。
 * - completed: 返回 true
 * - failed: 返回 { failed: true, message }，调用方保留弹框并展示
 * - 超时（状态未知）: 返回 'timeout'
 * 轮询单次网络错误不计为失败，继续重试。
 */
const pollMergeJob = async (
  jobId: number,
): Promise<boolean | 'timeout' | { failed: true; message: string }> => {
  const maxPolls = 60
  for (let i = 0; i < maxPolls; i++) {
    await new Promise(resolve => setTimeout(resolve, 2000))
    let job
    try {
      const res = await peopleApi.getMergeJob(jobId)
      job = res.data?.data
    } catch {
      // 网络抖动：继续重试
      continue
    }
    if (!job) return 'timeout'
    if (job.status === 'completed') return true
    if (job.status === 'failed') {
      return { failed: true, message: job.error_message || '合并任务失败' }
    }
    // pending / processing：继续轮询
  }
  return 'timeout'
}

/**
 * 确认合并：调用现有异步合并接口并轮询任务状态。
 * - 提交期间禁用取消与重复确认；
 * - 合并成功前不提前提示成功；
 * - 失败/超时保留确认弹框，展示错误，且不从列表移除当前人物。
 */
const handleMergeConfirm = async () => {
  const source = editingPerson.value
  const target = mergeTarget.value
  if (!source || !target) return
  mergeSubmitting.value = true
  mergeError.value = ''
  try {
    const res = await peopleApi.merge(target.id, [source.id])
    const jobId = res.data?.data?.job_id
    if (!jobId) {
      // 未返回任务 ID：视为未知状态，保留弹框，不移除人物
      mergeError.value = '合并任务未返回任务 ID，请稍后刷新页面查看结果'
      return
    }
    const result = await pollMergeJob(jobId)
    if (result === 'timeout') {
      // 状态未知/超时：保留弹框，展示提示，不移除当前人物
      mergeError.value = '合并任务超时，请稍后刷新页面查看结果'
      return
    }
    if (typeof result === 'object' && result.failed) {
      // 合并失败：保留确认弹框，展示后端错误信息
      mergeError.value = result.message
      return
    }
    // 合并成功：关闭两个弹框，更新列表，刷新合并建议，提示
    mergeConfirmVisible.value = false
    editDialogVisible.value = false
    await applyPersonMerge(source.id, target.id)
    // 刷新合并建议，避免继续显示已失效的建议
    void loadMergeSuggestions(true)
    ElMessage.success('人物已合并')
  } catch (error: any) {
    // 提交合并请求本身失败：保留确认弹框，展示后端错误信息
    mergeError.value = error.response?.data?.error?.message || error.message || '合并人物失败'
  } finally {
    mergeSubmitting.value = false
  }
}

/**
 * 单个人物隐藏/恢复：本地翻转标记或移除，不重置列表与滚动位置。
 * 操作可逆，无需二次确认。成功后刷新当前列表并提示结果。
 */
const handleVisibilityChange = async (personId: number, hidden: boolean) => {
  try {
    await peopleApi.updateVisibility([personId], hidden)
    // 翻转标记或按可见性筛选移除
    const patch = (list: Person[]) =>
      list
        .map(person => (person.id === personId ? { ...person, hidden } : person))
        .filter(person => !(person.id === personId && !belongsToCurrentVisibility(person, hidden)))
    const beforePeople = people.value.length
    const beforeContinuous = continuousPeople.value.length
    people.value = patch(people.value)
    continuousPeople.value = patch(continuousPeople.value)
    if (people.value.length < beforePeople) {
      total.value = Math.max(0, total.value - 1)
    }
    if (continuousPeople.value.length < beforeContinuous) {
      continuousTotal.value = Math.max(0, continuousTotal.value - 1)
    }
    // 人物移出当前列表后清理其勾选状态与连选锚点，避免悬挂选择
    if (!belongsToCurrentVisibility({ id: personId } as Person, hidden)) {
      if (selectedIds.value.has(personId)) {
        const next = new Set(selectedIds.value)
        next.delete(personId)
        selectedIds.value = next
      }
      if (selectionAnchorId.value === personId) {
        selectionAnchorId.value = null
      }
    }
    ElMessage.success(hidden ? '已隐藏该人物' : '已恢复显示')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '操作失败')
  }
}

/**
 * 批量隐藏/恢复：操作前显示确认弹窗及人物数量。
 * 成功后清空选择并重新加载列表（翻页重载当前页，连续浏览重置到第一页，避免 offset 漏项）。
 * 失败时保留列表及勾选状态，允许重试。
 */
const handleBatchVisibility = async (hidden: boolean) => {
  const ids = Array.from(selectedIds.value)
  if (ids.length === 0) return
  const action = hidden ? '隐藏' : '恢复显示'
  try {
    await ElMessageBox.confirm(
      `确定要${action}选中的 ${ids.length} 个人物吗？`,
      `批量${action}确认`,
      { confirmButtonText: `确认${action}`, cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  visibilitySubmitting.value = true
  try {
    const res = await peopleApi.updateVisibility(ids, hidden)
    const updated = res.data?.data?.updated ?? 0
    ElMessage.success(`已${action} ${updated} 个人物`)
    clearSelection()
    await refreshPeopleForCurrentMode()
  } catch (error: any) {
    // 失败时保留列表及勾选状态，允许重试
    ElMessage.error(error.response?.data?.error?.message || error.message || '批量操作失败')
  } finally {
    visibilitySubmitting.value = false
  }
}

const refreshCurrentTab = async () => {
  if (activeTab.value === 'task') {
    await loadTaskData()
    return
  }
  avatarFailed.value = new Set()
  if (browseMode.value === 'continuous') {
    resetContinuousList()
    await Promise.all([loadMoreContinuous(), loadMergeSuggestions()])
    return
  }
  await Promise.all([loadPeople(), loadMergeSuggestions()])
}

const handleStart = async () => {
  starting.value = true
  try {
    await peopleApi.startBackground()
    ElMessage.success('人物后台任务已启动')
    await loadTaskData()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '启动失败')
  } finally {
    starting.value = false
  }
}

const handleStop = async () => {
  stopping.value = true
  try {
    await peopleApi.stopBackground()
    ElMessage.success('停止请求已发送')
    await loadTaskData()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '停止失败')
  } finally {
    stopping.value = false
  }
}

const handleReset = async () => {
  try {
    await ElMessageBox.confirm(
      '全量重建将清除所有人物数据（人物、人脸、聚类结果），并重新对所有照片进行人脸检测与聚类。此操作不可撤销，确定继续？',
      '全量重建确认',
      { confirmButtonText: '确认重建', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  resetting.value = true
  try {
    const res = await peopleApi.resetAllPeople()
    const data = res.data?.data
    ElMessage.success(`人物数据已重置，已入队 ${data?.photos_enqueued || 0} 张照片`)
    await loadTaskData()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '重建失败')
  } finally {
    resetting.value = false
  }
}

const handleEnqueueUnprocessed = async () => {
  enqueueing.value = true
  try {
    const res = await peopleApi.enqueueUnprocessed()
    const data = res.data?.data
    ElMessage.success(`已入队 ${data?.enqueued || 0} 张未处理照片`)
    await loadTaskData()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '入队失败')
  } finally {
    enqueueing.value = false
  }
}

const openMergeSuggestionReview = async (id: number) => {
  mergeSuggestionDialogVisible.value = true
  currentMergeSuggestion.value = null
  currentMergeSuggestionId.value = id
  await loadMergeSuggestionDetail(id, true)
  if (!currentMergeSuggestion.value) {
    mergeSuggestionDialogVisible.value = false
    await loadMergeSuggestions()
  }
}

const reloadMergeSuggestionReviewState = async (shouldCloseOnComplete = false) => {
  // 合并建议审核弹窗仅在人物列表 Tab 打开；未进入后台任务 Tab 时不触发后台任务请求，
  // 待用户切回后台任务 Tab 时由 watch 重新加载
  const tasks: Promise<unknown>[] = [loadMergeSuggestions(), refreshPeopleForCurrentMode()]
  if (activeTab.value === 'task') {
    tasks.push(loadTaskData())
  }
  await Promise.all(tasks)
  if (!currentMergeSuggestionId.value) {
    return
  }
  // 操作完成后直接关闭对话框（避免已合并建议返回 404）
  if (shouldCloseOnComplete) {
    mergeSuggestionDialogVisible.value = false
    return
  }
  // 静默加载详情，避免合并完成后 404 报错
  await loadMergeSuggestionDetail(currentMergeSuggestionId.value, true)
  if (!currentMergeSuggestion.value || !currentMergeSuggestion.value.items?.length) {
    mergeSuggestionDialogVisible.value = false
  }
}

/**
 * 合并/审核后刷新人物列表：翻页模式重新加载当前页，连续浏览模式重置并从第一页加载。
 * 连续浏览下若已保存快照（从详情返回场景）一并清除，避免恢复过期数据。
 */
const refreshPeopleForCurrentMode = async () => {
  if (browseMode.value === 'continuous') {
    clearContinuousSnapshot()
    resetContinuousList()
    await loadMoreContinuous()
    return
  }
  await loadPeople()
}

const handleExcludeMergeSuggestion = async (candidateIds: number[]) => {
  if (!currentMergeSuggestionId.value || candidateIds.length === 0) return
  mergeSuggestionSubmitting.value = true
  try {
    await peopleApi.excludeMergeSuggestionCandidates(currentMergeSuggestionId.value, candidateIds)
    ElMessage.success('已剔除所选候选人物')
    await reloadMergeSuggestionReviewState()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '剔除失败')
  } finally {
    mergeSuggestionSubmitting.value = false
  }
}

const handleApplyMergeSuggestion = async (candidateIds: number[]) => {
  if (!currentMergeSuggestionId.value || candidateIds.length === 0) return
  mergeSuggestionSubmitting.value = true
  try {
    await peopleApi.applyMergeSuggestion(currentMergeSuggestionId.value, candidateIds)
    ElMessage.success('已应用所选合并建议')
    await reloadMergeSuggestionReviewState(true)
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '应用失败')
  } finally {
    mergeSuggestionSubmitting.value = false
  }
}

const handlePauseMergeSuggestionTask = async () => {
  mergeSuggestionAction.value = 'pause'
  try {
    await peopleApi.pauseMergeSuggestionTask()
    ElMessage.success('人物合并建议巡检已暂停')
    await loadTaskData()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '暂停失败')
  } finally {
    mergeSuggestionAction.value = ''
  }
}

const handleResumeMergeSuggestionTask = async () => {
  mergeSuggestionAction.value = 'resume'
  try {
    await peopleApi.resumeMergeSuggestionTask()
    ElMessage.success('人物合并建议巡检已恢复')
    await loadTaskData()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '恢复失败')
  } finally {
    mergeSuggestionAction.value = ''
  }
}

const handleRebuildMergeSuggestionTask = async () => {
  mergeSuggestionAction.value = 'rebuild'
  try {
    await peopleApi.rebuildMergeSuggestionTask()
    ElMessage.success('人物合并建议已标记重跑')
    await Promise.all([loadTaskData(), loadMergeSuggestions()])
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '重跑失败')
  } finally {
    mergeSuggestionAction.value = ''
  }
}

watch(backgroundLogs, async () => {
  await nextTick()
  if (logContainerRef.value) {
    logContainerRef.value.scrollTop = logContainerRef.value.scrollHeight
  }
})

watch(mergeSuggestionLogs, async () => {
  await nextTick()
  if (mergeLogContainerRef.value) {
    mergeLogContainerRef.value.scrollTop = mergeLogContainerRef.value.scrollHeight
  }
})

watch(mergeSuggestionDialogVisible, (visible) => {
  if (!visible) {
    currentMergeSuggestion.value = null
    currentMergeSuggestionId.value = null
  }
})

watch(activeTab, async (tab) => {
  if (tab === 'task') {
    teardownScrollObserver()
    await loadTaskData()
    return
  }
  await loadMergeSuggestions()
  // 回到人物列表 Tab 时，若处于连续浏览模式，重新挂载触底监听
  await nextTick()
  if (browseMode.value === 'continuous') {
    setupScrollObserver()
  }
})

// 连续浏览模式下哨兵节点变化时重新挂载监听
watch(sentinelRef, () => {
  if (browseMode.value === 'continuous') {
    setupScrollObserver()
  }
})

onMounted(async () => {
  // 首次加载人物列表与合并建议；后台任务数据改为进入“后台任务” Tab 后按需懒加载
  if (browseMode.value === 'continuous') {
    await Promise.all([initContinuousMode(), loadMergeSuggestions()])
  } else {
    await Promise.all([loadPeople(), loadMergeSuggestions()])
  }
  // 后台治理状态条：独立轮询，最多 30s 一次，仅页面打开时。
  void loadBackgroundStatus()
  backgroundStatusTimer = window.setInterval(() => {
    void loadBackgroundStatus()
  }, 30000)
  taskTimer = window.setInterval(() => {
    // 仅在后台任务 Tab 时轮询后台任务数据；切回人物列表后不再产生后台任务请求
    if (activeTab.value === 'task') {
      void loadTaskData()
    }
    void loadMergeSuggestions(true) // silent: true 避免轮询时 loading 闪烁
  }, 30000)
})

onBeforeUnmount(() => {
  if (taskTimer) {
    clearInterval(taskTimer)
    taskTimer = null
  }
  if (backgroundStatusTimer) {
    clearInterval(backgroundStatusTimer)
    backgroundStatusTimer = null
  }
  teardownScrollObserver()
})
</script>

<style scoped>
.background-pressure-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  padding: 8px 12px;
  border-radius: 12px;
  background: var(--el-fill-color-light, #f5f7fa);
  font-size: 12px;
}

.pressure-chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 999px;
  background: var(--el-fill-color, #eef0f3);
  color: var(--el-text-color-regular, #606266);
}

.pressure-chip.is-foreground {
  background: var(--el-color-warning-light-9, #fdf6ec);
  color: var(--el-color-warning, #e6a23c);
}

.pressure-chip.is-pause {
  background: var(--el-color-info-light-9, #f4f4f5);
  color: var(--el-color-info, #909399);
}

.pressure-chip.is-load {
  background: var(--el-color-primary-light-9, #ecf5ff);
  color: var(--el-color-primary, #409eff);
}

.people-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: var(--spacing-xl);
}

.people-tabs :deep(.el-tabs__header) {
  margin-bottom: 20px;
}

.section-card {
  border-radius: 18px;
}

.section-card :deep(.el-card__header) {
  padding: 22px 28px;
}

.section-card :deep(.el-card__body) {
  padding: 24px 28px;
}

.merge-suggestion-card-wrap.is-collapsed :deep(.el-card__body) {
  display: none;
}

.collapse-btn {
  padding: 4px !important;
  margin-left: -4px;
}

.collapse-btn .el-icon {
  transition: transform 0.2s ease;
}

.collapse-btn .el-icon.is-collapsed {
  transform: rotate(180deg);
}

.people-list-card :deep(.section-header) {
  flex-wrap: wrap;
}

.people-list-card :deep(.section-header-actions) {
  margin-left: auto;
}

.people-header-filters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.header-filter-input {
  width: 220px;
}

.header-filter-select {
  width: 130px;
}

.visibility-select {
  width: 110px;
}

.batch-action-bar {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  padding: 12px 16px;
  margin-bottom: 16px;
  border-radius: 12px;
  background: var(--color-bg-soft);
  border: 1px solid var(--color-border);
}

.batch-selected-count {
  font-size: 13px;
  color: var(--color-text-secondary);
}

.batch-range-tip {
  font-size: 12px;
  color: var(--color-text-secondary);
  opacity: 0.75;
}

.batch-actions {
  margin-left: auto;
  display: flex;
  gap: 8px;
}

.people-grid-wrap {
  min-height: 240px;
}

.people-card-grid {
  display: grid;
  /* 宽屏桌面约 5–7 人/行；auto-fill + minmax 自适应密度 */
  grid-template-columns: repeat(auto-fill, minmax(168px, 1fr));
  gap: 14px;
}

/* 人物卡片样式由 PersonCard.vue 自带 scoped 样式管理 */

.merge-suggestion-header,
.queue-progress-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.merge-suggestion-title {
  font-weight: 600;
  font-size: 14px;
  color: var(--color-text-primary);
  line-height: 1.4;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.merge-suggestion-meta,
.merge-suggestion-subtitle {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.merge-suggestion-meta,
.merge-suggestion-subtitle {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.mode-toggle {
  flex-shrink: 0;
}

.continuous-sentinel {
  height: 1px;
  width: 100%;
}

.continuous-status {
  margin-top: 16px;
  padding: 12px 0;
  text-align: center;
  font-size: 13px;
  color: var(--color-text-secondary);
}

.continuous-status.continuous-error {
  color: #f56c6c;
}

.retry-link {
  padding: 0 4px;
  vertical-align: baseline;
}

.pagination-wrap {
  display: flex;
  justify-content: flex-end;
  margin-top: 20px;
}

.merge-suggestion-list {
  min-height: 120px;
}

.merge-suggestion-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
}

.merge-suggestion-card {
  border: 1px solid var(--color-border);
  border-radius: 14px;
  padding: 16px;
  background: linear-gradient(135deg, #fffdf6 0%, #ffffff 100%);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.merge-suggestion-target {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.merge-suggestion-avatar {
  flex-shrink: 0;
}

.merge-suggestion-score {
  flex-shrink: 0;
  padding: 4px 8px;
  border-radius: 999px;
  background: rgba(230, 162, 60, 0.12);
  color: #d46b08;
  font-size: 12px;
  font-weight: 700;
}

.candidate-preview-list {
  display: flex;
  align-items: center;
  gap: 8px;
}

.candidate-preview {
  flex-shrink: 0;
}

.merge-suggestion-actions {
  display: flex;
  justify-content: flex-end;
}

.task-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.queue-progress {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.queue-progress-header,
.queue-progress-detail,
.queue-empty,
.task-summary,
.task-phase,
.merge-stat-label {
  font-size: 13px;
  color: var(--color-text-secondary);
}

.queue-progress-numbers {
  font-weight: 600;
  color: var(--color-text-primary);
}


.queue-empty {
  padding: 16px 0;
}

.task-summary {
  padding: 12px 16px;
  border-radius: 12px;
  background: var(--color-bg-soft);
  border: 1px solid var(--color-border);
}

.task-phase {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.task-phase-message {
  color: var(--color-text-primary);
  font-weight: 500;
}

.status-pill {
  padding: 4px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}

.status-pill.info {
  color: #909399;
  background: rgba(144, 147, 153, 0.12);
}

.status-pill.warning {
  color: #e6a23c;
  background: rgba(230, 162, 60, 0.12);
}

.status-pill.danger {
  color: #f56c6c;
  background: rgba(245, 108, 108, 0.12);
}

.danger {
  color: #f56c6c;
}

.background-log-body {
  max-height: 300px;
  overflow: auto;
  padding: 16px 18px;
  border-radius: 14px;
  background: #111827;
  color: #e5e7eb;
}

.background-log-body pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 12px;
  line-height: 1.7;
}

.background-log-empty {
  color: #9ca3af;
}

.merge-task-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.merge-stat-card {
  padding: 14px 16px;
  border-radius: 14px;
  border: 1px solid var(--color-border);
  background: var(--color-bg-soft);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.merge-stat-card strong {
  font-size: 22px;
  line-height: 1;
  color: var(--color-text-primary);
}

@media (max-width: 1200px) {
  /* 普通桌面 / 平板：约 3–4 人/行 */
  .people-card-grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .merge-suggestion-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .merge-task-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 992px) {
  .people-card-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .people-page {
    padding: 16px;
  }

  .section-card :deep(.el-card__header),
  .section-card :deep(.el-card__body) {
    padding-left: 18px;
    padding-right: 18px;
  }

  /* 手机：2 人/行 */
  .people-card-grid,
  .merge-suggestion-grid,
  .merge-task-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .pagination-wrap {
    justify-content: center;
  }
}

@media (max-width: 520px) {
  .people-card-grid,
  .merge-suggestion-grid,
  .merge-task-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .header-filter-input {
    width: 100%;
  }

  .header-filter-select {
    flex: 1 1 120px;
    width: auto;
  }

  /* 窄屏下筛选与模式切换允许换行，避免挤压 */
  .people-header-filters {
    justify-content: flex-start;
  }
}
</style>
