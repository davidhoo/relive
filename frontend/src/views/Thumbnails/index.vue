<template>
  <div class="thumbnail-page">
    <PageHeader title="缩略图生成" subtitle="管理后台缩略图队列，支持开始、停止和进度查看" :gradient="true">
      <template #actions>
        <el-button class="header-action-btn" @click="$router.push('/photos')">
          <el-icon><Picture /></el-icon>
          前往照片管理
        </el-button>
      </template>
    </PageHeader>

    <el-card shadow="never" class="section-card animate-fade-in">
      <template #header>
        <SectionHeader :icon="Picture" title="后台任务">
          <template #actions>
            <span class="status-pill" :class="taskRunning ? 'warning' : taskStopping ? 'warning' : 'success'">
              {{ taskRunning ? '运行中' : taskStopping ? '停止中' : '未运行' }}
            </span>
          </template>
        </SectionHeader>
      </template>

      <div class="section-content">
        <div class="control-row control-row-stack">
          <div class="control-row-main">
            <el-button
              v-if="!taskRunning && !taskStopping"
              type="primary"
              size="large"
              @click="handleStart"
              :loading="starting"
              class="action-btn-primary"
            >
              开启后台生成
            </el-button>
            <el-button
              v-else
              type="danger"
              size="large"
              @click="handleStop"
              :loading="stopping"
              :disabled="taskStopping"
              class="action-btn-danger"
            >
              {{ taskStopping ? '停止中...' : '停止后台生成' }}
            </el-button>
          </div>
          <div class="inline-note-wrap">
            <el-text type="info" class="inline-info-text aligned-note">
              后台生成会持续处理缩略图队列。照片管理与仪表盘访问到未生成缩略图的照片时，会自动触发热点优先补队列。
            </el-text>
            <div class="inline-note-divider"></div>
          </div>
        </div>

        <div class="background-log-panel flat-log-panel">
          <div class="background-log-header">
            <span>任务日志（最后 100 行）</span>
            <el-button size="small" plain class="mini-action-btn" @click="loadBackgroundLogs">刷新</el-button>
          </div>
          <div class="background-log-body" ref="logContainerRef">
            <pre v-if="backgroundLogs.length">{{ backgroundLogs.join('\n') }}</pre>
            <div v-else class="background-log-empty">暂无缩略图后台任务日志</div>
          </div>
        </div>
      </div>
    </el-card>

    <el-card shadow="never" class="section-card animate-fade-in animate-delay-1">
      <template #header>
        <SectionHeader :icon="DataLine" title="队列统计">
          <template #actions>
            <el-button size="small" plain class="mini-action-btn" @click="loadData">刷新</el-button>
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
        <SectionHeader :icon="Clock" title="当前进度" />
      </template>

      <div class="runtime-inline-list">
        <div class="runtime-inline-row">
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">任务状态</span>
            <span class="runtime-inline-value">{{ task?.status || '-' }}</span>
          </div>
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">已处理</span>
            <span class="runtime-inline-value">{{ task?.processed_jobs || 0 }}</span>
          </div>
        </div>
        <div class="runtime-inline-row">
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">当前照片</span>
            <span class="runtime-inline-value mono">{{ task?.current_photo_id ? `Photo #${task.current_photo_id}` : '-' }}</span>
          </div>
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">当前文件</span>
            <span class="runtime-inline-value mono">{{ task?.current_file || '-' }}</span>
          </div>
        </div>
        <div class="runtime-inline-row">
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">开始时间</span>
            <span class="runtime-inline-value">{{ formatTime(task?.started_at) }}</span>
          </div>
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">停止时间</span>
            <span class="runtime-inline-value">{{ formatTime(task?.stopped_at) }}</span>
          </div>
        </div>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Clock, DataLine, Picture } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { thumbnailApi } from '@/api/thumbnail'
import type { ThumbnailStats, ThumbnailTask } from '@/types/thumbnail'

const task = ref<ThumbnailTask | null>(null)
const stats = ref<ThumbnailStats>({ total: 0, pending: 0, queued: 0, processing: 0, completed: 0, failed: 0, cancelled: 0 })
const starting = ref(false)
const stopping = ref(false)
const backgroundLogs = ref<string[]>([])
const logContainerRef = ref<HTMLElement | null>(null)
let timer: number | null = null

const taskRunning = computed(() => task.value?.status === 'running')
const taskStopping = computed(() => task.value?.status === 'stopping')

const formatTime = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN')
}

const loadBackgroundLogs = async () => {
  try {
    const res = await thumbnailApi.getBackgroundLogs()
    backgroundLogs.value = res.data?.data?.lines || []
  } catch (error: any) {
    console.error('Failed to load thumbnail logs:', error)
  }
}

const loadData = async () => {
  try {
    const [taskRes, statsRes, logsRes] = await Promise.all([thumbnailApi.getTask(), thumbnailApi.getStats(), thumbnailApi.getBackgroundLogs()])
    task.value = taskRes.data?.data || null
    stats.value = statsRes.data?.data || stats.value
    backgroundLogs.value = logsRes.data?.data?.lines || []
  } catch (error: any) {
    console.error('Failed to load thumbnail data:', error)
  }
}

const handleStart = async () => {
  try {
    starting.value = true
    await thumbnailApi.startBackground()
    ElMessage.success('缩略图后台生成已启动')
    await loadData()
  } catch (error: any) {
    ElMessage.error(error.message || '启动缩略图后台生成失败')
  } finally {
    starting.value = false
  }
}

const handleStop = async () => {
  try {
    stopping.value = true
    await thumbnailApi.stopBackground()
    ElMessage.info('已请求停止缩略图后台生成')
    await loadData()
  } catch (error: any) {
    ElMessage.error(error.message || '停止缩略图后台生成失败')
  } finally {
    stopping.value = false
  }
}

onMounted(async () => {
  await loadData()
  timer = window.setInterval(loadData, 5000)
})

onUnmounted(() => {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
})

watch(backgroundLogs, async () => {
  await nextTick()
  if (logContainerRef.value) {
    logContainerRef.value.scrollTop = logContainerRef.value.scrollHeight
  }
})
</script>

<style scoped>
.thumbnail-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: var(--spacing-xl);
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
.section-content {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.control-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.control-row-stack {
  flex-direction: column;
  align-items: stretch;
}
.control-row-main {
  display: flex;
  align-items: center;
  gap: 12px;
}
.inline-note-wrap {
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 12px;
}
.aligned-note {
  width: 100%;
  text-align: left;
}
.inline-note-divider {
  width: 100%;
  height: 1px;
  background: var(--color-border);
}
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 14px;
}
.stat-item {
  padding: 16px;
  border-radius: 14px;
  background: var(--el-fill-color-light);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.stat-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.success { color: var(--el-color-success); }
.danger { color: var(--el-color-danger); }
.runtime-inline-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.runtime-inline-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.runtime-inline-item {
  padding: 14px 16px;
  border-radius: 14px;
  background: var(--el-fill-color-light);
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.runtime-inline-label {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.runtime-inline-value {
  color: var(--el-text-color-primary);
}
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.status-pill {
  padding: 4px 10px;
  border-radius: 999px;
  font-size: 12px;
}
.status-pill.success {
  color: var(--el-color-success);
  background: var(--el-color-success-light-9);
}
.status-pill.warning {
  color: var(--el-color-warning);
  background: var(--el-color-warning-light-9);
}
.background-log-panel {
  margin-top: 12px;
  border-radius: 18px;
  border: 1px solid var(--color-border);
  overflow: hidden;
}
.background-log-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--color-border);
  background: var(--el-fill-color-light);
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.background-log-body {
  max-height: 260px;
  overflow: auto;
  padding: 14px 16px;
  background: var(--el-bg-color-overlay);
}
.background-log-body pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  line-height: 1.7;
  color: var(--el-text-color-regular);
}
.background-log-empty {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
@media (max-width: 768px) {
  .thumbnail-page {
    padding: var(--spacing-lg);
  }

  .section-card :deep(.el-card__header),
  .section-card :deep(.el-card__body) {
    padding: 20px;
  }

  .runtime-inline-row {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 480px) {
  .thumbnail-page {
    padding: var(--spacing-md);
  }
}
</style>
