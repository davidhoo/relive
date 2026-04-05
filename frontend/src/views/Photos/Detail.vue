<template>
  <div class="photo-detail" v-loading="loading">
    <el-card shadow="never" v-if="photo">
      <template #header>
        <div class="header">
          <div class="header-nav">
            <el-button link @click="goBack" class="back-link">
              <el-icon><ArrowLeft /></el-icon>
              返回
            </el-button>
            <div class="photo-nav-buttons">
              <el-tooltip content="上一张 (←)" placement="top">
                <el-button
                  :icon="ArrowLeft"
                  circle
                  size="small"
                  :disabled="prevId === null"
                  @click="navigateTo(prevId)"
                />
              </el-tooltip>
              <el-tooltip content="下一张 (→)" placement="top">
                <el-button
                  :icon="ArrowRight"
                  circle
                  size="small"
                  :disabled="nextId === null"
                  @click="navigateTo(nextId)"
                />
              </el-tooltip>
            </div>
          </div>
          <div class="header-actions">
            <el-button
              v-if="photo?.status === 'excluded'"
              type="success"
              @click="handleRestore"
              :loading="statusUpdating"
            >
              <el-icon><RefreshRight /></el-icon>
              恢复照片
            </el-button>
            <el-button
              v-else
              type="danger"
              @click="handleExclude"
              :loading="statusUpdating"
            >
              <el-icon><Delete /></el-icon>
              排除照片
            </el-button>
            <el-button @click="handleThumbnail" :loading="thumbnailing">
              {{ thumbnailing ? '生成中...' : (photo?.thumbnail_status === 'ready' ? '重新生成缩略图' : '生成缩略图') }}
            </el-button>
            <el-button @click="handleGeocode" :loading="geocoding" :disabled="!photo?.gps_latitude || !photo?.gps_longitude">
              {{ geocoding ? '解析中...' : (photo?.location ? '重新解析 GPS' : '解析 GPS') }}
            </el-button>
            <el-button @click="showLocationPicker = true">
              {{ photo?.gps_latitude && photo?.gps_longitude ? '修改位置' : '设置位置' }}
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

      <el-alert
        v-if="photo.status === 'excluded'"
        title="该照片已被排除，不参与展示、分析和统计"
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 16px"
      />

      <el-row :gutter="20">
        <!-- 左侧：照片预览 -->
        <el-col :span="12">
          <el-image
            :key="imageVersion"
            :src="getPhotoThumbnailUrl(photo.id, String(imageVersion))"
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
            <el-descriptions-item label="方向">
              <div class="orientation-cell">
                <span>{{ photo.manual_rotation ? photo.manual_rotation + '°' : '0°' }}</span>
                <el-button-group size="small" class="orientation-actions">
                  <el-button :loading="orientationUpdating" @click="handleRotate('left')" title="逆时针旋转 90°">
                    <el-icon><RefreshLeft /></el-icon>
                  </el-button>
                  <el-button :loading="orientationUpdating" @click="handleRotate('right')" title="顺时针旋转 90°">
                    <el-icon><RefreshRight /></el-icon>
                  </el-button>
                </el-button-group>
              </div>
            </el-descriptions-item>
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

          <el-divider />
          <div class="people-detail-section">
            <div class="people-section-header">
              <div>
                <h3>人物信息</h3>
                <p class="people-section-subtitle">展示这张照片中出现的人物和对应的人脸样本，便于检查聚类结果。</p>
              </div>
              <el-tag effect="light" :type="photoPeopleGroups.length > 0 ? 'danger' : 'info'">
                {{ photoPeopleSummaryLabel }}
              </el-tag>
            </div>

            <el-skeleton v-if="photoPeopleLoading" animated :rows="4" />

            <template v-else>
              <el-alert
                v-if="photoPeopleStatus === 'pending' || photoPeopleStatus === 'processing'"
                type="info"
                :closable="false"
                show-icon
                :title="photoPeopleStatus === 'pending' ? '人物队列待处理' : '人物识别处理中'"
                description="人物后台任务会在扫描 / 重建后自动推进，识别完成后这里会展示分组结果。"
                class="people-status-alert"
              />

              <el-alert
                v-else-if="photoPeopleStatus === 'failed'"
                type="warning"
                :closable="false"
                show-icon
                title="人物识别失败"
                description="可以先检查人物后台任务日志，必要时重新触发扫描或后续修复。"
                class="people-status-alert"
              />

              <div v-if="photoPeopleGroups.length === 0" class="people-empty-compact">
                {{ photoPeopleEmptyText }}
              </div>

              <div v-else class="photo-people-groups">
                <div v-for="group in photoPeopleGroups" :key="group.category" class="photo-people-group">
                  <div class="photo-people-group-header">
                    <h4>{{ group.label }}</h4>
                    <span class="photo-people-group-meta">
                      {{ `${group.people.length} 人 · ${group.face_count} 张人脸` }}
                    </span>
                  </div>

                  <div class="photo-people-person-grid">
                    <router-link
                      v-for="personItem in group.people"
                      :key="personItem.id"
                      :to="`/people/${personItem.id}`"
                      class="photo-person-card"
                    >
                      <div class="photo-person-main">
                        <el-avatar
                          :size="44"
                          :src="personItem.representative_face_id ? getFaceThumbnailUrl(personItem.representative_face_id, String(imageVersion)) : ''"
                        >
                          {{ getPersonAvatarFallback(personItem) }}
                        </el-avatar>
                        <div class="photo-person-copy">
                          <div class="photo-person-name">{{ getPhotoPersonName(personItem) }}</div>
                          <div class="photo-person-meta">
                            {{ `${getPersonCategoryLabel(personItem.category)} · ${personItem.faces?.length || 0} 张样本` }}
                          </div>
                        </div>
                      </div>

                      <div class="photo-person-face-strip">
                        <img
                          v-for="face in (personItem.faces || []).slice(0, 4)"
                          :key="face.id"
                          :src="getFaceThumbnailUrl(face.id, String(imageVersion))"
                          :alt="`face-${face.id}`"
                          class="photo-person-face"
                        />
                      </div>
                    </router-link>
                  </div>
                </div>
              </div>
            </template>
          </div>

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
            <div class="detail-section">
              <h4>分类</h4>
              <div class="category-edit-container">
                <template v-if="!categoryEditing">
                  <template v-if="photo.main_category">
                    <el-tag
                      type="primary"
                      size="large"
                      class="clickable-tag"
                      @click="handleCategoryClick(photo.main_category!)"
                    >
                      {{ photo.main_category }}
                    </el-tag>
                    <el-icon class="edit-icon-btn" @click="startCategoryEdit"><Edit /></el-icon>
                  </template>
                  <el-button
                    v-else
                    link
                    type="primary"
                    size="small"
                    @click="startCategoryEdit"
                  >
                    + 添加分类
                  </el-button>
                </template>
                <template v-else>
                  <el-select
                    v-model="categoryValue"
                    filterable
                    placeholder="请选择分类"
                    size="default"
                    style="width: 200px"
                    :loading="categoriesLoading"
                    @change="handleCategoryChange"
                    @visible-change="(visible: boolean) => { if (!visible && categoryEditing) cancelCategoryEdit() }"
                    ref="categorySelectRef"
                  >
                    <el-option
                      v-for="cat in availableCategories"
                      :key="cat"
                      :label="cat"
                      :value="cat"
                    />
                  </el-select>
                </template>
              </div>
            </div>

            <!-- 标签 -->
            <div class="detail-section" v-if="photo.tags && photo.tags.length > 0">
              <h4>标签</h4>
              <el-tag
                v-for="tag in photo.tags"
                :key="tag"
                class="clickable-tag tag-chip"
                @click="handleTagClick(tag)"
              >
                {{ tag }}
              </el-tag>
            </div>

          </div>
          <el-empty v-else description="照片尚未分析" />
        </el-col>
      </el-row>
    </el-card>

    <!-- 位置选择器 -->
    <LocationPicker
      v-model:visible="showLocationPicker"
      :initial-lat="photo?.gps_latitude"
      :initial-lng="photo?.gps_longitude"
      @confirm="handleLocationConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, nextTick, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ArrowRight, InfoFilled, Delete, RefreshRight, RefreshLeft, Edit } from '@element-plus/icons-vue'
import { photoApi } from '@/api/photo'
import { aiApi } from '@/api/ai'
import { geocodeApi } from '@/api/geocode'
import { thumbnailApi } from '@/api/thumbnail'
import { peopleApi } from '@/api/people'
import type { Photo } from '@/types/photo'
import type { PhotoPeopleResponse, Person } from '@/types/people'
import LocationPicker from '@/components/LocationPicker.vue'
import dayjs from 'dayjs'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getPersonAvatarFallback, getPersonCategoryLabel } from '@/views/People/peopleHelpers'
import { buildFaceThumbnailUrl, getPhotoPeopleSummaryLabel, groupPhotoPeopleByCategory } from './photoPeopleHelpers'

const route = useRoute()
const router = useRouter()

const photo = ref<Photo | null>(null)
const loading = ref(false)
const analyzing = ref(false)
const geocoding = ref(false)
const thumbnailing = ref(false)
const statusUpdating = ref(false)
const orientationUpdating = ref(false)
const imageVersion = ref(Date.now())
const showLocationPicker = ref(false)
const photoPeople = ref<PhotoPeopleResponse | null>(null)
const photoPeopleLoading = ref(false)

// 上一张/下一张导航
const prevId = ref<number | null>(null)
const nextId = ref<number | null>(null)
const navLoading = ref(false)

// 分类编辑状态
const categoryEditing = ref(false)
const categoryValue = ref('')
const availableCategories = ref<string[]>([])
const categoriesLoading = ref(false)
const categorySelectRef = ref<any>(null)

const buildPhotoPeopleFallback = (): Pick<PhotoPeopleResponse, 'face_process_status' | 'face_count' | 'top_person_category'> | null => {
  if (!photo.value) return null
  return {
    face_process_status: (photo.value.face_process_status as PhotoPeopleResponse['face_process_status']) || 'none',
    face_count: photo.value.face_count || 0,
    top_person_category: (photo.value.top_person_category as PhotoPeopleResponse['top_person_category']) || '',
  }
}

const photoPeopleStatus = computed(() => photoPeople.value?.face_process_status || buildPhotoPeopleFallback()?.face_process_status || 'none')
const photoPeopleGroups = computed(() => groupPhotoPeopleByCategory(photoPeople.value))
const photoPeopleSummaryLabel = computed(() => getPhotoPeopleSummaryLabel(photoPeople.value || buildPhotoPeopleFallback()))
const photoPeopleEmptyText = computed(() => {
  switch (photoPeopleStatus.value) {
    case 'none':
      return '尚未生成人物结果'
    case 'no_face':
      return '未检测到人脸'
    case 'failed':
      return '人物识别失败'
    default:
      return photoPeopleSummaryLabel.value
  }
})

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

// 获取照片缩略图 URL
const getPhotoThumbnailUrl = (photoId: number, version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const params = new URLSearchParams()
  if (version) params.set('v', version)
  const query = params.toString()
  return `${baseUrl}/photos/${photoId}/thumbnail${query ? `?${query}` : ''}`
}

// 获取照片原图 URL（用于预览）
const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return `${baseUrl}/photos/${photoId}/image`
}

const getFaceThumbnailUrl = (faceId: number, version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return buildFaceThumbnailUrl(faceId, baseUrl, version)
}

const getPhotoPersonName = (personItem: Person) => personItem.name?.trim() || `未命名人物 #${personItem.id}`

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
    await loadPhotoPeople(photo.value?.id)
  } catch (error: any) {
    ElMessage.error(error.message || '加载照片详情失败')
  } finally {
    loading.value = false
  }
}

const loadPhotoPeople = async (photoId?: number) => {
  if (!photoId) {
    photoPeople.value = null
    return
  }

  photoPeopleLoading.value = true
  try {
    const res = await peopleApi.getPhotoPeople(photoId)
    photoPeople.value = res.data?.data || null
  } catch (error) {
    photoPeople.value = null
    console.error('Failed to load photo people:', error)
  } finally {
    photoPeopleLoading.value = false
  }
}

// 加载相邻照片 ID
const loadAdjacent = async () => {
  const photoId = Number(route.params.id)
  const query = route.query
  const params: Record<string, any> = {}
  if (query.analyzed) params.analyzed = query.analyzed
  if (query.has_thumbnail) params.has_thumbnail = query.has_thumbnail
  if (query.has_gps) params.has_gps = query.has_gps
  if (query.status) params.status = query.status
  if (query.search) params.search = query.search
  if (query.category) params.category = query.category
  if (query.tag) params.tag = query.tag
  if (query.sort_by) params.sort_by = query.sort_by
  if (query.sort_desc) params.sort_desc = query.sort_desc
  try {
    const res = await photoApi.getAdjacent(photoId, params)
    const data = res.data?.data
    prevId.value = data?.prev_id ?? null
    nextId.value = data?.next_id ?? null
  } catch {
    prevId.value = null
    nextId.value = null
  }
}

// 导航到相邻照片
const navigateTo = (id: number | null) => {
  if (!id || navLoading.value) return
  navLoading.value = true
  router.replace({ path: `/photos/${id}`, query: route.query })
}

// 键盘导航
const handleKeydown = (e: KeyboardEvent) => {
  // 忽略输入框内的按键
  if ((e.target as HTMLElement)?.tagName === 'INPUT' || (e.target as HTMLElement)?.tagName === 'TEXTAREA') return
  if (e.key === 'ArrowLeft') {
    e.preventDefault()
    navigateTo(prevId.value)
  } else if (e.key === 'ArrowRight') {
    e.preventDefault()
    navigateTo(nextId.value)
  }
}

// 监听路由参数变化（同一组件内切换照片）
watch(() => route.params.id, async (newId) => {
  if (newId) {
    await loadPhoto()
    loadAdjacent()
    imageVersion.value = Date.now()
    navLoading.value = false
  }
})

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

// 手动设置位置确认回调
const handleLocationConfirm = async (coords: { latitude: number; longitude: number }) => {
  if (!photo.value) return
  try {
    await photoApi.setLocation(photo.value.id, coords)
    await loadPhoto()
    ElMessage.success('位置已更新')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '设置位置失败')
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

// 开始编辑分类
const startCategoryEdit = async () => {
  categoryValue.value = photo.value?.main_category || ''
  categoryEditing.value = true
  categoriesLoading.value = true
  try {
    const res = await photoApi.getCategories()
    availableCategories.value = res.data?.data || []
  } catch {
    availableCategories.value = []
  } finally {
    categoriesLoading.value = false
  }
  await nextTick()
  categorySelectRef.value?.focus()
  categorySelectRef.value?.$el?.querySelector('input')?.click()
}

// 取消编辑分类
const cancelCategoryEdit = () => {
  categoryEditing.value = false
  categoryValue.value = ''
}

// 分类选择改变时保存
const handleCategoryChange = async (value: string) => {
  if (!photo.value) return
  try {
    await photoApi.updateCategory(photo.value.id, value || '')
    ElMessage.success('分类已更新')
    categoryEditing.value = false
    await loadPhoto()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '更新分类失败')
  }
}

// 点击标签/分类跳转列表页
const handleCategoryClick = (category: string) => {
  router.push({
    path: '/photos',
    query: {
      category: category.trim(),
      page: '1'
    }
  })
}

const handleTagClick = (tag: string) => {
  router.push({
    path: '/photos',
    query: {
      tag: tag.trim(),
      page: '1'
    }
  })
}

// 排除照片

// 手动旋转
const handleRotate = async (direction: 'left' | 'right') => {
  if (!photo.value) return
  const current = photo.value.manual_rotation || 0
  const newRotation = direction === 'right'
    ? (current + 90) % 360
    : (current + 270) % 360
  orientationUpdating.value = true
  try {
    const { data: res } = await photoApi.updateRotation(photo.value.id, newRotation)
    if (res.success) {
      ElMessage.success('旋转已更新')
      await loadPhoto()
      imageVersion.value = Date.now()
    } else {
      ElMessage.error(res.error?.message || '更新失败')
    }
  } catch {
    ElMessage.error('更新旋转失败')
  } finally {
    orientationUpdating.value = false
  }
}

const handleExclude = async () => {
  if (!photo.value) return
  try {
    await ElMessageBox.confirm(
      '排除后该照片将不参与展示、分析和统计，重新扫描也不会恢复。确定排除？',
      '排除照片',
      { confirmButtonText: '排除', cancelButtonText: '取消', type: 'warning' }
    )
  } catch {
    return
  }
  try {
    statusUpdating.value = true
    await photoApi.batchUpdateStatus({ photo_ids: [photo.value.id], status: 'excluded' })
    ElMessage.success('照片已排除')
    await loadPhoto()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || '排除失败')
  } finally {
    statusUpdating.value = false
  }
}

// 恢复照片
const handleRestore = async () => {
  if (!photo.value) return
  try {
    statusUpdating.value = true
    await photoApi.batchUpdateStatus({ photo_ids: [photo.value.id], status: 'active' })
    ElMessage.success('照片已恢复')
    await loadPhoto()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || '恢复失败')
  } finally {
    statusUpdating.value = false
  }
}

// 返回
const goBack = () => {
  const query = route.query

  // 如果有查询参数，返回到对应状态的列表页
  if (query.page || query.analyzed || query.search || query.has_thumbnail || query.has_gps || query.status || query.category || query.tag) {
    router.push({
      path: '/photos',
      query: {
        ...(query.page && { page: query.page }),
        ...(query.pageSize && { pageSize: query.pageSize }),
        ...(query.analyzed && { analyzed: query.analyzed }),
        ...(query.has_thumbnail && { has_thumbnail: query.has_thumbnail }),
        ...(query.has_gps && { has_gps: query.has_gps }),
        ...(query.status && { status: query.status }),
        ...(query.search && { search: query.search }),
        ...(query.category && { category: query.category }),
        ...(query.tag && { tag: query.tag })
      }
    })
  } else {
    // 否则使用浏览器返回
    router.back()
  }
}

onMounted(() => {
  loadPhoto()
  loadAdjacent()
  document.addEventListener('keydown', handleKeydown)
})

onBeforeUnmount(() => {
  clearAllTimers()
  document.removeEventListener('keydown', handleKeydown)
})</script>

<style scoped>
.photo-detail {
  padding: 20px;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-nav {
  display: flex;
  align-items: center;
  gap: 12px;
}

.photo-nav-buttons {
  display: flex;
  gap: 4px;
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

.category-edit-container {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 4px;
}

.edit-icon-btn {
  font-size: 14px;
  color: #909399;
  cursor: pointer;
  transition: color 0.2s;
}

.edit-icon-btn:hover {
  color: var(--el-color-primary);
}

.orientation-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.orientation-actions {
  margin-left: auto;
}

.people-detail-section {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.people-section-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}

.people-section-subtitle {
  margin: 6px 0 0;
  color: #606266;
  line-height: 1.7;
}

.people-status-alert {
  margin-top: 4px;
}

.people-empty-compact {
  padding: 12px 0;
  color: #909399;
  font-size: 13px;
}

.photo-people-groups {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.photo-people-group {
  padding: 16px;
  border-radius: 14px;
  background: #f8fafc;
  border: 1px solid #e5e7eb;
}

.photo-people-group-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: baseline;
  margin-bottom: 12px;
}

.photo-people-group-meta {
  color: #6b7280;
  font-size: 13px;
}

.photo-people-person-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.photo-person-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px;
  border-radius: 12px;
  background: #fff;
  border: 1px solid #e5e7eb;
  text-decoration: none;
}

.photo-person-card:hover {
  border-color: var(--el-color-primary);
}

.photo-person-main {
  display: flex;
  gap: 12px;
  align-items: center;
}

.photo-person-copy {
  min-width: 0;
}

.photo-person-name {
  font-weight: 600;
  color: #303133;
}

.photo-person-meta {
  margin-top: 4px;
  color: #6b7280;
  font-size: 13px;
}

.photo-person-face-strip {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.photo-person-face {
  width: 54px;
  height: 54px;
  object-fit: cover;
  border-radius: 10px;
  background: #eef2f7;
}

@media (max-width: 768px) {
  .people-section-header,
  .photo-people-group-header {
    flex-direction: column;
  }

  .photo-people-person-grid {
    grid-template-columns: 1fr;
  }
}

</style>
