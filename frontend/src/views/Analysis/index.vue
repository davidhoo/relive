<template>
  <div class="analysis-page">
    <!-- AI Provider 信息 -->
    <el-card shadow="never" style="margin-bottom: 20px">
      <template #header>
        <span><el-icon><Setting /></el-icon> AI Provider 配置</span>
      </template>
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

    <!-- 批量分析 -->
    <el-card shadow="never" style="margin-bottom: 20px">
      <template #header>
        <span><el-icon><MagicStick /></el-icon> 批量分析</span>
      </template>
      <el-row :gutter="20">
        <el-col :span="12">
          <el-form label-width="120px">
            <el-form-item label="分析数量">
              <el-input-number
                v-model="batchLimit"
                :min="1"
                :max="1000"
                :step="10"
                style="width: 200px"
              />
            </el-form-item>
            <el-form-item>
              <el-button
                type="primary"
                size="large"
                @click="handleBatchAnalyze"
                :loading="analyzing"
              >
                {{ analyzing ? '分析中...' : '开始批量分析' }}
              </el-button>
            </el-form-item>
          </el-form>
        </el-col>
        <el-col :span="12">
          <el-alert
            title="批量分析说明"
            type="info"
            :closable="false"
            description="批量分析将按照队列顺序处理未分析的照片。建议每次处理数量不超过 500 张，避免长时间占用资源。"
          />
        </el-col>
      </el-row>
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
        </el-descriptions>
      </div>
    </el-card>

    <el-empty v-else description="暂无分析任务" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { aiApi } from '@/api/ai'
import type { AIAnalyzeProgress, AIProviderInfo } from '@/types/ai'
import dayjs from 'dayjs'

const providerInfo = ref<AIProviderInfo | null>(null)
const progress = ref<AIAnalyzeProgress | null>(null)
const batchLimit = ref(100)
const analyzing = ref(false)
let progressTimer: any = null

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
    providerInfo.value = res.data || null
  } catch (error) {
    console.error('Failed to load provider info:', error)
  }
}

// 加载进度
const loadProgress = async () => {
  try {
    const res = await aiApi.getProgress()
    progress.value = res.data || null
  } catch (error) {
    console.error('Failed to load progress:', error)
  }
}

// 批量分析
const handleBatchAnalyze = async () => {
  try {
    analyzing.value = true
    const res = await aiApi.analyzeBatch(batchLimit.value)
    ElMessage.success(`已提交 ${res.data?.queued || 0} 张照片进行分析`)

    // 开始轮询进度
    startProgressPolling()
  } catch (error: any) {
    ElMessage.error(error.message || '批量分析失败')
    analyzing.value = false
  }
}

// 开始轮询进度
const startProgressPolling = () => {
  if (progressTimer) {
    clearInterval(progressTimer)
  }

  progressTimer = setInterval(async () => {
    await loadProgress()

    if (!progress.value?.is_running) {
      clearInterval(progressTimer)
      progressTimer = null
      analyzing.value = false
      ElMessage.success('批量分析已完成')
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
  await loadProgress()

  // 如果有正在运行的任务，开始轮询
  if (progress.value?.is_running) {
    analyzing.value = true
    startProgressPolling()
  }
})

onUnmounted(() => {
  stopProgressPolling()
})
</script>

<style scoped>
.analysis-page {
  padding: 20px;
}
</style>
