<template>
  <div class="system-page">
    <!-- 系统健康状态 -->
    <el-card shadow="never" style="margin-bottom: 20px">
      <template #header>
        <span><el-icon><Monitor /></el-icon> 系统健康状态</span>
      </template>
      <el-descriptions :column="2" border v-if="health">
        <el-descriptions-item label="状态">
          <el-tag :type="health.status === 'healthy' ? 'success' : 'danger'" size="large">
            {{ health.status }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="检查时间">
          {{ formatTime(health.timestamp) }}
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <!-- 系统信息 -->
    <el-card shadow="never">
      <template #header>
        <span><el-icon><InfoFilled /></el-icon> 系统信息</span>
      </template>
      <el-descriptions :column="2" border v-if="stats">
        <el-descriptions-item label="系统版本">
          <el-tag>v0.3.0</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="Go 版本">
          <el-tag>{{ stats.go_version || '-' }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="启动时间">
          {{ formatTime(stats.started_at) }}
        </el-descriptions-item>
        <el-descriptions-item label="运行时长">
          {{ formatDuration(stats.uptime) }}
        </el-descriptions-item>
        <el-descriptions-item label="照片总数">
          {{ stats.total_photos || 0 }}
        </el-descriptions-item>
        <el-descriptions-item label="已分析照片">
          {{ stats.analyzed_photos || 0 }}
        </el-descriptions-item>
        <el-descriptions-item label="设备总数">
          {{ stats.total_devices || 0 }}
        </el-descriptions-item>
        <el-descriptions-item label="在线设备">
          {{ stats.online_devices || 0 }}
        </el-descriptions-item>
        <el-descriptions-item label="存储空间">
          {{ formatSize(stats.storage_size) }}
        </el-descriptions-item>
        <el-descriptions-item label="数据库大小">
          {{ formatSize(stats.database_size) }}
        </el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useSystemStore } from '@/stores/system'
import dayjs from 'dayjs'
import duration from 'dayjs/plugin/duration'

dayjs.extend(duration)

const systemStore = useSystemStore()

const health = computed(() => systemStore.health)
const stats = computed(() => systemStore.stats)

// 格式化时间
const formatTime = (time?: string | number) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

// 格式化时长
const formatDuration = (seconds?: number) => {
  if (!seconds) return '-'
  const d = dayjs.duration(seconds, 'seconds')
  const days = Math.floor(d.asDays())
  const hours = d.hours()
  const minutes = d.minutes()

  if (days > 0) {
    return `${days} 天 ${hours} 小时 ${minutes} 分钟`
  }
  if (hours > 0) {
    return `${hours} 小时 ${minutes} 分钟`
  }
  return `${minutes} 分钟`
}

// 格式化文件大小
const formatSize = (size?: number) => {
  if (!size) return '-'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(2)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(2)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
}

onMounted(async () => {
  await systemStore.fetchHealth()
  await systemStore.fetchStats()
})
</script>

<style scoped>
.system-page {
  padding: 20px;
}
</style>
