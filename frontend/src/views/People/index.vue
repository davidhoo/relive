<template>
  <div class="people-page">
    <PageHeader title="人物管理" subtitle="按人物维度浏览聚类结果，查看后台进度，并进入详情页做修正" :gradient="true">
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
              <SectionHeader :icon="Document" title="最近活动">
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
        </div>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Clock, Document, Search, User } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { peopleApi } from '@/api/people'
import type { PeopleStats, PeopleTask, Person, PersonCategory } from '@/types/people'
import { getPeopleTaskStatusMeta, getPersonAvatarFallback, getPersonCategoryLabel, sortPeopleForDisplay } from './peopleHelpers'

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
const starting = ref(false)
const stopping = ref(false)
const resetting = ref(false)
const enqueueing = ref(false)
const logContainerRef = ref<HTMLElement | null>(null)
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
  const total = done + queuePending.value
  if (total === 0) return 0
  return Math.round((done / total) * 100)
})

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
  } catch (error: any) {
    ElMessage.error(error.message || '加载人物任务状态失败')
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
  await loadPeople()
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

watch(backgroundLogs, async () => {
  await nextTick()
  if (logContainerRef.value) {
    logContainerRef.value.scrollTop = logContainerRef.value.scrollHeight
  }
})

watch(activeTab, async (tab) => {
  if (tab === 'task') {
    await loadTaskData()
  }
})

onMounted(async () => {
  await Promise.all([loadPeople(), loadTaskData()])
  taskTimer = window.setInterval(loadTaskData, 5000)
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

.people-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
}

.detail-btn {
  background-color: #fff7e6 !important;
  border-color: #ffd591 !important;
  color: #d46b08 !important;
  border-radius: var(--radius-sm);
  min-width: 78px;
}

.detail-btn:hover:not(:disabled) {
  background-color: #ffe7ba !important;
  border-color: #ffc53d !important;
  color: #ad4e00 !important;
}

.detail-btn:disabled {
  background-color: #f5f5f5 !important;
  border-color: #d9d9d9 !important;
  color: #999 !important;
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

.person-card-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.person-card-name {
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

.person-card-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}

.person-card-counts {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.pagination-wrap {
  display: flex;
  justify-content: flex-end;
  margin-top: 20px;
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

.queue-progress-header {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  color: var(--color-text-secondary);
}

.queue-progress-numbers {
  font-weight: 600;
  color: var(--color-text-primary);
}

.queue-progress-detail {
  font-size: 13px;
  color: var(--color-text-secondary);
}

.queue-empty {
  padding: 16px 0;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.task-summary {
  padding: 12px 16px;
  border-radius: 12px;
  background: var(--color-bg-soft);
  border: 1px solid var(--color-border);
  font-size: 13px;
  color: var(--color-text-secondary);
}

.task-phase {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  font-size: 13px;
}

.task-phase-label {
  color: var(--color-text-secondary);
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

.danger {
  color: #f56c6c;
}

.background-log-body {
  max-height: 360px;
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

@media (max-width: 1200px) {
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

  .people-card-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .pagination-wrap {
    justify-content: center;
  }
}

@media (max-width: 520px) {
  .people-card-grid {
    grid-template-columns: 1fr;
  }
}
</style>
