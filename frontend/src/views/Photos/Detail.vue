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
            <el-button @click="handleFaceDetection" :loading="faceDetecting">
              <el-icon><View /></el-icon>
              {{ faceDetecting ? '识别中...' : (photoPeopleStatus === 'ready' || photoPeopleStatus === 'no_face' ? '重新识别人脸' : '识别人脸') }}
            </el-button>
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
                <p class="people-section-subtitle">展示这张照片中检测到的人脸及其归属人物，可直接对某张人脸改名。</p>
              </div>
              <el-tag effect="light" :type="photoFaceEntries.length > 0 ? 'danger' : 'info'">
                {{ photoPeopleCountTag }}
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

             <div v-if="photoFaceEntries.length > 0" class="photo-face-list">
               <div v-for="entry in photoFaceEntries" :key="entry.faceId" class="photo-face-item">
                 <router-link :to="`/people/${entry.person.id}`" class="photo-face-main">
                   <img
                     :src="getFaceThumbnailUrl(entry.faceId, String(imageVersion))"
                     :alt="`face-${entry.faceId}`"
                     class="photo-face-thumb"
                   />
                   <div class="photo-face-copy">
                     <div class="photo-face-name">{{ getPhotoPersonName(entry.person) }}</div>
                     <div class="photo-face-meta">
                       <span class="photo-face-category" :class="`is-${entry.person.category || 'stranger'}`">
                         {{ getPersonCategoryLabel(entry.person.category) }}
                       </span>
                     </div>
                   </div>
                 </router-link>
                  <el-button
                    link
                    size="small"
                    class="photo-face-edit"
                    @click="openFaceAssignDialog(entry)"
                  >
                    <el-icon><Edit /></el-icon>
                  </el-button>
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

    <!-- 编辑人脸人物：对本照片中的单张人脸改名/改归属 -->
    <el-dialog
      v-model="faceAssignVisible"
      title="编辑人脸人物"
      width="420px"
      :close-on-click-modal="false"
      append-to-body
    >
      <div v-if="faceAssignTarget" class="face-assign-body">
        <div class="face-assign-preview">
          <img
            :src="getFaceThumbnailUrl(faceAssignTarget.faceId, String(imageVersion))"
            alt="当前人脸"
            class="face-assign-thumb"
          />
          <div class="face-assign-current">
            <div class="face-assign-current-label">当前归属</div>
            <div class="face-assign-current-name">{{ getPhotoPersonName(faceAssignTarget.person) }}</div>
            <span class="photo-face-category" :class="`is-${faceAssignTarget.person.category || 'stranger'}`">
              {{ getPersonCategoryLabel(faceAssignTarget.person.category) }}
            </span>
          </div>
        </div>

        <el-form label-position="top" class="face-assign-form">
          <el-form-item label="人物姓名">
            <el-input
              v-model="faceAssignName"
              placeholder="输入人物姓名，留空则保持未命名"
              clearable
              :maxlength="50"
              show-word-limit
            />
          </el-form-item>

          <el-form-item label="人物分类">
            <el-select v-model="faceAssignCategory" placeholder="选择类别" style="width: 100%">
              <el-option
                v-for="option in faceCategoryOptions"
                :key="option.value"
                :label="option.label"
                :value="option.value"
              />
            </el-select>
          </el-form-item>
        </el-form>

        <!-- 姓名搜索：命中已有人物提示移动语义；未命中提示拆分创建 -->
        <div class="face-assign-hint">
          <template v-if="faceAssignSearchLoading">搜索中…</template>
          <template v-else-if="faceAssignMatchedPerson">
            <el-icon class="face-assign-hint-icon"><InfoFilled /></el-icon>
            <span>
              命中已有人物「{{ faceAssignMatchedName }}」（{{ getPersonCategoryLabel(faceAssignMatchedPerson.category) }}），
              保存将把这张人脸移动到该人物，分类使用目标人物分类。
            </span>
          </template>
          <template v-else-if="faceAssignName.trim()">
            <el-icon class="face-assign-hint-icon"><InfoFilled /></el-icon>
            <span>未找到同名人物，保存将从当前人物拆分这张人脸，创建新人物并命名，分类使用上方选择。</span>
          </template>
          <template v-else>
            <span class="face-assign-hint-muted">输入姓名后可搜索已有人物；命中则移动，未命中则拆分创建。</span>
          </template>
        </div>
      </div>

      <template #footer>
        <div class="face-assign-footer">
          <el-button
            type="warning"
            plain
            :loading="faceAssignSplitting"
            :disabled="faceAssignSaving"
            @click="handleFaceAssignSplit"
          >拆分</el-button>
          <div class="face-assign-footer-right">
            <el-button @click="faceAssignVisible = false">取消</el-button>
            <el-button type="primary" :loading="faceAssignSaving" :disabled="faceAssignSplitting" @click="handleFaceAssignSave">保存</el-button>
          </div>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, nextTick, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ArrowRight, InfoFilled, Delete, RefreshRight, RefreshLeft, Edit, View } from '@element-plus/icons-vue'
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
import { buildFaceThumbnailUrl, flattenPhotoPeopleFaces, getPhotoPeopleCountTag } from './photoPeopleHelpers'

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
const faceDetecting = ref(false)

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
const photoFaceEntries = computed(() => flattenPhotoPeopleFaces(photoPeople.value))
const photoPeopleCountTag = computed(() => getPhotoPeopleCountTag(photoPeople.value))
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

// ---- 编辑人脸人物对话框（本照片单张 face 改名/改归属）----
const faceCategoryOptions: Array<{ label: string; value: Person['category'] }> = [
  { label: '家人', value: 'family' },
  { label: '亲友', value: 'friend' },
  { label: '熟人', value: 'acquaintance' },
  { label: '路人', value: 'stranger' },
]

const faceAssignVisible = ref(false)
const faceAssignSaving = ref(false)
const faceAssignSplitting = ref(false)
const faceAssignTarget = ref<{ faceId: number; person: Person } | null>(null)
const faceAssignName = ref('')
const faceAssignCategory = ref<Person['category']>('stranger')
// 姓名搜索命中已有人物（同名），用于提示移动语义
const faceAssignMatchedPerson = ref<Person | null>(null)
const faceAssignSearchLoading = ref(false)
const faceAssignMatchedName = computed(() =>
  faceAssignMatchedPerson.value
    ? (faceAssignMatchedPerson.value.name?.trim() || `未命名人物 #${faceAssignMatchedPerson.value.id}`)
    : '',
)
let faceAssignSearchTimer: number | null = null
let faceAssignSearchSeq = 0

const openFaceAssignDialog = (entry: { faceId: number; person: Person }) => {
  faceAssignTarget.value = entry
  faceAssignName.value = entry.person.name?.trim() ?? ''
  faceAssignCategory.value = entry.person.category || 'stranger'
  faceAssignMatchedPerson.value = null
  faceAssignSearchLoading.value = false
  faceAssignSearchSeq++
  faceAssignVisible.value = true
}

// 姓名变化：去抖 300ms 搜索同名已有人物（排除当前归属人物自身）
watch(faceAssignName, value => {
  if (faceAssignSearchTimer !== null) {
    window.clearTimeout(faceAssignSearchTimer)
  }
  const trimmed = value.trim()
  if (!trimmed || !faceAssignTarget.value) {
    faceAssignMatchedPerson.value = null
    faceAssignSearchLoading.value = false
    return
  }
  faceAssignSearchLoading.value = true
  const currentPersonId = faceAssignTarget.value.person.id
  const mySeq = ++faceAssignSearchSeq
  faceAssignSearchTimer = window.setTimeout(async () => {
    try {
      const res = await peopleApi.getList({ search: trimmed, page: 1, page_size: 20 })
      if (mySeq !== faceAssignSearchSeq) return
      const items = (res.data?.data?.items || []).filter(
        p => p.id !== currentPersonId && p.name?.trim() === trimmed,
      )
      faceAssignMatchedPerson.value = items.length > 0 ? items[0] ?? null : null
    } catch {
      if (mySeq !== faceAssignSearchSeq) return
      faceAssignMatchedPerson.value = null
    } finally {
      if (mySeq === faceAssignSearchSeq) {
        faceAssignSearchLoading.value = false
      }
    }
  }, 300)
})

const handleFaceAssignSave = async () => {
  if (!faceAssignTarget.value || !photo.value) return
  // 双击防重复：保存/拆分任一进行中时直接 return。
  if (faceAssignSaving.value || faceAssignSplitting.value) return
  const target = faceAssignTarget.value
  const trimmedName = faceAssignName.value.trim()
  // 命中已有人物时走移动；否则若姓名为空且未命中，也按“移动到自身”无意义，要求姓名
  if (!faceAssignMatchedPerson.value && !trimmedName) {
    ElMessage.warning('请输入人物姓名，或选择已有人物')
    return
  }

  faceAssignSaving.value = true
  try {
    const res = await peopleApi.assignFacePerson(target.faceId, {
      name: trimmedName,
      category: faceAssignCategory.value,
      target_person_id: faceAssignMatchedPerson.value?.id,
    })
    const updated = res.data?.data
    if (updated) {
      photoPeople.value = updated
      // imageVersion bump 让缩略图刷新（拆分/移动后人脸缩略图本身不变，但稳妥起见）
    } else {
      await loadPhotoPeople(photo.value.id)
    }
    imageVersion.value = Date.now()
    ElMessage.success('人脸归属已更新')
    faceAssignVisible.value = false
  } catch (error: any) {
    if (!error?.response || [502, 503, 504].includes(error.response.status)) {
      ElMessage.warning('操作可能仍在后台处理中，请稍后刷新页面查看结果')
    } else {
      ElMessage.error(error.response?.data?.error?.message || error.message || '保存失败')
    }
  } finally {
    faceAssignSaving.value = false
  }
}

// 拆分：把当前 face 从所属人物直接拆分出去，创建新人物（不命名）
const handleFaceAssignSplit = async () => {
  if (!faceAssignTarget.value || !photo.value) return
  // 双击防重复：保存/拆分任一进行中时直接 return。
  if (faceAssignSaving.value || faceAssignSplitting.value) return
  const target = faceAssignTarget.value

  faceAssignSplitting.value = true
  try {
    const res = await peopleApi.split([target.faceId])
    const data = res.data?.data as any
    const evaluated = data?.recluster_evaluated || 0
    const reassigned = data?.recluster_reassigned || 0
    let msg = '已拆分为新人物'
    if (reassigned > 0) {
      msg += `（已重新评估 ${evaluated} 张不确定人脸，${reassigned} 张已重新分配）`
    } else if (evaluated > 0) {
      msg += `（已重新评估 ${evaluated} 张不确定人脸，无需调整）`
    } else {
      msg += '，后台将继续重新评估不确定人脸'
    }
    ElMessage.success(msg)
    faceAssignVisible.value = false
    // split 接口不返回 photo people 数据，需重新加载
    await loadPhotoPeople(photo.value.id)
    imageVersion.value = Date.now()
  } catch (error: any) {
    if (!error?.response || [502, 503, 504].includes(error.response.status)) {
      ElMessage.warning('操作可能仍在后台处理中，请稍后刷新页面查看结果')
    } else {
      ElMessage.error(error.response?.data?.error?.message || error.message || '拆分失败')
    }
  } finally {
    faceAssignSplitting.value = false
  }
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
    'openai': 'OpenAI (Compatible)',
    'openai_responses': 'OpenAI (Responses)',
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

// 人脸识别
const handleFaceDetection = async () => {
  if (!photo.value) return

  const status = photoPeopleStatus.value
  const force = status === 'ready' || status === 'no_face'
  const label = force ? '重新识别将覆盖现有人脸数据，确定？' : '确定要对该照片进行人脸识别？'
  try {
    await ElMessageBox.confirm(label, force ? '重新识别人脸' : '人脸识别', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: force ? 'warning' : 'info',
    })
  } catch {
    return
  }

  try {
    faceDetecting.value = true
    await peopleApi.enqueueFaceDetection(photo.value.id, force)
    ElMessage.success('人脸识别任务已入队')

    const lastStatus = status
    const timer = addTimer(setInterval(async () => {
      await loadPhotoPeople(photo.value?.id)
      const current = photoPeopleStatus.value
      if (current !== lastStatus && (current === 'ready' || current === 'no_face' || current === 'failed')) {
        clearInterval(timer)
        faceDetecting.value = false
        if (current === 'ready') {
          ElMessage.success('人脸识别完成')
        } else if (current === 'no_face') {
          ElMessage.info('未检测到人脸')
        } else {
          ElMessage.warning('人脸识别失败')
        }
        await loadPhoto()
      }
    }, 2000))

    addTimer(setTimeout(() => {
      clearInterval(timer)
      faceDetecting.value = false
    }, 120000))
  } catch (error: any) {
    faceDetecting.value = false
    ElMessage.error(error.response?.data?.error?.message || error.message || '人脸识别入队失败')
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

// AI 分析/重新分析（后端异步入队，POST 立即返回；前端轮询直到完成）
const handleAnalyze = async () => {
  if (!photo.value) return

  const isReanalyze = photo.value.ai_analyzed
  // 记录当前分析时间，用于重新分析时检测 analyzed_at 变化
  const lastAnalyzedAt = photo.value.analyzed_at
  try {
    analyzing.value = true

    // 后端异步启动分析，POST 立即返回；AI 分析在后台运行（耗时数分钟），
    // 避免同步长连接被反向代理/网关 60s 超时切断（504）
    if (isReanalyze) {
      await aiApi.reAnalyze(photo.value.id)
    } else {
      await aiApi.analyze(photo.value.id)
    }

    // 轮询检测分析完成：首次分析看 ai_analyzed 变 true；重新分析看 analyzed_at 变化
    const POLL_INTERVAL = 2000
    const POLL_TIMEOUT = 300000 // 5 分钟（后端单会话上限 120s × 2 + 余量）
    const startedAt = Date.now()
    const timer = addTimer(setInterval(async () => {
      // 轻量轮询：只取照片字段，避免每次都拉人物信息
      try {
        const res = await photoApi.getById(photo.value!.id)
        const p = res.data?.data
        if (p) photo.value = p
      } catch {
        return
      }
      const completed = !isReanalyze
        ? photo.value?.ai_analyzed
        : (photo.value?.analyzed_at && photo.value.analyzed_at !== lastAnalyzedAt)
      if (completed) {
        clearInterval(timer)
        analyzing.value = false
        // 完成后完整刷新（含人物信息）
        await loadPhoto()
        ElMessage.success(isReanalyze ? '重新分析完成' : '分析完成')
      } else if (Date.now() - startedAt > POLL_TIMEOUT) {
        clearInterval(timer)
        analyzing.value = false
        ElMessage.warning('分析仍在进行，请稍后返回查看结果')
      }
    }, POLL_INTERVAL))
    // timer 已通过 addTimer 注册，组件卸载时由 clearAllTimers 统一清理
  } catch (error: any) {
    analyzing.value = false
    // 特殊处理 AI 服务未配置的情况
    if (error.response?.status === 503) {
      ElMessage.warning({
        message: 'AI 服务未配置或不可用，请先在配置管理中配置 AI Provider',
        duration: 5000
      })
    } else {
      ElMessage.error(error.response?.data?.error?.message || error.message || '分析失败')
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

.photo-people-groups {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

/* 人脸级紧凑列表 */
.photo-face-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.photo-face-item {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px 6px 6px;
  border-radius: 10px;
  background: #fff;
  border: 1px solid #e5e7eb;
}

.photo-face-item:hover {
  border-color: var(--el-color-primary);
}

.photo-face-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  text-decoration: none;
}

.photo-face-thumb {
  width: 36px;
  height: 36px;
  object-fit: cover;
  border-radius: 8px;
  background: #eef2f7;
  flex-shrink: 0;
}

.photo-face-copy {
  min-width: 0;
}

.photo-face-name {
  font-weight: 600;
  font-size: 13px;
  color: #303133;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.photo-face-meta {
  margin-top: 2px;
  display: flex;
  align-items: center;
  gap: 8px;
}

.photo-face-category {
  font-size: 10px;
  font-weight: 600;
  padding: 0 6px;
  border-radius: 999px;
  white-space: nowrap;
}

.photo-face-category.is-family {
  background: rgba(245, 108, 108, 0.12);
  color: #c45656;
}

.photo-face-category.is-friend {
  background: rgba(103, 194, 58, 0.14);
  color: #5a9a3a;
}

.photo-face-category.is-acquaintance {
  background: rgba(230, 162, 60, 0.14);
  color: #b8821f;
}

.photo-face-category.is-stranger {
  background: rgba(144, 147, 153, 0.14);
  color: #8a8d93;
}

.photo-face-edit {
  flex-shrink: 0;
  padding: 2px;
  color: #9ca3af;
}

/* 编辑人脸人物对话框 */
.face-assign-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.face-assign-preview {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border-radius: 10px;
  background: #f8fafc;
  border: 1px solid #e5e7eb;
}

.face-assign-thumb {
  width: 56px;
  height: 56px;
  object-fit: cover;
  border-radius: 10px;
  background: #eef2f7;
  flex-shrink: 0;
}

.face-assign-current {
  min-width: 0;
}

.face-assign-current-label {
  font-size: 12px;
  color: #6b7280;
}

.face-assign-current-name {
  font-weight: 600;
  color: #303133;
  margin: 2px 0 4px;
}

.face-assign-form {
  margin-top: 0;
}

.face-assign-hint {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  font-size: 13px;
  color: #6b7280;
  line-height: 1.6;
}

.face-assign-hint-icon {
  color: var(--el-color-primary);
  margin-top: 2px;
  flex-shrink: 0;
}

.face-assign-hint-muted {
  color: #9ca3af;
}

.face-assign-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}

.face-assign-footer-right {
  display: flex;
  gap: 8px;
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
