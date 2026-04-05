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
                  <div class="person-card-header">
                    <el-avatar :size="56" :src="getFaceThumbnail(personItem.representative_face_id)" class="person-card-avatar">
                      {{ getPersonAvatarFallback(personItem) }}
                    </el-avatar>

                    <div class="person-card-main">
                      <div class="person-card-title-row">
                        <span class="person-card-name">{{ getPersonName(personItem) }}</span>
                        <span class="person-card-id">{{ `#${personItem.id}` }}</span>
                      </div>
                      <el-tag :type="categoryTagType(personItem.category)" effect="light" size="small" class="person-card-category">
                        {{ getPersonCategoryLabel(personItem.category) }}
                      </el-tag>
                    </div>
                  </div>

                  <div class="person-card-stats">
                    <div class="person-card-stat">
                      <span class="person-card-stat-label">照片</span>
                      <strong class="person-card-stat-value">{{ personItem.photo_count }}</strong>
                    </div>
                    <div class="person-card-stat">
                      <span class="person-card-stat-label">人脸</span>
                      <strong class="person-card-stat-value">{{ personItem.face_count }}</strong>
                    </div>
                  </div>

                  <div class="person-card-footer">
                    <span class="person-card-link">查看详情</span>
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
              <SectionHeader :icon="Clock" title="后台任务概览">
                <template #actions>
                  <span class="status-pill" :class="taskMeta.type">{{ taskMeta.label }}</span>
                  <el-button
                    v-if="!taskRunning && !taskStopping"
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

            <div class="task-overview">
              <div class="task-overview-main">
                <div class="task-overview-item">
                  <span class="task-overview-label">任务状态</span>
                  <strong>{{ taskMeta.label }}</strong>
                </div>
                <div class="task-overview-item">
                  <span class="task-overview-label">已处理任务</span>
                  <strong>{{ task?.processed_jobs || 0 }}</strong>
                </div>
                <div class="task-overview-item">
                  <span class="task-overview-label">当前照片</span>
                  <strong>{{ task?.current_photo_id ? `#${task.current_photo_id}` : '-' }}</strong>
                </div>
              </div>
            </div>
          </el-card>

          <el-card shadow="never" class="section-card animate-fade-in animate-delay-1">
            <template #header>
              <SectionHeader :icon="DataLine" title="队列统计">
                <template #actions>
                  <el-button size="small" plain class="mini-action-btn" @click="loadTaskData">刷新</el-button>
                </template>
              </SectionHeader>
            </template>

            <div class="stats-grid">
              <div class="stat-item"><span class="stat-label">总任务</span><strong>{{ stats.total }}</strong></div>
              <div class="stat-item"><span class="stat-label">待处理</span><strong>{{ stats.pending + stats.queued }}</strong></div>
              <div class="stat-item"><span class="stat-label">处理中</span><strong>{{ stats.processing }}</strong></div>
              <div class="stat-item"><span class="stat-label">已完成</span><strong class="success">{{ stats.completed }}</strong></div>
              <div class="stat-item"><span class="stat-label">失败</span><strong class="danger">{{ stats.failed }}</strong></div>
              <div class="stat-item"><span class="stat-label">已取消</span><strong>{{ stats.cancelled }}</strong></div>
            </div>
          </el-card>

          <el-card shadow="never" class="section-card animate-fade-in animate-delay-2">
            <template #header>
              <SectionHeader :icon="Document" title="最近日志">
                <template #actions>
                  <el-button size="small" plain class="mini-action-btn" @click="loadTaskData">刷新</el-button>
                </template>
              </SectionHeader>
            </template>

            <div ref="logContainerRef" class="background-log-body">
              <pre v-if="backgroundLogs.length">{{ backgroundLogs.join('\n') }}</pre>
              <div v-else class="background-log-empty">暂无人物后台任务日志</div>
            </div>
          </el-card>
        </div>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Clock, DataLine, Document, Search, User } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { peopleApi } from '@/api/people'
import type { PeopleStats, PeopleTask, Person, PersonCategory } from '@/types/people'
import { getPeopleTaskStatusMeta, getPersonAvatarFallback, getPersonCategoryLabel, sortPeopleForDisplay } from './peopleHelpers'

const router = useRouter()
const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const activeTab = ref<'people' | 'task'>('people')
const peopleLoading = ref(false)
const task = ref<PeopleTask | null>(null)
const stats = ref<PeopleStats>({ total: 0, pending: 0, queued: 0, processing: 0, completed: 0, failed: 0, cancelled: 0 })
const backgroundLogs = ref<string[]>([])
const people = ref<Person[]>([])
const total = ref(0)
const starting = ref(false)
const stopping = ref(false)
const resetting = ref(false)
const logContainerRef = ref<HTMLElement | null>(null)
let taskTimer: number | null = null

const taskRunning = computed(() => task.value?.status === 'running')
const taskStopping = computed(() => task.value?.status === 'stopping')

const filters = reactive<{
  page: number
  page_size: number
  search: string
  category?: PersonCategory
}>({
  page: 1,
  page_size: 20,
  search: '',
  category: undefined,
})

const categoryOptions = [
  { label: '家人', value: 'family' },
  { label: '亲友', value: 'friend' },
  { label: '熟人', value: 'acquaintance' },
  { label: '路人', value: 'stranger' },
] satisfies Array<{ label: string; value: PersonCategory }>

const taskMeta = computed(() => getPeopleTaskStatusMeta(task.value?.status))

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
  router.push(`/people/${personId}`)
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
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 16px;
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
  border-radius: 16px;
  padding: 16px;
  background: #fff;
  display: flex;
  flex-direction: column;
  gap: 14px;
  text-align: left;
  cursor: pointer;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}

.person-card:hover {
  transform: translateY(-2px);
  border-color: rgba(212, 107, 8, 0.28);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.08);
}

.person-card-header {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.person-card-avatar {
  flex-shrink: 0;
}

.person-card-main {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
}

.person-card-title-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.person-card-name {
  font-weight: 600;
  color: var(--color-text-primary);
  line-height: 1.5;
  min-width: 0;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.person-card-id {
  flex-shrink: 0;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--color-bg-soft);
  color: var(--color-text-secondary);
  font-size: 12px;
  font-weight: 600;
}

.person-card-category {
  align-self: flex-start;
}

.person-card-stats {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.person-card-stat {
  padding: 12px;
  border-radius: 12px;
  background: var(--color-bg-soft);
  border: 1px solid var(--color-border);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.person-card-stat-label {
  color: var(--color-text-secondary);
  font-size: 12px;
}

.person-card-stat-value {
  color: var(--color-text-primary);
  font-size: 18px;
  line-height: 1.2;
}

.person-card-footer {
  display: flex;
  justify-content: flex-end;
}

.person-card-link {
  color: #d46b08;
  font-size: 13px;
  font-weight: 600;
}

.pagination-wrap {
  display: flex;
  justify-content: flex-end;
  margin-top: 20px;
}

.task-overview {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.task-overview-main {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.task-overview-item {
  padding: 16px 18px;
  border-radius: 14px;
  background: var(--color-bg-soft);
  border: 1px solid var(--color-border);
}

.task-overview-label {
  display: block;
  color: var(--color-text-secondary);
  font-size: 13px;
  margin-bottom: 8px;
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

.status-pill.success {
  color: #67c23a;
  background: rgba(103, 194, 58, 0.12);
}

.status-pill.danger {
  color: #f56c6c;
  background: rgba(245, 108, 108, 0.12);
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: 12px;
}

.stat-item {
  padding: 16px 18px;
  border-radius: 14px;
  background: var(--color-bg-soft);
  border: 1px solid var(--color-border);
}

.stat-label {
  display: block;
  color: var(--color-text-secondary);
  font-size: 13px;
  margin-bottom: 6px;
}

.success {
  color: #67c23a;
}

.danger {
  color: #f56c6c;
}

.background-log-body {
  max-height: 280px;
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

  .stats-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .task-overview-main {
    grid-template-columns: 1fr;
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

  .stats-grid {
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

  .person-card-stats {
    grid-template-columns: 1fr;
  }
}
</style>
