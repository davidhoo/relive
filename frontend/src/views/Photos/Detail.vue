<template>
  <div class="photo-detail" v-loading="loading">
    <el-card shadow="never" v-if="photo">
      <template #header>
        <div class="header">
          <el-button link @click="goBack" class="back-link">
            <el-icon><ArrowLeft /></el-icon>
            返回
          </el-button>
          <div class="header-actions">
            <el-button @click="handleThumbnail" :loading="thumbnailing">
              {{ thumbnailing ? '生成中...' : (photo?.thumbnail_status === 'ready' ? '重新生成缩略图' : '生成缩略图') }}
            </el-button>
            <el-button @click="handleGeocode" :loading="geocoding" :disabled="!photo?.gps_latitude || !photo?.gps_longitude">
              {{ geocoding ? '解析中...' : (photo?.location ? '重新解析 GPS' : '解析 GPS') }}
            </el-button>
            <el-tooltip
              content="需要先配置 AI Provider 才能使用分析功能"
              placement="left"
              :disabled="false"
            >
              <el-button type="primary" @click="handleAnalyze" :loading="analyzing">
                {{ analyzing ? '分析中...' : (photo?.ai_analyzed ? '重新分析' : '分析') }}
              </el-button>
            </el-tooltip>
          </div>
        </div>
      </template>

      <el-row :gutter="20">
        <!-- 左侧：照片预览 -->
        <el-col :span="12">
          <el-image
            :src="getPhotoThumbnailUrl(photo.id, photo.updated_at)"
            :preview-src-list="[getPhotoUrl(photo.id)]"
            fit="contain"
            class="preview-image"
            preview-teleported
            :preview-props="{ zIndex: 9999 }"
          />
        </el-col>

        <!-- 右侧：照片信息 -->
        <el-col :span="12">
          <!-- 基本信息 -->
          <el-descriptions title="基本信息" :column="1" border>
            <el-descriptions-item label="文件路径">{{ photo.file_path }}</el-descriptions-item>
            <el-descriptions-item label="文件名">{{ photo.file_name }}</el-descriptions-item>
            <el-descriptions-item label="文件大小">{{ formatSize(photo.file_size) }}</el-descriptions-item>
            <el-descriptions-item label="文件哈希">
              <el-tag size="small">{{ photo.file_hash?.substring(0, 16) }}...</el-tag>
            </el-descriptions-item>
          </el-descriptions>

          <!-- EXIF 信息 -->
          <el-divider />
          <el-descriptions title="EXIF 信息" :column="1" border>
            <el-descriptions-item label="拍摄时间">{{ formatTime(photo.taken_at) }}</el-descriptions-item>
            <el-descriptions-item label="相机型号">{{ photo.camera_model || '-' }}</el-descriptions-item>
            <el-descriptions-item label="图片尺寸">
              {{ photo.width && photo.height ? `${photo.width} × ${photo.height}` : '-' }}
            </el-descriptions-item>
            <el-descriptions-item label="方向">{{ photo.orientation || '-' }}</el-descriptions-item>
            <el-descriptions-item label="GPS 坐标">
              {{ photo.gps_latitude && photo.gps_longitude
                ? `${photo.gps_latitude.toFixed(6)}, ${photo.gps_longitude.toFixed(6)}`
                : '-' }}
            </el-descriptions-item>
            <el-descriptions-item label="位置">{{ photo.location || (photo.geocode_status === 'pending' ? '解析中' : '-') }}</el-descriptions-item>
            <el-descriptions-item label="位置来源">{{ formatGeocodeProvider(photo.geocode_provider) }}</el-descriptions-item>
            <el-descriptions-item label="解析时间">{{ formatTime(photo.geocoded_at) }}</el-descriptions-item>
            <el-descriptions-item label="缩略图状态">{{ formatThumbnailStatus(photo.thumbnail_status) }}</el-descriptions-item>
            <el-descriptions-item label="缩略图时间">{{ formatTime(photo.thumbnail_generated_at) }}</el-descriptions-item>
          </el-descriptions>

          <!-- 文件时间信息 -->
          <el-divider />
          <el-descriptions title="文件时间" :column="2" border>
            <el-descriptions-item label="文件创建">{{ formatTime(photo.file_create_time) }}</el-descriptions-item>
            <el-descriptions-item label="文件修改">{{ formatTime(photo.file_mod_time) }}</el-descriptions-item>
            <el-descriptions-item label="导入时间">{{ formatTime(photo.created_at) }}</el-descriptions-item>
            <el-descriptions-item label="更新时间">{{ formatTime(photo.updated_at) }}</el-descriptions-item>
          </el-descriptions>

          <!-- AI 分析结果 -->
          <el-divider />
          <div v-if="photo.ai_analyzed">
            <h3>AI 分析结果</h3>
            <el-descriptions :column="2" border class="analysis-descriptions">
              <el-descriptions-item label="综合评分" :span="2">
                <el-progress
                  :percentage="photo.overall_score || 0"
                  :color="getScoreColor(photo.overall_score || 0)"
                  :stroke-width="20"
                />
              </el-descriptions-item>
              <el-descriptions-item label="记忆价值">{{ photo.memory_score?.toFixed(2) }}</el-descriptions-item>
              <el-descriptions-item label="美学评分">{{ photo.beauty_score?.toFixed(2) }}</el-descriptions-item>
              <el-descriptions-item label="评分理由" :span="2" v-if="photo.score_reason">
                <el-icon><InfoFilled /></el-icon>
                <span class="score-reason">{{ photo.score_reason }}</span>
              </el-descriptions-item>
              <el-descriptions-item label="AI 提供商">
                <el-tag type="success" size="small">{{ formatAIProvider(photo.ai_provider) }}</el-tag>
              </el-descriptions-item>
              <el-descriptions-item label="分析时间">{{ formatTime(photo.analyzed_at) }}</el-descriptions-item>
            </el-descriptions>

            <!-- 描述 -->
            <div class="detail-section" v-if="photo.description">
              <h4>照片描述</h4>
              <p class="detail-text-muted">{{ photo.description }}</p>
            </div>

            <!-- 标题 -->
            <div class="detail-section" v-if="photo.caption">
              <h4>标题</h4>
              <p class="detail-text-strong">{{ photo.caption }}</p>
            </div>

            <!-- 分类 -->
            <div class="detail-section" v-if="photo.main_category">
              <h4>分类</h4>
              <el-tag
                type="primary"
                size="large"
                class="clickable-tag"
                @click="handleTagClick(photo.main_category!)"
              >
                {{ photo.main_category }}
              </el-tag>
            </div>

            <!-- 标签 -->
            <div class="detail-section" v-if="photo.tags">
              <h4>标签</h4>
              <el-tag
                v-for="tag in photo.tags.split(',')"
                :key="tag"
                class="clickable-tag tag-chip"
                @click="handleTagClick(tag)"
              >
                {{ tag }}
              </el-tag>
            </div>

            <!-- 分析描述 -->
            <div class="detail-section" v-if="(photo as any).analysis_result">
              <h4>AI 描述</h4>
              <el-card shadow="never" class="analysis-card">
                <p class="analysis-result-text">{{ (photo as any).analysis_result }}</p>
              </el-card>
            </div>
          </div>
          <el-empty v-else description="照片尚未分析" />
        </el-col>
      </el-row>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, InfoFilled } from '@element-plus/icons-vue'
import { photoApi } from '@/api/photo'
import { aiApi } from '@/api/ai'
import { geocodeApi } from '@/api/geocode'
import { thumbnailApi } from '@/api/thumbnail'
import type { Photo } from '@/types/photo'
import dayjs from 'dayjs'
import { useUserStore } from '@/stores/user'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const photo = ref<Photo | null>(null)
const loading = ref(false)
const analyzing = ref(false)
const geocoding = ref(false)
const thumbnailing = ref(false)

// 统一管理所有轮询定时器，离开页面时清理
const activeTimers: ReturnType<typeof setInterval | typeof setTimeout>[] = []
const addTimer = (id: ReturnType<typeof setInterval | typeof setTimeout>) => {
  activeTimers.push(id)
  return id
}
const clearAllTimers = () => {
  activeTimers.forEach(id => clearInterval(id as any))
  activeTimers.length = 0
}

onBeforeUnmount(() => {
  clearAllTimers()
})

// 获取照片缩略图 URL
const getPhotoThumbnailUrl = (photoId: number, version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const token = userStore.token
  const params = new URLSearchParams()
  if (token) params.set('token', token)
  if (version) params.set('v', version)
  const query = params.toString()
  return `${baseUrl}/photos/${photoId}/thumbnail${query ? `?${query}` : ''}`
}

// 获取照片原图 URL（用于预览）
const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const token = userStore.token
  return `${baseUrl}/photos/${photoId}/image${token ? `?token=${token}` : ''}`
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

// 格式化 AI 提供商名称
const formatThumbnailStatus = (status?: string) => {
  const statusMap: Record<string, string> = {
    none: '未生成',
    pending: '待生成',
    ready: '已生成',
    failed: '生成失败'
  }
  return status ? (statusMap[status] || status) : '-'
}

const formatGeocodeProvider = (provider?: string) => {
  if (!provider) return '-'
  const providerMap: Record<string, string> = {
    'weibo': '微博地图',
    'offline': '离线库',
    'nominatim': 'OpenStreetMap',
    'amap': '高德地图'
  }
  return providerMap[provider] || provider
}

const formatAIProvider = (provider?: string) => {
  if (!provider) return '-'
  const providerMap: Record<string, string> = {
    'qwen': '通义千问',
    'ollama': 'Ollama',
    'openai': 'OpenAI',
    'vllm': 'vLLM',
    'hybrid': '混合模式'
  }
  return providerMap[provider] || provider
}

// 加载照片详情
const loadPhoto = async () => {
  loading.value = true
  try {
    const photoId = Number(route.params.id)
    const res = await photoApi.getById(photoId)
    photo.value = res.data?.data || null
  } catch (error: any) {
    ElMessage.error(error.message || '加载照片详情失败')
  } finally {
    loading.value = false
  }
}

// GPS 解析
const handleGeocode = async () => {
  if (!photo.value) return

  try {
    geocoding.value = true
    await geocodeApi.geocode(photo.value.id)
    await loadPhoto()
    ElMessage.success('GPS 解析完成')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || 'GPS 解析失败')
  } finally {
    geocoding.value = false
  }
}

// 生成缩略图
const handleThumbnail = async () => {
  if (!photo.value) return

  try {
    thumbnailing.value = true
    const isRegenerate = photo.value.thumbnail_status === 'ready'
    await thumbnailApi.generate(photo.value.id, isRegenerate)
    await loadPhoto()
    ElMessage.success('缩略图生成完成')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '缩略图生成失败')
  } finally {
    thumbnailing.value = false
  }
}

// AI 分析/重新分析
const handleAnalyze = async () => {
  if (!photo.value) return

  const isReanalyze = photo.value.ai_analyzed
  try {
    analyzing.value = true

    // 根据是否已分析调用不同 API
    if (isReanalyze) {
      await aiApi.reAnalyze(photo.value.id)
      ElMessage.success('重新分析请求已提交')
    } else {
      await aiApi.analyze(photo.value.id)
      ElMessage.success('分析请求已提交')
    }

    // 记录当前分析时间用于检测变化
    const lastAnalyzedAt = photo.value.analyzed_at

    // 轮询结果
    const timer = addTimer(setInterval(async () => {
      await loadPhoto()
      // 首次分析：检测 ai_analyzed 变为 true
      // 重新分析：检测 analyzed_at 时间变化
      const completed = !isReanalyze
        ? photo.value?.ai_analyzed
        : (photo.value?.analyzed_at && photo.value.analyzed_at !== lastAnalyzedAt)

      if (completed) {
        clearInterval(timer)
        analyzing.value = false
        ElMessage.success('分析完成')
      }
    }, 2000))

    // 60秒超时（重新分析可能需要更长时间）
    addTimer(setTimeout(() => {
      clearInterval(timer)
      analyzing.value = false
    }, 60000))
  } catch (error: any) {
    analyzing.value = false
    // 特殊处理 AI 服务未配置的情况
    if (error.response?.status === 503) {
      ElMessage.warning({
        message: 'AI 服务未配置或不可用，请先在配置管理中配置 AI Provider',
        duration: 5000
      })
    } else {
      ElMessage.error(error.message || '分析失败')
    }
  }
}

// 点击标签/分类跳转列表页
const handleTagClick = (tag: string) => {
  router.push({
    path: '/photos',
    query: {
      search: tag.trim(),
      page: '1'
    }
  })
}

// 返回
const goBack = () => {
  const query = route.query

  // 如果有查询参数，返回到对应状态的列表页
  if (query.page || query.analyzed || query.search) {
    router.push({
      path: '/photos',
      query: {
        ...(query.page && { page: query.page }),
        ...(query.pageSize && { pageSize: query.pageSize }),
        ...(query.analyzed && { analyzed: query.analyzed }),
        ...(query.search && { search: query.search })
      }
    })
  } else {
    // 否则使用浏览器返回
    router.back()
  }
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

.header-actions {
  display: flex;
  gap: 8px;
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

/* 可点击标签样式 */
.clickable-tag {
  cursor: pointer;
  transition: all 0.2s ease;
}

.clickable-tag:hover {
  transform: translateY(-2px);
  box-shadow: 0 4px 8px rgba(0, 0, 0, 0.15);
}
.back-link {
  color: var(--color-primary);
  font-weight: 500;
}

.preview-image {
  width: 100%;
  border-radius: 8px;
}

.analysis-descriptions {
  margin-top: 16px;
}

.score-reason {
  margin-left: 8px;
  color: #606266;
  font-style: italic;
}

.detail-section {
  margin-top: 20px;
}

.detail-text-muted {
  color: #606266;
  line-height: 1.8;
}

.detail-text-strong {
  color: #303133;
  font-weight: 500;
}

.tag-chip {
  margin-right: 8px;
  margin-top: 8px;
}

.analysis-card {
  margin-top: 8px;
}

.analysis-result-text {
  white-space: pre-wrap;
  line-height: 1.6;
}

</style>
