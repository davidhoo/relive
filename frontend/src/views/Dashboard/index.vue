<template>
  <div class="dashboard">
    <el-row :gutter="20">
      <!-- 系统统计卡片 -->
      <el-col :span="6">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <el-icon><Picture /></el-icon>
              <span>总照片数</span>
            </div>
          </template>
          <div class="stat-value">{{ systemStats?.total_photos || 0 }}</div>
        </el-card>
      </el-col>

      <el-col :span="6">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <el-icon><MagicStick /></el-icon>
              <span>已分析</span>
            </div>
          </template>
          <div class="stat-value">{{ systemStats?.analyzed_photos || 0 }}</div>
          <div class="stat-subtitle">
            {{ analysisRate }}%
          </div>
        </el-card>
      </el-col>

      <el-col :span="6">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <el-icon><Monitor /></el-icon>
              <span>在线设备</span>
            </div>
          </template>
          <div class="stat-value">{{ systemStats?.online_devices || 0 }}</div>
          <div class="stat-subtitle">
            总计 {{ systemStats?.total_devices || 0 }} 台
          </div>
        </el-card>
      </el-col>

      <el-col :span="6">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <el-icon><DataLine /></el-icon>
              <span>存储空间</span>
            </div>
          </template>
          <div class="stat-value">{{ storageSize }}</div>
          <div class="stat-subtitle">{{ systemStats?.total_photos || 0 }} 张照片</div>
        </el-card>
      </el-col>
    </el-row>

    <!-- AI 分析进度 -->
    <el-row :gutter="20" style="margin-top: 20px">
      <el-col :span="24">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>AI 分析进度</span>
              <el-button
                type="primary"
                size="small"
                @click="handleStartAnalysis"
                :loading="analyzing"
              >
                {{ analyzing ? '分析中...' : '开始批量分析' }}
              </el-button>
            </div>
          </template>
          <div v-if="aiProgress">
            <el-progress
              :percentage="progressPercentage"
              :status="progressStatus"
              :stroke-width="20"
            />
            <div class="progress-info">
              <span>已完成: {{ aiProgress.completed }}/{{ aiProgress.total }}</span>
              <span>失败: {{ aiProgress.failed }}</span>
              <span v-if="aiProgress.current_photo_id">
                当前: Photo #{{ aiProgress.current_photo_id }}
              </span>
            </div>
          </div>
          <el-empty v-else description="暂无分析任务" />
        </el-card>
      </el-col>
    </el-row>

    <!-- 最近照片 -->
    <el-row :gutter="20" style="margin-top: 20px">
      <el-col :span="24">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>最近照片</span>
              <el-button type="primary" size="small" link @click="gotoPhotos">
                查看全部
              </el-button>
            </div>
          </template>
          <el-row :gutter="10" v-if="recentPhotos.length">
            <el-col
              :span="4"
              v-for="photo in recentPhotos"
              :key="photo.id"
              style="margin-bottom: 10px"
            >
              <el-image
                :src="getPhotoUrl(photo.id)"
                :preview-src-list="[getPhotoUrl(photo.id)]"
                fit="cover"
                style="width: 100%; height: 150px; border-radius: 4px; cursor: pointer"
                @click="gotoPhotoDetail(photo.id)"
              />
            </el-col>
          </el-row>
          <el-empty v-else description="暂无照片" />
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useSystemStore } from '@/stores/system'
import { photoApi } from '@/api/photo'
import { aiApi } from '@/api/ai'
import type { Photo } from '@/types/photo'
import type { AIAnalyzeProgress } from '@/types/ai'

const router = useRouter()
const systemStore = useSystemStore()

const recentPhotos = ref<Photo[]>([])
const aiProgress = ref<AIAnalyzeProgress | null>(null)
const analyzing = ref(false)

// 系统统计
const systemStats = computed(() => systemStore.stats)

// 分析率
const analysisRate = computed(() => {
  if (!systemStats.value?.total_photos) return 0
  return Math.round(
    (systemStats.value.analyzed_photos / systemStats.value.total_photos) * 100
  )
})

// 存储大小格式化
const storageSize = computed(() => {
  const size = systemStats.value?.storage_size || 0
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(2)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(2)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
})

// AI 进度百分比
const progressPercentage = computed(() => {
  if (!aiProgress.value?.total) return 0
  return Math.round((aiProgress.value.completed / aiProgress.value.total) * 100)
})

// 进度状态
const progressStatus = computed(() => {
  if (!aiProgress.value) return undefined
  if (aiProgress.value.is_running) return undefined
  if (aiProgress.value.failed > 0) return 'warning'
  return 'success'
})

// 获取照片 URL
const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return `${baseUrl}/photos/${photoId}/image`
}

// 加载最近照片
const loadRecentPhotos = async () => {
  try {
    const res = await photoApi.getList({ page: 1, page_size: 12 })
    recentPhotos.value = res.data?.items || []
  } catch (error) {
    console.error('Failed to load recent photos:', error)
  }
}

// 加载 AI 进度
const loadAIProgress = async () => {
  try {
    const res = await aiApi.getProgress()
    aiProgress.value = res.data || null
  } catch (error) {
    console.error('Failed to load AI progress:', error)
  }
}

// 开始批量分析
const handleStartAnalysis = async () => {
  try {
    analyzing.value = true
    await aiApi.analyzeBatch(100)
    ElMessage.success('批量分析已开始')

    // 轮询进度
    const timer = setInterval(async () => {
      await loadAIProgress()
      if (!aiProgress.value?.is_running) {
        clearInterval(timer)
        analyzing.value = false
        await systemStore.fetchStats()
        ElMessage.success('批量分析已完成')
      }
    }, 2000)
  } catch (error: any) {
    analyzing.value = false
    ElMessage.error(error.message || '启动批量分析失败')
  }
}

// 跳转到照片列表
const gotoPhotos = () => {
  router.push('/photos')
}

// 跳转到照片详情
const gotoPhotoDetail = (photoId: number) => {
  router.push(`/photos/${photoId}`)
}

onMounted(async () => {
  await systemStore.fetchStats()
  await loadRecentPhotos()
  await loadAIProgress()
})
</script>

<style scoped>
.dashboard {
  padding: 20px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: bold;
}

.card-header .el-icon {
  margin-right: 8px;
  font-size: 20px;
}

.stat-value {
  font-size: 36px;
  font-weight: bold;
  color: #409eff;
  text-align: center;
  margin: 20px 0;
}

.stat-subtitle {
  text-align: center;
  color: #909399;
  font-size: 14px;
}

.progress-info {
  display: flex;
  justify-content: space-around;
  margin-top: 20px;
  color: #606266;
}
</style>
