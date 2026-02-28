<template>
  <div class="photo-detail" v-loading="loading">
    <el-card shadow="never" v-if="photo">
      <template #header>
        <div class="header">
          <el-button type="primary" link @click="goBack">
            <el-icon><ArrowLeft /></el-icon>
            返回
          </el-button>
          <div>
            <el-button type="primary" @click="handleAnalyze" :loading="analyzing">
              {{ analyzing ? '分析中...' : '重新分析' }}
            </el-button>
          </div>
        </div>
      </template>

      <el-row :gutter="20">
        <!-- 左侧：照片预览 -->
        <el-col :span="12">
          <el-image
            :src="getPhotoUrl(photo.id)"
            :preview-src-list="[getPhotoUrl(photo.id)]"
            fit="contain"
            style="width: 100%; border-radius: 8px"
          />
        </el-col>

        <!-- 右侧：照片信息 -->
        <el-col :span="12">
          <!-- 基本信息 -->
          <el-descriptions title="基本信息" :column="1" border>
            <el-descriptions-item label="文件路径">{{ photo.file_path }}</el-descriptions-item>
            <el-descriptions-item label="文件大小">{{ formatSize(photo.file_size) }}</el-descriptions-item>
            <el-descriptions-item label="拍摄时间">{{ formatTime(photo.taken_at) }}</el-descriptions-item>
            <el-descriptions-item label="导入时间">{{ formatTime(photo.created_at) }}</el-descriptions-item>
            <el-descriptions-item label="设备ID">{{ photo.esp32_device_id || '-' }}</el-descriptions-item>
            <el-descriptions-item label="文件哈希">
              <el-tag size="small">{{ photo.file_hash?.substring(0, 16) }}...</el-tag>
            </el-descriptions-item>
          </el-descriptions>

          <!-- AI 分析结果 -->
          <el-divider />
          <div v-if="photo.is_analyzed">
            <h3>AI 分析结果</h3>
            <el-descriptions :column="2" border style="margin-top: 16px">
              <el-descriptions-item label="综合评分" :span="2">
                <el-progress
                  :percentage="photo.overall_score || 0"
                  :color="getScoreColor(photo.overall_score || 0)"
                  :stroke-width="20"
                />
              </el-descriptions-item>
              <el-descriptions-item label="记忆价值">{{ photo.memory_score?.toFixed(2) }}</el-descriptions-item>
              <el-descriptions-item label="美学评分">{{ photo.beauty_score?.toFixed(2) }}</el-descriptions-item>
              <el-descriptions-item label="情感评分">{{ photo.emotion_score?.toFixed(2) }}</el-descriptions-item>
              <el-descriptions-item label="技术质量">{{ photo.technical_score?.toFixed(2) }}</el-descriptions-item>
            </el-descriptions>

            <!-- 标签 -->
            <div style="margin-top: 20px" v-if="photo.tags">
              <h4>标签</h4>
              <el-tag
                v-for="tag in photo.tags"
                :key="tag"
                style="margin-right: 8px; margin-top: 8px"
              >
                {{ tag }}
              </el-tag>
            </div>

            <!-- 分析描述 -->
            <div style="margin-top: 20px" v-if="photo.analysis_result">
              <h4>AI 描述</h4>
              <el-card shadow="never" style="margin-top: 8px">
                <p style="white-space: pre-wrap; line-height: 1.6">{{ photo.analysis_result }}</p>
              </el-card>
            </div>

            <!-- 分析时间和提供商 -->
            <el-descriptions :column="2" border style="margin-top: 20px">
              <el-descriptions-item label="分析时间">
                {{ formatTime(photo.analyzed_at) }}
              </el-descriptions-item>
              <el-descriptions-item label="AI 提供商">
                <el-tag>{{ photo.ai_provider || '-' }}</el-tag>
              </el-descriptions-item>
            </el-descriptions>
          </div>
          <el-empty v-else description="照片尚未分析" />
        </el-col>
      </el-row>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { photoApi } from '@/api/photo'
import { aiApi } from '@/api/ai'
import type { Photo } from '@/types/photo'
import dayjs from 'dayjs'

const route = useRoute()
const router = useRouter()

const photo = ref<Photo | null>(null)
const loading = ref(false)
const analyzing = ref(false)

// 获取照片 URL
const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return `${baseUrl}/photos/${photoId}/image`
}

// 格式化时间
const formatTime = (time?: string) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

// 格式化文件大小
const formatSize = (size?: number) => {
  if (!size) return '-'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(2)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(2)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// 根据评分获取颜色
const getScoreColor = (score: number) => {
  if (score >= 80) return '#67c23a'
  if (score >= 60) return '#e6a23c'
  return '#f56c6c'
}

// 加载照片详情
const loadPhoto = async () => {
  loading.value = true
  try {
    const photoId = Number(route.params.id)
    const res = await photoApi.getById(photoId)
    photo.value = res.data || null
  } catch (error: any) {
    ElMessage.error(error.message || '加载照片详情失败')
  } finally {
    loading.value = false
  }
}

// 重新分析
const handleAnalyze = async () => {
  if (!photo.value) return

  try {
    analyzing.value = true
    await aiApi.analyze(photo.value.id)
    ElMessage.success('分析请求已提交')

    // 轮询结果
    const timer = setInterval(async () => {
      await loadPhoto()
      if (photo.value?.is_analyzed) {
        clearInterval(timer)
        analyzing.value = false
        ElMessage.success('分析完成')
      }
    }, 2000)

    // 30秒超时
    setTimeout(() => {
      clearInterval(timer)
      analyzing.value = false
    }, 30000)
  } catch (error: any) {
    analyzing.value = false
    ElMessage.error(error.message || '分析失败')
  }
}

// 返回
const goBack = () => {
  router.back()
}

onMounted(() => {
  loadPhoto()
})
</script>

<style scoped>
.photo-detail {
  padding: 20px;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

h3,
h4 {
  color: #303133;
  margin: 0;
}

h3 {
  font-size: 18px;
  font-weight: bold;
}

h4 {
  font-size: 16px;
  font-weight: 600;
}
</style>
