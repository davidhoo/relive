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

          <el-table v-loading="peopleLoading" :data="people" row-key="id" class="people-table">
            <el-table-column label="人物" min-width="280">
              <template #default="{ row }">
                <div class="person-cell">
                  <el-avatar :size="52" :src="getFaceThumbnail(row.representative_face_id)">
                    {{ getPersonAvatarFallback(row) }}
                  </el-avatar>
                  <div class="person-cell-main">
                    <div class="person-cell-title">
                      <span class="person-name">{{ getPersonName(row) }}</span>
                      <el-tag size="small" effect="plain">{{ `#${row.id}` }}</el-tag>
                    </div>
                    <div class="person-cell-meta">
                      最近更新：{{ formatTime(row.updated_at) }}
                    </div>
                  </div>
                </div>
              </template>
            </el-table-column>

            <el-table-column label="类别" width="120">
              <template #default="{ row }">
                <el-tag :type="categoryTagType(row.category)" effect="light">
                  {{ getPersonCategoryLabel(row.category) }}
                </el-tag>
              </template>
            </el-table-column>

            <el-table-column prop="photo_count" label="照片数" width="100" />
            <el-table-column prop="face_count" label="人脸数" width="100" />

            <el-table-column label="操作" width="120" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" @click="goToDetail(row.id)">查看详情</el-button>
              </template>
            </el-table-column>
          </el-table>

          <el-empty v-if="!peopleLoading && people.length === 0" description="暂无人物数据" />

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
      </el-tab-pane>

      <el-tab-pane label="后台任务" name="task">
        <el-card shadow="never" class="section-card animate-fade-in">
          <template #header>
            <SectionHeader :icon="Clock" title="后台任务概览">
              <template #actions>
                <span class="status-pill" :class="taskMeta.type">{{ taskMeta.label }}</span>
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
            <el-text type="info" class="task-note">
              人物后台处理会在扫描 / 重建后自动启动，这里主要用于查看进度、失败数和最近日志。
            </el-text>
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

          <div class="background-log-body">
            <pre v-if="backgroundLogs.length">{{ backgroundLogs.join('\n') }}</pre>
            <div v-else class="background-log-empty">暂无人物后台任务日志</div>
          </div>
        </el-card>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Clock, DataLine, Document, Search, User } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

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

const formatTime = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN')
}

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

watch(activeTab, async (tab) => {
  if (tab === 'task') {
    await loadTaskData()
  }
})

onMounted(async () => {
  await Promise.all([loadPeople(), loadTaskData()])
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

.people-table :deep(.el-table__cell) {
  vertical-align: middle;
}

.person-cell {
  display: flex;
  align-items: center;
  gap: 12px;
}

.person-cell-main {
  min-width: 0;
}

.person-cell-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.person-name {
  font-weight: 600;
  color: var(--color-text-primary);
}

.person-cell-meta {
  font-size: 13px;
  color: var(--color-text-secondary);
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

.task-note {
  line-height: 1.7;
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

  .stats-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .pagination-wrap {
    justify-content: center;
  }
}
</style>
