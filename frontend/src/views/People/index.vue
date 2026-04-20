<template>
  <div class="people-page">
    <PageHeader title="人物管理" subtitle="按人物维度浏览聚类结果，查看后台进度，并集中审核系统给出的合并建议" :gradient="true">
      <template #actions>
        <el-button class="header-action-btn" @click="refreshCurrentTab">
          刷新当前标签
        </el-button>
      </template>
    </PageHeader>

    <el-tabs v-model="activeTab" class="people-tabs">
      <el-tab-pane label="人物列表" name="people">
        <div class="section-stack">
          <el-card shadow="never" class="section-card animate-fade-in">
            <template #header>
              <SectionHeader :icon="Search" title="筛选条件" />
            </template>

            <div class="filters-row">
              <el-input
                v-model="filters.search"
                clearable
                placeholder="搜索人物姓名 / ID / 类别"
                class="filter-input"
                @keyup.enter="handleSearch"
                @clear="handleSearch"
              />
              <el-select v-model="filters.category" clearable placeholder="全部类别" class="filter-select">
                <el-option v-for="option in categoryOptions" :key="option.value" :label="option.label" :value="option.value" />
              </el-select>
              <el-button type="primary" @click="handleSearch">应用筛选</el-button>
            </div>
          </el-card>

          <el-card shadow="never" class="section-card animate-fade-in animate-delay-1">
            <template #header>
              <SectionHeader :icon="User" :title="`人物列表（共 ${total} 人）`">
                <template #actions>
                  <el-button size="small" plain class="mini-action-btn" @click="loadPeople">刷新</el-button>
                </template>
              </SectionHeader>
            </template>

            <div v-loading="peopleLoading" class="people-grid-wrap">
              <el-empty v-if="!peopleLoading && people.length === 0" description="暂无人物数据" />

              <div v-else class="people-card-grid">
                <button
                  v-for="personItem in people"
                  :key="personItem.id"
                  type="button"
                  class="person-card"
                  @click="goToDetail(personItem.id)"
                >
                  <el-avatar :size="44" :src="getFaceThumbnail(personItem.representative_face_id)" class="person-card-avatar">
                    {{ getPersonAvatarFallback(personItem) }}
                  </el-avatar>

                  <div class="person-card-body">
                    <div class="person-card-title-row">
                      <span class="person-card-name">{{ getPersonName(personItem) }}</span>
                      <span class="person-card-id">{{ `#${personItem.id}` }}</span>
                    </div>
                    <div class="person-card-meta">
                      <el-tag :type="categoryTagType(personItem.category)" effect="light" size="small">
                        {{ getPersonCategoryLabel(personItem.category) }}
                      </el-tag>
                      <span class="person-card-counts">{{ personItem.photo_count }} 照片 · {{ personItem.face_count }} 人脸</span>
                    </div>
                  </div>
                </button>
              </div>
            </div>

            <div v-if="total > 0" class="pagination-wrap">
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

          <el-card v-if="mergeSuggestionVisible" shadow="never" class="section-card animate-fade-in animate-delay-2">
            <template #header>
              <SectionHeader :icon="Connection" :title="`人物合并建议（${mergeSuggestionTotal}）`">
                <template #actions>
                  <el-button size="small" plain class="mini-action-btn" @click="loadMergeSuggestions">刷新</el-button>
                </template>
              </SectionHeader>
            </template>

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
          </el-card>
        </div>
      </el-tab-pane>

      <el-tab-pane label="后台任务" name="task">
        <div class="section-stack">
          <el-card shadow="never" class="section-card animate-fade-in">
            <template #header>
              <SectionHeader :icon="Clock" title="Worker 控制">
                <template #actions>
                  <span class="status-pill" :class="taskMeta.type">{{ taskMeta.label }}</span>
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
                  <span>队列进度</span>
                  <span class="queue-progress-numbers">{{ stats.completed }} / {{ stats.completed + queuePending }}</span>
                </div>
                <el-progress :percentage="queueProgressPercent" :stroke-width="10" :show-text="false" />
                <div class="queue-progress-detail">
                  待处理 {{ queuePending }}<template v-if="stats.failed > 0"> · <span class="danger">失败 {{ stats.failed }}</span></template>
                </div>
              </div>
              <div v-else class="queue-empty">
                队列已清空，等待新任务入队
              </div>

              <div v-if="clusteringPending > 0" class="queue-progress">
                <div class="queue-progress-header">
                  <span>聚类积压</span>
                  <span class="queue-progress-numbers">{{ clusteringPending }}</span>
                </div>
                <div class="queue-progress-detail">
                  未聚类 {{ stats.pending_faces_never_clustered }} · 已重试 {{ stats.pending_faces_retried }}
                </div>
              </div>
              <div v-else class="queue-empty">
                没有待聚类人脸积压
              </div>

              <div v-if="task?.current_message" class="task-phase">
                <span class="task-phase-label">{{ taskPhaseLabel }}</span>
                <span class="task-phase-message">{{ task.current_message }}</span>
              </div>

              <div class="task-summary">
                <span>累计完成 <strong>{{ stats.completed }}</strong></span>
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
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Clock, Connection, Document, Search, User } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { peopleApi } from '@/api/people'
import type {
  PeopleStats,
  PeopleTask,
  Person,
  PersonCategory,
  PersonMergeSuggestion,
  PersonMergeSuggestionStats,
  PersonMergeSuggestionTask,
} from '@/types/people'
import MergeSuggestionReviewDialog from './MergeSuggestionReviewDialog.vue'
import {
  getMergeSuggestionTaskStatusMeta,
  getMergeSuggestionVisibility,
  getPeopleTaskStatusMeta,
  getPersonAvatarFallback,
  getPersonCategoryLabel,
  sortPeopleForDisplay,
} from './peopleHelpers'

const route = useRoute()
const router = useRouter()
const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

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
})
const backgroundLogs = ref<string[]>([])
const people = ref<Person[]>([])
const total = ref(0)

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

const workerActive = computed(() => {
  const s = task.value?.status
  return s === 'running' || s === 'idle' || s === 'stopping'
})
const taskStopping = computed(() => task.value?.status === 'stopping')

const queuePending = computed(() => stats.value.pending + stats.value.queued + stats.value.processing)
const clusteringPending = computed(() => stats.value.pending_faces_total)
const queueProgressPercent = computed(() => {
  const done = stats.value.completed
  const totalCount = done + queuePending.value
  if (totalCount === 0) return 0
  return Math.round((done / totalCount) * 100)
})

const mergeSuggestionVisible = computed(() => getMergeSuggestionVisibility(mergeSuggestionTotal.value, mergeSuggestionLoading.value))
const mergeSuggestionTaskMeta = computed(() => getMergeSuggestionTaskStatusMeta(mergeSuggestionTask.value))

const filters = reactive<{
  page: number
  page_size: number
  search: string
  category?: PersonCategory
}>({
  page: Number(route.query.page) || 1,
  page_size: Number(route.query.page_size) || 20,
  search: (route.query.search as string) || '',
  category: (route.query.category as PersonCategory) || undefined,
})

const syncFiltersToQuery = () => {
  const query: Record<string, string> = {}
  if (filters.page > 1) query.page = String(filters.page)
  if (filters.page_size !== 20) query.page_size = String(filters.page_size)
  if (filters.search) query.search = filters.search
  if (filters.category) query.category = filters.category
  router.replace({ query })
}

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

const categoryTagType = (category: PersonCategory) => {
  switch (category) {
    case 'family':
      return 'danger'
    case 'friend':
      return 'success'
    case 'acquaintance':
      return 'warning'
    default:
      return 'info'
  }
}

const getPersonName = (person: Person) => person.name?.trim() || `未命名人物 #${person.id}`

const getFaceThumbnail = (faceId?: number) => {
  if (!faceId) return ''
  return `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`
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
    })
    const payload = res.data?.data
    people.value = sortPeopleForDisplay(payload?.items || [])
    total.value = payload?.total || 0
  } catch (error: any) {
    ElMessage.error(error.message || '加载人物列表失败')
  } finally {
    peopleLoading.value = false
  }
}

const loadMergeSuggestions = async () => {
  mergeSuggestionLoading.value = true
  try {
    const res = await peopleApi.listMergeSuggestions({ page: 1, page_size: 12 })
    const payload = res.data?.data
    mergeSuggestions.value = payload?.items || []
    mergeSuggestionTotal.value = payload?.total || 0
  } catch (error: any) {
    ElMessage.error(error.message || '加载人物合并建议失败')
  } finally {
    mergeSuggestionLoading.value = false
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
  }
}

const loadMergeSuggestionDetail = async (id: number) => {
  mergeSuggestionDetailLoading.value = true
  try {
    const res = await peopleApi.getMergeSuggestion(id)
    currentMergeSuggestion.value = res.data?.data || null
    currentMergeSuggestionId.value = currentMergeSuggestion.value?.id || null
  } catch (error: any) {
    currentMergeSuggestion.value = null
    currentMergeSuggestionId.value = null
    ElMessage.error(error.response?.data?.error?.message || error.message || '加载建议详情失败')
  } finally {
    mergeSuggestionDetailLoading.value = false
  }
}

const handleSearch = async () => {
  filters.page = 1
  await loadPeople()
}

const handlePageChange = async (page: number) => {
  filters.page = page
  await loadPeople()
}

const handlePageSizeChange = async (pageSize: number) => {
  filters.page_size = pageSize
  filters.page = 1
  await loadPeople()
}

const goToDetail = (personId: number) => {
  router.push({
    path: `/people/${personId}`,
    query: { ...route.query }
  })
}

const refreshCurrentTab = async () => {
  if (activeTab.value === 'task') {
    await loadTaskData()
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
  await loadMergeSuggestionDetail(id)
}

const reloadMergeSuggestionReviewState = async () => {
  await Promise.all([loadMergeSuggestions(), loadTaskData()])
  if (!currentMergeSuggestionId.value) {
    return
  }
  try {
    await loadMergeSuggestionDetail(currentMergeSuggestionId.value)
    if (!currentMergeSuggestion.value || !currentMergeSuggestion.value.items?.length) {
      mergeSuggestionDialogVisible.value = false
    }
  } catch {
    mergeSuggestionDialogVisible.value = false
  }
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
    await reloadMergeSuggestionReviewState()
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
    await loadTaskData()
    return
  }
  await loadMergeSuggestions()
})

onMounted(async () => {
  await Promise.all([loadPeople(), loadTaskData(), loadMergeSuggestions()])
  taskTimer = window.setInterval(() => {
    void loadTaskData()
    void loadMergeSuggestions()
  }, 5000)
})

onBeforeUnmount(() => {
  if (taskTimer) {
    clearInterval(taskTimer)
    taskTimer = null
  }
})
</script>

<style scoped>
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

.filters-row {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}

.filter-input {
  width: min(360px, 100%);
}

.filter-select {
  width: 160px;
}

.people-grid-wrap {
  min-height: 240px;
}

.people-card-grid,
.merge-suggestion-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
}

.person-card {
  width: 100%;
  border: 1px solid var(--color-border);
  border-radius: 14px;
  padding: 14px;
  background: #fff;
  display: flex;
  align-items: center;
  gap: 12px;
  text-align: left;
  cursor: pointer;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}

.person-card:hover {
  transform: translateY(-1px);
  border-color: rgba(212, 107, 8, 0.28);
  box-shadow: 0 8px 20px rgba(15, 23, 42, 0.07);
}

.person-card-avatar {
  flex-shrink: 0;
}

.person-card-body {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.person-card-title-row,
.merge-suggestion-header,
.queue-progress-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.person-card-name,
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

.person-card-id {
  flex-shrink: 0;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--color-bg-soft);
  color: var(--color-text-secondary);
  font-size: 11px;
  font-weight: 600;
}

.person-card-meta,
.merge-suggestion-meta,
.merge-suggestion-subtitle {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.person-card-counts,
.merge-suggestion-meta,
.merge-suggestion-subtitle {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.pagination-wrap {
  display: flex;
  justify-content: flex-end;
  margin-top: 20px;
}

.merge-suggestion-list {
  min-height: 120px;
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
  .people-card-grid,
  .merge-suggestion-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .merge-task-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
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
    grid-template-columns: 1fr;
  }
}
</style>
