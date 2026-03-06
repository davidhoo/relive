<template>
  <div class="analysis-page">
    <!-- AI Provider 信息 -->
    <el-card shadow="never" style="margin-bottom: 20px">
      <template #header>
        <span><el-icon><Setting /></el-icon> AI Provider 配置</span>
      </template>

      <!-- AI 未配置提示 -->
      <el-alert
        v-if="!providerInfo"
        type="warning"
        title="AI 服务未配置"
        description="请先在配置管理中配置 AI Provider (Ollama/Qwen/OpenAI) 才能使用 AI 分析功能"
        show-icon
        :closable="false"
        style="margin-bottom: 20px"
      >
        <template #default>
          <el-button type="primary" size="small" @click="$router.push('/config')">
            前往配置
          </el-button>
        </template>
      </el-alert>

      <el-descriptions :column="2" border v-if="providerInfo">
        <el-descriptions-item label="当前 Provider">
          <el-tag type="primary" size="large">{{ providerInfo.name }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="providerInfo.is_available ? 'success' : 'danger'">
            {{ providerInfo.is_available ? '可用' : '不可用' }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="估算成本" :span="2">
          {{ providerInfo.estimated_cost || '免费' }}
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card shadow="never" style="margin-bottom: 20px">
      <template #header>
        <span>分析运行状态</span>
      </template>

      <el-descriptions :column="2" border>
        <el-descriptions-item label="当前状态">
          <el-tag :type="runtimeStatus?.is_active ? 'warning' : 'success'">
            {{ runtimeStatus?.is_active ? '已占用' : '空闲' }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="占用模式">
          {{ runtimeModeText }}
        </el-descriptions-item>
        <el-descriptions-item label="占用实例">
          {{ runtimeStatus?.owner_id || '-' }}
        </el-descriptions-item>
        <el-descriptions-item label="开始时间">
          {{ formatTime(runtimeStatus?.started_at) }}
        </el-descriptions-item>
        <el-descriptions-item label="说明" :span="2">
          {{ runtimeStatus?.message || '当前没有分析器占用运行权' }}
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <!-- 批量分析 -->
    <el-card shadow="never" style="margin-bottom: 20px">
      <template #header>
        <span><el-icon><MagicStick /></el-icon> 批量分析</span>
      </template>
      <div class="batch-analyze-form">
        <div class="batch-analyze-row">
          <span class="batch-label">分析数量：</span>
          <el-input-number
            v-model="batchLimit"
            :min="1"
            :max="1000"
            :step="10"
            style="width: 200px"
          />
          <el-button
            type="primary"
            size="large"
            @click="handleBatchAnalyze"
            :loading="analyzing"
            :disabled="batchAnalyzeDisabled"
          >
            {{ analyzing ? '分析中...' : '开始批量分析' }}
          </el-button>
          <el-text v-if="batchDisabledReason" type="info" style="margin-left: 10px">
            {{ batchDisabledReason }}
          </el-text>
        </div>
        <el-alert
          title="批量分析说明"
          type="info"
          :closable="false"
          description="批量分析将按照队列顺序处理未分析的照片。建议每次处理数量不超过 500 张，避免长时间占用资源。"
          style="margin-top: 16px"
        />
      </div>
    </el-card>

    <el-card shadow="never" style="margin-bottom: 20px">
      <template #header>
        <span>后台分析</span>
      </template>
      <div class="batch-analyze-form">
        <div class="batch-analyze-row">
          <el-button
            v-if="!backgroundRunning"
            type="primary"
            size="large"
            @click="handleStartBackground"
            :disabled="backgroundStartDisabled"
          >
            开启后台分析
          </el-button>
          <el-button
            v-else
            type="danger"
            size="large"
            @click="handleStopBackground"
          >
            停止后台分析
          </el-button>
          <el-text v-if="backgroundDisabledReason" type="info" style="margin-left: 10px">
            {{ backgroundDisabledReason }}
          </el-text>
        </div>
        <el-alert
          title="后台分析说明"
          type="info"
          :closable="false"
          description="后台分析会持续扫描未分析照片并自动处理；没有新照片时会短暂等待后继续轮询。"
          style="margin-top: 16px"
        />

        <div class="background-log-panel">
          <div class="background-log-header">
            <span>任务日志（最后 100 行）</span>
            <el-button size="small" text @click="loadBackgroundLogs">刷新</el-button>
          </div>
          <div class="background-log-body" ref="logContainerRef">
            <pre v-if="backgroundLogs.length">{{ backgroundLogs.join('\n') }}</pre>
            <div v-else class="background-log-empty">暂无后台分析日志</div>
          </div>
        </div>
      </div>
    </el-card>

    <!-- 分析进度 -->
    <el-card shadow="never" v-if="progress">
      <template #header>
        <div style="display: flex; justify-content: space-between; align-items: center">
          <span><el-icon><DataLine /></el-icon> 分析进度</span>
          <el-button size="small" @click="loadProgress">刷新</el-button>
        </div>
      </template>

      <el-row :gutter="20" style="margin-bottom: 20px">
        <el-col :span="6">
          <el-statistic title="总任务数" :value="progress.total" />
        </el-col>
        <el-col :span="6">
          <el-statistic title="已完成" :value="progress.completed" />
        </el-col>
        <el-col :span="6">
          <el-statistic title="失败" :value="progress.failed" />
        </el-col>
        <el-col :span="6">
          <el-statistic title="剩余" :value="progress.total - progress.completed - progress.failed" />
        </el-col>
      </el-row>

      <el-progress
        :percentage="progressPercentage"
        :status="progressStatus"
        :stroke-width="24"
      />

      <div style="margin-top: 20px">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="运行状态">
            <el-tag :type="progress.is_running ? 'success' : 'info'">
              {{ progress.is_running ? '运行中' : '已停止' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="当前照片">
            {{ progress.current_photo_id ? `Photo #${progress.current_photo_id}` : '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="开始时间" :span="2">
            {{ formatTime(progress.started_at) }}
          </el-descriptions-item>
          <el-descriptions-item label="运行模式">
            {{ progressModeText }}
          </el-descriptions-item>
          <el-descriptions-item label="当前状态">
            {{ progressStatusText }}
          </el-descriptions-item>
          <el-descriptions-item label="当前消息" :span="2">
            {{ progress.current_message || '-' }}
          </el-descriptions-item>
        </el-descriptions>
      </div>
    </el-card>

    <el-empty v-else description="暂无分析任务" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
import { ElMessage } from 'element-plus'
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

// 进度百分比
const progressPercentage = computed(() => {
  if (!progress.value?.total) return 0
  return Math.round((progress.value.completed / progress.value.total) * 100)
})

// 进度状态
const progressStatus = computed(() => {
  if (!progress.value) return undefined
  if (progress.value.is_running) return undefined
  if (progress.value.failed > 0) return 'warning'
  return 'success'
})

// 格式化时间
const formatTime = (time?: string) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

// 加载 Provider 信息
const loadProviderInfo = async () => {
  try {
    const res = await aiApi.getProviderInfo()
    providerInfo.value = res.data?.data || null
  } catch (error) {
    console.error('Failed to load provider info:', error)
  }
}

// 加载进度
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

// 批量分析
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

    // 开始轮询进度
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
        duration: 5000
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

// 开始轮询进度
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

// 停止轮询
const stopProgressPolling = () => {
  if (progressTimer) {
    clearInterval(progressTimer)
    progressTimer = null
  }
}

onMounted(async () => {
  await loadProviderInfo()
  await Promise.all([loadProgress(), loadRuntimeStatus(), loadBackgroundLogs()])

  // 如果有正在运行的任务，开始轮询
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
  padding: 20px;
}

.batch-analyze-form {
  width: 100%;
}

.batch-analyze-row {
  display: flex;
  align-items: center;
  gap: 16px;
}

.batch-label {
  font-size: 14px;
  color: var(--color-text-secondary);
}

.background-log-panel {
  margin-top: 16px;
}

.background-log-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.background-log-body {
  height: 240px;
  padding: 12px;
  overflow-y: auto;
  border: 1px solid var(--el-border-color);
  border-radius: 6px;
  background: var(--el-fill-color-light);
}

.background-log-body pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--el-font-family-monospace, monospace);
  font-size: 12px;
  line-height: 1.5;
}

.background-log-empty {
  color: var(--color-text-secondary);
  font-size: 13px;
}
</style>
