<template>
  <div class="analysis-page">
    <PageHeader title="AI 分析" subtitle="管理分析任务、查看运行状态与批量处理进度" :gradient="true">
      <template #actions>
        <el-button class="header-action-btn" @click="$router.push('/config')">
          <el-icon><Setting /></el-icon>
          前往配置
        </el-button>
      </template>
    </PageHeader>

    <el-card shadow="never" class="section-card animate-fade-in">
      <template #header>
        <SectionHeader :icon="Setting" title="AI Provider 配置" />
      </template>

      <el-alert
        v-if="!providerInfo"
        type="warning"
        title="AI 服务未配置"
        description="请先在配置管理中配置 AI Provider (Ollama/Qwen/OpenAI) 才能使用 AI 分析功能"
        show-icon
        :closable="false"
        class="provider-alert"
      />

      <div v-else class="provider-inline-row">
        <div class="provider-inline-item">
          <span class="provider-inline-label">当前 Provider</span>
          <el-tag type="primary" effect="light" round>{{ providerInfo.name }}</el-tag>
        </div>
        <div class="provider-inline-item">
          <span class="provider-inline-label">服务状态</span>
          <el-tag :type="providerInfo.is_available ? 'success' : 'danger'" effect="light" round>
            {{ providerInfo.is_available ? '可用' : '不可用' }}
          </el-tag>
        </div>
      </div>
    </el-card>

    <el-card shadow="never" class="section-card animate-fade-in animate-delay-1">
      <template #header>
        <SectionHeader :icon="Cpu" title="分析运行状态" />
      </template>

      <div class="runtime-inline-list">
        <div class="runtime-inline-row">
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">当前状态</span>
            <el-tag :type="runtimeStatus?.is_active ? 'warning' : 'success'" effect="light" round>
              {{ runtimeStatus?.is_active ? '已占用' : '空闲' }}
            </el-tag>
          </div>
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">占用模式</span>
            <span class="runtime-inline-value">{{ runtimeModeText }}</span>
          </div>
        </div>
        <div class="runtime-inline-row">
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">占用实例</span>
            <span class="runtime-inline-value mono">{{ runtimeStatus?.owner_id || '-' }}</span>
          </div>
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">开始时间</span>
            <span class="runtime-inline-value">{{ formatTime(runtimeStatus?.started_at) }}</span>
          </div>
        </div>
        <div class="runtime-inline-row runtime-inline-row-full">
          <div class="runtime-inline-item runtime-inline-item-full">
            <span class="runtime-inline-label">说明</span>
            <span class="runtime-inline-value">{{ runtimeStatus?.message || '当前没有分析器占用运行权' }}</span>
          </div>
        </div>
      </div>
    </el-card>

    <el-card shadow="never" class="section-card animate-fade-in animate-delay-2">
      <template #header>
        <SectionHeader :icon="MagicStick" title="批量分析" />
      </template>

      <div class="section-content">
        <div class="control-row">
          <div class="control-row-main">
            <span class="control-label">分析数量</span>
            <el-input-number
              v-model="batchLimit"
              :min="1"
              :max="1000"
              :step="10"
              class="input-number-width-lg"
            />
            <el-button
              type="primary"
              size="large"
              @click="handleBatchAnalyze"
              :loading="analyzing"
              :disabled="batchAnalyzeDisabled"
              class="action-btn-primary"
            >
              {{ analyzing ? '分析中...' : '开始批量分析' }}
            </el-button>
          </div>
          <el-text v-if="batchDisabledReason" type="info" class="inline-info-text">
            {{ batchDisabledReason }}
          </el-text>
        </div>
        <div class="inline-note">
          批量分析将按照队列顺序处理未分析的照片，建议每次处理数量不超过 500 张，避免长时间占用资源。
        </div>
      </div>
    </el-card>

    <el-card shadow="never" class="section-card animate-fade-in animate-delay-3">
      <template #header>
        <SectionHeader :icon="MagicStick" title="后台分析">
          <template #actions>
            <span class="status-pill" :class="backgroundRunning ? 'warning' : 'success'">
              {{ backgroundRunning ? '运行中' : '未运行' }}
            </span>
          </template>
        </SectionHeader>
      </template>

      <div class="section-content">
        <div class="control-row control-row-stack">
          <div class="control-row-main">
            <el-button
              v-if="!backgroundRunning"
              type="primary"
              size="large"
              @click="handleStartBackground"
              :disabled="backgroundStartDisabled"
              class="action-btn-primary"
            >
              开启后台分析
            </el-button>
            <el-button
              v-else
              type="danger"
              size="large"
              @click="handleStopBackground"
              class="action-btn-danger"
            >
              停止后台分析
            </el-button>
          </div>
          <el-text v-if="backgroundDisabledReason" type="info" class="inline-info-text">
            {{ backgroundDisabledReason }}
          </el-text>
        </div>

        <div class="inline-note">
          后台分析会持续扫描未分析照片并自动处理，没有新照片时会短暂等待后继续轮询。
        </div>

        <div class="background-log-panel flat-log-panel">
          <div class="background-log-header">
            <span>任务日志（最后 100 行）</span>
            <el-button size="small" plain class="mini-action-btn" @click="loadBackgroundLogs">刷新</el-button>
          </div>
          <div class="background-log-body" ref="logContainerRef">
            <pre v-if="backgroundLogs.length">{{ backgroundLogs.join('\n') }}</pre>
            <div v-else class="background-log-empty">暂无后台分析日志</div>
          </div>
        </div>
      </div>
    </el-card>

    <el-card shadow="never" class="section-card animate-fade-in animate-delay-4" v-if="progress">
      <template #header>
        <SectionHeader :icon="DataLine" title="分析进度">
          <template #actions>
            <div class="header-actions-inline">
              <span class="status-pill" :class="progressPillClass">{{ progressPillText }}</span>
              <el-button size="small" plain class="mini-action-btn" @click="loadProgress">刷新</el-button>
            </div>
          </template>
        </SectionHeader>
      </template>

      <div class="progress-summary-strip">
        <div class="progress-summary-item">
          <span class="progress-stat-label">总任务数</span>
          <strong class="progress-stat-value">{{ progress.total }}</strong>
        </div>
        <div class="progress-summary-item">
          <span class="progress-stat-label">已完成</span>
          <strong class="progress-stat-value success">{{ progress.completed }}</strong>
        </div>
        <div class="progress-summary-item" :class="{ warning: progress.failed > 0 }">
          <span class="progress-stat-label">失败</span>
          <strong class="progress-stat-value danger">{{ progress.failed }}</strong>
        </div>
        <div class="progress-summary-item">
          <span class="progress-stat-label">剩余</span>
          <strong class="progress-stat-value">{{ remainingProgress }}</strong>
        </div>
      </div>

      <div class="progress-bar-inline">
        <el-progress :percentage="progressPercentage" :status="progressStatus" :stroke-width="16" />
      </div>

      <div class="runtime-inline-list progress-inline-list progress-table-shell">
        <div class="runtime-inline-row">
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">运行状态</span>
            <el-tag :type="progress.is_running ? 'success' : 'info'" effect="light" round>
              {{ progress.is_running ? '运行中' : '已停止' }}
            </el-tag>
          </div>
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">当前照片</span>
            <span class="runtime-inline-value mono">{{ progress.current_photo_id ? `Photo #${progress.current_photo_id}` : '-' }}</span>
          </div>
        </div>
        <div class="runtime-inline-row">
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">开始时间</span>
            <span class="runtime-inline-value">{{ formatTime(progress.started_at) }}</span>
          </div>
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">运行模式</span>
            <span class="runtime-inline-value">{{ progressModeText }}</span>
          </div>
        </div>
        <div class="runtime-inline-row">
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">当前状态</span>
            <span class="runtime-inline-value">{{ progressStatusText }}</span>
          </div>
          <div class="runtime-inline-item">
            <span class="runtime-inline-label">当前消息</span>
            <span class="runtime-inline-value">{{ progress.current_message || '-' }}</span>
          </div>
        </div>
      </div>
    </el-card>

    <el-empty v-else description="暂无分析任务" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
import { ElMessage } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { Cpu, DataLine, MagicStick, Setting } from '@element-plus/icons-vue'
import { aiApi } from '@/api/ai'
import type { AIAnalyzeProgress, AIProviderInfo, AnalysisRuntimeStatus } from '@/types/ai'
import dayjs from 'dayjs'

const providerInfo = ref<AIProviderInfo | null>(null)
const progress = ref<AIAnalyzeProgress | null>(null)
const runtimeStatus = ref<AnalysisRuntimeStatus | null>(null)
const batchLimit = ref(100)
const analyzing = ref(false)
const backgroundLogs = ref<string[]>([])
let progressTimer: any = null
const logContainerRef = ref<HTMLElement | null>(null)

const runtimeModeText = computed(() => {
  if (!runtimeStatus.value?.is_active) return '-'

  switch (runtimeStatus.value.owner_type) {
    case 'batch':
      return '在线批量分析'
    case 'background':
      return '在线后台分析'
    case 'analyzer':
      return '离线 analyzer'
    default:
      return runtimeStatus.value.owner_type || '-'
  }
})

const batchAnalyzeDisabled = computed(() => {
  return !providerInfo.value || (!!runtimeStatus.value?.is_active && runtimeStatus.value.owner_type !== 'batch')
})

const batchDisabledReason = computed(() => {
  if (!providerInfo.value) return '请先配置 AI Provider'
  if (!runtimeStatus.value?.is_active) return ''
  if (runtimeStatus.value.owner_type === 'analyzer') return '离线 analyzer 正在运行'
  if (runtimeStatus.value.owner_type === 'background') return '在线后台分析正在运行'
  if (runtimeStatus.value.owner_type === 'batch' && !analyzing.value) return '在线批量分析正在运行'
  return ''
})

const backgroundRunning = computed(() => {
  return !!runtimeStatus.value?.is_active && runtimeStatus.value.owner_type === 'background'
})

const backgroundStartDisabled = computed(() => {
  return !providerInfo.value || (!!runtimeStatus.value?.is_active && runtimeStatus.value.owner_type !== 'background')
})

const backgroundDisabledReason = computed(() => {
  if (!providerInfo.value) return '请先配置 AI Provider'
  if (!runtimeStatus.value?.is_active) return ''
  if (runtimeStatus.value.owner_type === 'analyzer') return '离线 analyzer 正在运行'
  if (runtimeStatus.value.owner_type === 'batch') return '在线批量分析正在运行'
  return ''
})

const progressModeText = computed(() => {
  if (!progress.value?.mode) return '-'
  switch (progress.value.mode) {
    case 'batch':
      return '在线批量分析'
    case 'background':
      return '在线后台分析'
    default:
      return progress.value.mode
  }
})

const progressStatusText = computed(() => {
  if (!progress.value?.status) return '-'
  switch (progress.value.status) {
    case 'running':
      return '运行中'
    case 'sleeping':
      return '等待新任务'
    case 'stopping':
      return '停止中'
    case 'completed':
      return '已完成'
    default:
      return progress.value.status
  }
})

const providerPillText = computed(() => {
  if (!providerInfo.value) return '未配置'
  return providerInfo.value.is_available ? '可用' : '不可用'
})

const providerPillClass = computed(() => {
  if (!providerInfo.value) return 'warning'
  return providerInfo.value.is_available ? 'success' : 'danger'
})

const runtimePillText = computed(() => {
  return runtimeStatus.value?.is_active ? '运行中' : '空闲'
})

const runtimePillClass = computed(() => {
  return runtimeStatus.value?.is_active ? 'warning' : 'success'
})

const progressPillText = computed(() => {
  if (!progress.value) return '未开始'
  return progress.value.is_running ? '运行中' : '已停止'
})

const progressPillClass = computed(() => {
  if (!progress.value) return 'info'
  if (progress.value.is_running) return 'success'
  if (progress.value.failed > 0) return 'warning'
  return 'info'
})

const remainingProgress = computed(() => {
  if (!progress.value) return 0
  return Math.max(progress.value.total - progress.value.completed - progress.value.failed, 0)
})

const progressPercentage = computed(() => {
  if (!progress.value?.total) return 0
  return Math.round((progress.value.completed / progress.value.total) * 100)
})

const progressStatus = computed(() => {
  if (!progress.value) return undefined
  if (progress.value.is_running) return undefined
  if (progress.value.failed > 0) return 'warning'
  return 'success'
})

const formatTime = (time?: string) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

const loadProviderInfo = async () => {
  try {
    const res = await aiApi.getProviderInfo()
    providerInfo.value = res.data?.data || null
  } catch (error) {
    console.error('Failed to load provider info:', error)
  }
}

const loadProgress = async () => {
  try {
    const res = await aiApi.getProgress()
    progress.value = res.data?.data || null
    analyzing.value = !!progress.value?.is_running && progress.value?.mode === 'batch'
  } catch (error) {
    console.error('Failed to load progress:', error)
  }
}

const loadRuntimeStatus = async () => {
  try {
    const res = await aiApi.getRuntimeStatus()
    runtimeStatus.value = res.data?.data || null
  } catch (error) {
    console.error('Failed to load runtime status:', error)
  }
}

const loadBackgroundLogs = async () => {
  try {
    const res = await aiApi.getBackgroundLogs()
    backgroundLogs.value = res.data?.data?.lines || []
  } catch (error) {
    console.error('Failed to load background logs:', error)
  }
}

const handleBatchAnalyze = async () => {
  if (!providerInfo.value) {
    ElMessage.warning('请先配置 AI Provider')
    return
  }

  try {
    analyzing.value = true
    const res = await aiApi.analyzeBatch(batchLimit.value)
    ElMessage.success(`已提交 ${res.data?.data?.queued || 0} 张照片进行分析`)
    await loadRuntimeStatus()
    startProgressPolling()
  } catch (error: any) {
    if (error.response?.status === 409) {
      const ownerType = error.response?.data?.data?.owner_type
      const ownerLabel = ownerType === 'analyzer'
        ? '离线 analyzer'
        : ownerType === 'background'
          ? '在线后台分析'
          : ownerType === 'batch'
            ? '在线批量分析'
            : '其他分析器'
      ElMessage.warning(`当前 ${ownerLabel} 正在运行，请稍后再试`)
      await loadRuntimeStatus()
    } else if (error.response?.status === 503) {
      ElMessage.warning({
        message: 'AI 服务未配置或不可用，请先在配置管理中配置 AI Provider',
        duration: 5000,
      })
    } else {
      ElMessage.error(error.message || '批量分析失败')
    }
    analyzing.value = false
  }
}

const handleStartBackground = async () => {
  if (!providerInfo.value) {
    ElMessage.warning('请先配置 AI Provider')
    return
  }

  try {
    await aiApi.startBackground()
    ElMessage.success('后台分析已启动')
    await Promise.all([loadRuntimeStatus(), loadProgress(), loadBackgroundLogs()])
    startProgressPolling()
  } catch (error: any) {
    if (error.response?.status === 409) {
      const ownerType = error.response?.data?.data?.owner_type
      const ownerLabel = ownerType === 'analyzer'
        ? '离线 analyzer'
        : ownerType === 'batch'
          ? '在线批量分析'
          : ownerType === 'background'
            ? '在线后台分析'
            : '其他分析器'
      ElMessage.warning(`当前 ${ownerLabel} 正在运行，请稍后再试`)
      await loadRuntimeStatus()
    } else if (error.response?.status === 503) {
      ElMessage.warning('AI 服务未配置或不可用，请先在配置管理中配置 AI Provider')
    } else {
      ElMessage.error(error.message || '启动后台分析失败')
    }
  }
}

const handleStopBackground = async () => {
  try {
    await aiApi.stopBackground()
    ElMessage.success('后台分析正在停止')
    await Promise.all([loadRuntimeStatus(), loadProgress(), loadBackgroundLogs()])
    startProgressPolling()
  } catch (error: any) {
    ElMessage.error(error.message || '停止后台分析失败')
  }
}

const startProgressPolling = () => {
  if (progressTimer) {
    clearInterval(progressTimer)
  }

  progressTimer = setInterval(async () => {
    await Promise.all([loadProgress(), loadRuntimeStatus(), loadBackgroundLogs()])

    if (!progress.value?.is_running && !runtimeStatus.value?.is_active) {
      clearInterval(progressTimer)
      progressTimer = null

      if (analyzing.value) {
        ElMessage.success('批量分析已完成')
      }

      analyzing.value = false
    }
  }, 2000)
}

const stopProgressPolling = () => {
  if (progressTimer) {
    clearInterval(progressTimer)
    progressTimer = null
  }
}

onMounted(async () => {
  await loadProviderInfo()
  await Promise.all([loadProgress(), loadRuntimeStatus(), loadBackgroundLogs()])

  if (progress.value?.is_running || runtimeStatus.value?.is_active) {
    analyzing.value = !!progress.value?.is_running && progress.value?.mode === 'batch'
    startProgressPolling()
  }
})

onUnmounted(() => {
  stopProgressPolling()
})

watch(backgroundLogs, async () => {
  await nextTick()
  if (logContainerRef.value) {
    logContainerRef.value.scrollTop = logContainerRef.value.scrollHeight
  }
})
</script>

<style scoped>
.analysis-page {
  padding: var(--spacing-xl);
  background: var(--color-bg-primary);
  min-height: 100vh;
}

.header-action-btn {
  height: 36px;
  padding-inline: 16px;
  border-radius: 999px;
  border-color: var(--color-border);
}

.section-card {
  margin-bottom: var(--spacing-xl);
  border-radius: 24px;
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.section-card :deep(.el-card__header) {
  padding: 22px 28px;
  border-bottom: 1px solid var(--color-border);
}

.section-card :deep(.el-card__body) {
  padding: 24px 28px;
}

.status-pill,
.header-actions-inline {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.status-pill {
  justify-content: center;
  min-height: 32px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid #d9d9d9;
  background: #fff;
  color: var(--color-text-secondary);
  font-size: 13px;
  font-weight: var(--font-weight-medium);
}

.status-pill.success {
  color: #389e0d;
  background: #f6ffed;
  border-color: #b7eb8f;
}

.status-pill.warning {
  color: #d46b08;
  background: #fff7e6;
  border-color: #ffd591;
}

.status-pill.danger {
  color: #cf1322;
  background: #fff1f0;
  border-color: #ffa39e;
}

.status-pill.info {
  color: var(--color-text-secondary);
  background: #fafafa;
  border-color: #d9d9d9;
}

.provider-alert {
  border-radius: 18px;
}

.provider-inline-row {
  display: flex;
  align-items: center;
  gap: 32px;
  padding: 4px 12px;
  flex-wrap: wrap;
}

.provider-inline-item {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  min-height: 40px;
}

.provider-inline-label {
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: var(--font-weight-medium);
}

.runtime-inline-list {
  width: 100%;
}

.runtime-inline-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 24px;
  min-height: 56px;
  padding: 0 12px;
  align-items: center;
  border-top: 1px solid var(--color-border);
}

.runtime-inline-row:first-child {
  border-top: none;
}

.runtime-inline-row-full {
  min-height: auto;
  padding-top: 14px;
  padding-bottom: 14px;
}

.runtime-inline-item {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.runtime-inline-item-full {
  align-items: flex-start;
}

.runtime-inline-label {
  flex-shrink: 0;
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: var(--font-weight-medium);
}

.runtime-inline-value {
  color: var(--color-text-primary);
  font-size: 14px;
  line-height: 1.6;
}

.flat-table {
  width: 100%;
}

.info-table-head,
.info-table-row {
  display: grid;
  align-items: center;
}

.info-table-provider,
.info-table-runtime,
.info-table-progress {
  grid-template-columns: 180px minmax(0, 1fr);
}

.info-table-head {
  min-height: 44px;
  padding: 0 12px 12px;
  color: var(--color-text-tertiary);
  font-size: 13px;
  font-weight: var(--font-weight-semibold);
}

.info-table-row {
  min-height: 56px;
  padding: 0 12px;
  border-top: 1px solid var(--color-border);
}

.info-table-label {
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: var(--font-weight-medium);
}

.info-table-value {
  color: var(--color-text-primary);
  font-size: 14px;
  line-height: 1.6;
}

.info-table-value.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.info-table-row-full {
  align-items: flex-start;
  padding-top: 14px;
  padding-bottom: 14px;
}

.section-content {
  padding-inline: 4px;
}

.control-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.control-row-stack {
  align-items: flex-start;
}

.control-row-main {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}

.control-label {
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: var(--font-weight-medium);
}

.input-number-width-lg {
  width: 200px;
}

.action-btn-primary,
.action-btn-danger,
.mini-action-btn {
  border-radius: 999px;
  font-weight: var(--font-weight-semibold);
}

.action-btn-primary,
.action-btn-danger {
  min-width: 132px;
}

.mini-action-btn {
  height: 32px;
  padding-inline: 14px;
}

.inline-note {
  margin-top: 16px;
  color: var(--color-text-secondary);
  font-size: 14px;
  line-height: 1.7;
}

.section-alert {
  margin-top: 16px;
  border-radius: 16px;
}

.flat-log-panel {
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid var(--color-border);
}

.background-log-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  color: var(--color-text-primary);
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
}

.background-log-body {
  height: 240px;
  padding: 14px 16px;
  overflow-y: auto;
  border: 1px solid var(--color-border);
  border-radius: 16px;
  background: #fff;
}

.background-log-body pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--el-font-family-monospace, monospace);
  font-size: 12px;
  line-height: 1.6;
}

.background-log-empty {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.progress-summary-strip {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-bottom: 16px;
  border-top: 1px solid var(--color-border);
  border-bottom: 1px solid var(--color-border);
}

.progress-summary-item {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px 12px;
}

.progress-summary-item + .progress-summary-item {
  border-left: 1px solid var(--color-border);
}

.progress-summary-item.warning {
  background: #fffaf2;
}

.progress-stat-label {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.progress-stat-value {
  color: var(--color-text-primary);
  font-size: 30px;
  line-height: 1;
}

.progress-stat-value.success {
  color: #0d8a4f;
}

.progress-stat-value.danger {
  color: #cf1322;
}

.progress-bar-inline {
  margin: 16px 0 20px;
}

.progress-bar-inline :deep(.el-progress-bar__outer) {
  background: #eef2f6;
}

.progress-bar-inline :deep(.el-progress-bar__inner) {
  border-radius: 999px;
}

.progress-inline-list .runtime-inline-item {
  align-items: flex-start;
}

.progress-table-shell {
  margin-top: 0;
}

@media (max-width: 960px) {
  .progress-summary-strip {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .progress-summary-item:nth-child(3),
  .progress-summary-item:nth-child(4) {
    border-top: 1px solid var(--color-border);
  }

  .progress-summary-item:nth-child(3) {
    border-left: none;
  }
}

@media (max-width: 768px) {
  .analysis-page {
    padding: var(--spacing-lg);
  }

  .section-card :deep(.el-card__header),
  .section-card :deep(.el-card__body) {
    padding: 20px;
  }

  .info-table-provider,
  .info-table-runtime,
  .info-table-progress {
    grid-template-columns: 120px minmax(0, 1fr);
  }

  .runtime-inline-row {
    grid-template-columns: 1fr;
    gap: 12px;
    padding-top: 12px;
    padding-bottom: 12px;
  }

  .control-row,
  .control-row-main {
    align-items: stretch;
  }
}

@media (max-width: 640px) {
  .analysis-page {
    padding: var(--spacing-md);
  }

  .progress-summary-strip {
    grid-template-columns: 1fr;
  }

  .progress-summary-item + .progress-summary-item {
    border-left: none;
    border-top: 1px solid var(--color-border);
  }

  .info-table-head {
    display: none;
  }

  .info-table-provider,
  .info-table-runtime,
  .info-table-progress,
  .info-table-row {
    grid-template-columns: 1fr;
  }

  .info-table-row {
    gap: 6px;
    padding-top: 14px;
    padding-bottom: 14px;
  }

  .background-log-header,
  .header-actions-inline {
    flex-wrap: wrap;
  }
}
</style>
