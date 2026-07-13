<template>
  <div class="people-detail-page" v-loading="loading">
    <template v-if="person">
      <!-- 顶部紧凑操作台 -->
      <el-card shadow="never" class="section-card console-card">
        <template #header>
          <SectionHeader :icon="User" :title="`人物详情（#${person.id}）`">
            <template #actions>
              <el-button size="small" @click="goBack">返回列表</el-button>
              <el-button size="small" type="primary" @click="loadData">刷新</el-button>
            </template>
          </SectionHeader>
        </template>

        <div class="console-layout">
          <!-- 左侧：人物身份摘要 -->
          <div class="console-info">
            <div class="summary-avatar" @click="openEditDialog">
              <el-avatar :size="64" :src="avatarUrl" class="summary-avatar-img">
                {{ getPersonAvatarFallback(person) }}
              </el-avatar>
              <div class="summary-avatar-text">
                <div class="summary-name-row">
                  <span class="summary-name">{{ personTitle }}</span>
                  <span class="summary-category-chip" :class="`is-${person.category}`">{{ getPersonCategoryLabel(person.category) }}</span>
                  <el-button size="small" plain class="summary-edit-btn" @click.stop="openEditDialog">编辑</el-button>
                </div>
                <div class="summary-meta">
                  <span>人物 #{{ person.id }}</span>
                  <span>{{ person.face_count }} 张人脸</span>
                  <span>{{ person.photo_count }} 张照片</span>
                  <span v-if="person.representative_face_id">头像 Face #{{ person.representative_face_id }}</span>
                  <span v-else>未设置头像</span>
                  <el-tooltip v-if="person.created_at || person.updated_at" placement="top">
                    <template #content>
                      <div v-if="person.created_at">创建：{{ formatTime(person.created_at) }}</div>
                      <div v-if="person.updated_at">更新：{{ formatTime(person.updated_at) }}</div>
                    </template>
                    <span class="summary-meta-time">时间</span>
                  </el-tooltip>
                </div>
              </div>
            </div>
          </div>

          <!-- 右侧：纠错操作工具条 -->
          <div class="console-ops">
            <div class="ops-toolbar">
              <div class="ops-toolbar-head">
                <span class="ops-toolbar-title">纠错操作</span>
                <el-tag size="small" effect="plain" :type="selectedFaceCount > 0 ? 'warning' : 'info'">
                  已选 {{ selectedFaceCount }} 张
                </el-tag>
                <el-button v-if="selectedFaceCount > 0" size="small" text type="info" @click="clearSelection">清空选择</el-button>
              </div>
              <div class="ops-toolbar-actions">
                <el-tooltip content="把当前选中的人脸拆成一个新人物" placement="top">
                  <el-button size="small" type="warning" plain :disabled="selectedFaceIds.length === 0 || foregroundBusy" :loading="splitting" @click="splitSelectedFaces">
                    拆分
                  </el-button>
                </el-tooltip>
                <el-tooltip content="把选中的人脸移动到已有人物" placement="top">
                  <el-button size="small" plain :disabled="selectedFaceIds.length === 0 || foregroundBusy" @click="ensureCandidatePeople(); showMoveDialog = true">
                    移动
                  </el-button>
                </el-tooltip>
                <el-tooltip content="排除选中的人脸样本（非人脸或低质量）" placement="top">
                  <el-button size="small" type="danger" plain :disabled="selectedFaceIds.length === 0 || foregroundBusy" :loading="excluding" @click="excludeSelectedFaces">
                    排除
                  </el-button>
                </el-tooltip>
                <span class="ops-divider" />
                <el-tooltip content="从其他人物中选择若干个，并入当前人物" placement="top">
                  <el-button size="small" plain :disabled="foregroundBusy" @click="ensureCandidatePeople(); showMergeDialog = true">
                    合并到当前
                  </el-button>
                </el-tooltip>
                <span class="ops-divider" />
                <el-tooltip content="计算当前人物与目标人物的相似度" placement="top">
                  <el-button size="small" plain :disabled="foregroundBusy" @click="ensureCandidatePeople(); showSimilarityDialog = true">
                    相似度
                  </el-button>
                </el-tooltip>
                <el-tooltip :content="person.hidden ? '在人物管理列表中恢复显示此人物' : '在人物管理列表中隐藏此人物'" placement="top">
                  <el-button size="small" plain :loading="togglingVisibility" :disabled="foregroundBusy" @click="handleToggleVisibility">
                    {{ person.hidden ? '恢复显示' : '隐藏' }}
                  </el-button>
                </el-tooltip>
                <el-tooltip content="将所有人脸打回未聚类状态，删除当前人物" placement="top">
                  <el-button size="small" type="danger" plain :loading="dissolving" :disabled="foregroundBusy" @click="handleDissolve">
                    解散
                  </el-button>
                </el-tooltip>
              </div>
            </div>
          </div>
        </div>
      </el-card>

      <!-- 下方 Tab 内容 -->
      <el-card shadow="never" class="section-card content-card">
        <div class="content-tabs-wrapper">
          <div class="density-switcher">
            <el-tooltip content="小图" placement="top">
              <el-button
                size="small"
                :type="currentGridSize === 'small' ? 'primary' : 'default'"
                :plain="currentGridSize !== 'small'"
                class="density-btn"
                @click="setGridSize('small')"
              >
                <el-icon><Grid /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip content="中图" placement="top">
              <el-button
                size="small"
                :type="currentGridSize === 'medium' ? 'primary' : 'default'"
                :plain="currentGridSize !== 'medium'"
                class="density-btn"
                @click="setGridSize('medium')"
              >
                <el-icon><Menu /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip content="大图" placement="top">
              <el-button
                size="small"
                :type="currentGridSize === 'large' ? 'primary' : 'default'"
                :plain="currentGridSize !== 'large'"
                class="density-btn"
                @click="setGridSize('large')"
              >
                <el-icon><Monitor /></el-icon>
              </el-button>
            </el-tooltip>
          </div>
          <el-tabs v-model="activeTab" class="content-tabs">
          <el-tab-pane :label="`照片（${person?.photo_count ?? 0}）`" name="photos">
            <el-empty v-if="photos.length === 0 && !photosLoading && photosLoadedOnce" description="暂无关联照片" />

            <VirtualMediaGrid
              v-else-if="photos.length > 0 || photosLoading"
              ref="photosGridRef"
              :items="photos"
              :columns="photoColumns"
              :row-height="photoRowHeight"
              :gap="14"
              :size-class="photoGridSize"
              :has-more="!photosFinished"
              :loading="photosLoading"
              @load-more="loadMorePhotos"
            >
              <template #item="{ item }">
                <button type="button" class="photo-card" @click="goToPhoto(item.id)">
                  <img :src="photoThumbnail(item)" :alt="item.file_name || `photo-${item.id}`" class="photo-image" loading="lazy" decoding="async" />
                  <div class="photo-card-main">
                    <div class="photo-title">{{ item.caption || item.file_name || `Photo #${item.id}` }}</div>
                    <div class="photo-subtitle">{{ formatTime(item.taken_at || item.created_at) }}</div>
                  </div>
                </button>
              </template>
            </VirtualMediaGrid>

            <!-- 无限滚动哨兵 -->
            <div class="scroll-sentinel">
              <span v-if="photosLoading" class="sentinel-status">加载中...</span>
              <span v-else-if="photosError" class="sentinel-status sentinel-error">
                加载失败，<el-button text type="primary" size="small" @click="loadMorePhotos">重试</el-button>
              </span>
              <span v-else-if="photosFinished" class="sentinel-status">已加载全部 {{ photosTotal }} 张照片</span>
            </div>
          </el-tab-pane>

          <el-tab-pane :label="`人脸（${person?.face_count ?? 0}）`" name="faces">
            <el-empty v-if="faces.length === 0 && !facesLoading && facesLoadedOnce" description="暂无人脸样本" />

            <VirtualMediaGrid
              v-else-if="faces.length > 0 || facesLoading"
              ref="facesGridRef"
              :items="faces"
              :columns="faceColumns"
              :row-height="faceRowHeight"
              :gap="10"
              :size-class="faceGridSize"
              :has-more="!facesFinished"
              :loading="facesLoading"
              @load-more="loadMoreFaces"
            >
              <template #item="{ item }">
                <div class="face-card" :class="{ 'is-selected': selectedFaceSet.has(item.id) }">
                  <div class="face-image-wrap">
                    <img :src="faceThumbnail(item)" alt="face" class="face-image" loading="lazy" decoding="async" />
                    <el-checkbox class="face-checkbox" :model-value="selectedFaceSet.has(item.id)" @change="toggleFace(item.id, $event as boolean)" />
                  </div>
                  <div class="face-info">
                    <div class="face-info-row">
                      <span class="face-info-id">{{ `#${item.id}` }}</span>
                      <el-tag v-if="person?.representative_face_id === item.id" type="success" size="small">头像</el-tag>
                    </div>
                    <div class="face-info-row">
                      <el-tooltip content="人脸图像质量评分" placement="top">
                        <span class="face-info-quality">{{ `质量 ${(item.quality_score || 0).toFixed(2)}` }}</span>
                      </el-tooltip>
                      <el-tooltip v-if="item.manual_locked" content="用户已人工确认归属" placement="top">
                        <span class="face-info-tag tag-success">人工</span>
                      </el-tooltip>
                      <el-tooltip v-else-if="item.cluster_score" :content="`聚类置信度 ${Math.round((item.cluster_score || 0) * 100)}%，越高表示归属越可靠`" placement="top">
                        <span class="face-info-tag" :class="(item.cluster_score || 0) >= 0.55 ? 'tag-success' : (item.cluster_score || 0) >= 0.45 ? 'tag-warning' : 'tag-danger'">{{ `${Math.round((item.cluster_score || 0) * 100)}%` }}</span>
                      </el-tooltip>
                    </div>
                    <div class="face-info-actions">
                      <el-tooltip :content="person?.representative_face_id === item.id ? '已是当前头像' : '将此人脸设为人物代表头像'" placement="top">
                        <el-button size="small" plain :disabled="person?.representative_face_id === item.id || avatarSavingFaceId === item.id" @click="setAvatar(item.id)">
                          {{ avatarSavingFaceId === item.id ? '设置中' : '头像' }}
                        </el-button>
                      </el-tooltip>
                      <el-tooltip content="查看此人脸所在的原始照片" placement="top">
                        <el-button size="small" plain @click="goToPhoto(item.photo_id)">照片</el-button>
                      </el-tooltip>
                    </div>
                  </div>
                </div>
              </template>
            </VirtualMediaGrid>

            <!-- 无限滚动哨兵 -->
            <div class="scroll-sentinel">
              <span v-if="facesLoading" class="sentinel-status">加载中...</span>
              <span v-else-if="facesError" class="sentinel-status sentinel-error">
                加载失败，<el-button text type="primary" size="small" @click="loadMoreFaces">重试</el-button>
              </span>
              <span v-else-if="facesFinished" class="sentinel-status">已加载全部 {{ facesTotal }} 张人脸</span>
            </div>
          </el-tab-pane>
        </el-tabs>
        </div>
      </el-card>
    </template>

    <el-dialog v-model="showMoveDialog" title="移动到其他人物" width="480px">
      <el-select v-model="moveTargetPersonId" filterable class="dialog-select" placeholder="选择目标人物">
        <el-option v-for="candidate in candidatePeople" :key="candidate.id" :label="candidateLabel(candidate)" :value="candidate.id">
          <div class="candidate-option">
            <el-avatar :size="34" :src="candidateAvatarUrl(candidate)">
              {{ getPersonAvatarFallback(candidate) }}
            </el-avatar>
            <div class="candidate-option-body">
              <div class="candidate-option-title">{{ candidate.name?.trim() || `未命名人物 #${candidate.id}` }}</div>
              <div class="candidate-option-subtitle">{{ getPersonCategoryLabel(candidate.category) }}</div>
            </div>
          </div>
        </el-option>
      </el-select>
      <template #footer>
        <el-button @click="showMoveDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!moveTargetPersonId" :loading="moving" @click="confirmMoveFaces">确认移动</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showMergeDialog" title="合并其他人物到当前人物" width="560px">
      <el-select v-model="mergeSourceIds" multiple filterable class="dialog-select" placeholder="选择要并入当前人物的对象">
        <el-option v-for="candidate in candidatePeople" :key="candidate.id" :label="candidateLabel(candidate)" :value="candidate.id">
          <div class="candidate-option">
            <el-avatar :size="34" :src="candidateAvatarUrl(candidate)">
              {{ getPersonAvatarFallback(candidate) }}
            </el-avatar>
            <div class="candidate-option-body">
              <div class="candidate-option-title">{{ candidate.name?.trim() || `未命名人物 #${candidate.id}` }}</div>
              <div class="candidate-option-subtitle">{{ getPersonCategoryLabel(candidate.category) }}</div>
            </div>
          </div>
        </el-option>
      </el-select>
      <template #footer>
        <el-button @click="showMergeDialog = false">取消</el-button>
        <el-button type="primary" :disabled="mergeSourceIds.length === 0" :loading="merging" @click="confirmMerge">确认合并</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showSimilarityDialog" title="计算人物相似度" width="560px">
      <el-select v-model="similarityTargetId" filterable class="dialog-select" placeholder="选择目标人物进行相似度计算">
        <el-option v-for="candidate in candidatePeople" :key="candidate.id" :label="candidateLabel(candidate)" :value="candidate.id">
          <div class="candidate-option">
            <el-avatar :size="34" :src="candidateAvatarUrl(candidate)">
              {{ getPersonAvatarFallback(candidate) }}
            </el-avatar>
            <div class="candidate-option-body">
              <div class="candidate-option-title">{{ candidate.name?.trim() || `未命名人物 #${candidate.id}` }}</div>
              <div class="candidate-option-subtitle">{{ getPersonCategoryLabel(candidate.category) }}</div>
            </div>
          </div>
        </el-option>
      </el-select>

      <div v-if="similarityResult !== null" class="similarity-result">
        <el-divider />
        <div class="similarity-score">
          <span class="similarity-label">相似度得分：</span>
          <span class="similarity-value" :class="getSimilarityClass(similarityResult.similarity_score)">
            {{ (similarityResult.similarity_score * 100).toFixed(1) }}%
          </span>
        </div>
        <div class="similarity-thresholds">
          <div class="threshold-item">
            <span class="threshold-label">合并建议阈值：</span>
            <span class="threshold-value">{{ (similarityResult.merge_threshold * 100).toFixed(0) }}%</span>
            <el-tag v-if="similarityResult.similarity_score >= similarityResult.merge_threshold" type="success" size="small">已达阈值</el-tag>
            <el-tag v-else type="info" size="small">未达阈值</el-tag>
          </div>
          <div class="threshold-item">
            <span class="threshold-label">附加阈值：</span>
            <span class="threshold-value">{{ (similarityResult.attach_threshold * 100).toFixed(0) }}%</span>
            <el-tag v-if="similarityResult.similarity_score >= similarityResult.attach_threshold" type="warning" size="small">会自动附加</el-tag>
            <el-tag v-else type="info" size="small">不会自动附加</el-tag>
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="showSimilarityDialog = false">关闭</el-button>
        <el-button type="primary" :disabled="!similarityTargetId" :loading="calculatingSimilarity" @click="calculateSimilarity">
          计算相似度
        </el-button>
      </template>
    </el-dialog>

    <!-- 复用人物列表页编辑弹窗：姓名 / 类别编辑 + 搜索目标人物合并 -->
    <PersonEditDialog
      v-model="editDialogVisible"
      :person="editingPerson"
      :loading="editSaving"
      @submit="handleEditSubmit"
      @merge="handleEditMergeRequest"
    />

    <PersonMergeConfirmDialog
      v-model="mergeConfirmVisible"
      :source="editingPerson"
      :target="mergeTarget"
      :loading="mergeSubmitting"
      :error="mergeError"
      @confirm="handleMergeConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Grid, Menu, Monitor, User } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import SectionHeader from '@/components/SectionHeader.vue'
import VirtualMediaGrid from '@/components/VirtualMediaGrid.vue'
import PersonEditDialog from './PersonEditDialog.vue'
import PersonMergeConfirmDialog from './PersonMergeConfirmDialog.vue'
import { peopleApi } from '@/api/people'
import type { Face, Person, PersonCategory } from '@/types/people'
import type { Photo } from '@/types/photo'
import { getPersonAvatarFallback, getPersonCategoryLabel, sortPeopleForDisplay } from './peopleHelpers'
import { isAllFacesMoved } from './peopleGridUtils'

const route = useRoute()
const router = useRouter()
const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const loading = ref(false)
const person = ref<Person | null>(null)
const faces = ref<Face[]>([])
const photos = ref<Photo[]>([])
const allPeople = ref<Person[]>([])
// 跨虚拟区域人脸选择：统一使用 Set<number>，卡片卸载不清除选择状态。
// 仅在刷新页面 / 切换人物 / 操作成功后清空；切换 Tab / 密度不清空。
const selectedFaceSet = ref<Set<number>>(new Set())
const selectedFaceCount = computed(() => selectedFaceSet.value.size)
const selectedFaceIds = computed(() => Array.from(selectedFaceSet.value))
const avatarSavingFaceId = ref<number | null>(null)
const splitting = ref(false)
const excluding = ref(false)
const moving = ref(false)
const merging = ref(false)
const dissolving = ref(false)
const togglingVisibility = ref(false)
const showMoveDialog = ref(false)
const showMergeDialog = ref(false)
const showSimilarityDialog = ref(false)
const moveTargetPersonId = ref<number>()
const mergeSourceIds = ref<number[]>([])
const similarityTargetId = ref<number>()
const calculatingSimilarity = ref(false)
// foregroundBusy 统一标记任一前台人物 mutation 进行中。前台操作进行中时，禁用
// split/move/merge/similarity 等所有前台操作，避免并发重复提交。后台任务治理计划 Task 5。
const foregroundBusy = computed(
  () => splitting.value || moving.value || merging.value || dissolving.value || togglingVisibility.value || calculatingSimilarity.value || excluding.value,
)

const similarityResult = ref<{
  person_id_1: number
  person_id_2: number
  similarity_score: number
  merge_threshold: number
  attach_threshold: number
} | null>(null)

// Tab 状态：默认关联照片
const activeTab = ref<'photos' | 'faces'>('photos')

// 视图密度：默认值 照片=中图、人脸=小图；并记忆到 localStorage（按浏览器持久化）
type GridSize = 'small' | 'medium' | 'large'

const PHOTO_GRID_KEY = 'people_detail_photoGridSize'
const FACE_GRID_KEY = 'people_detail_faceGridSize'

const isValidGridSize = (value: string | null): value is GridSize =>
  value === 'small' || value === 'medium' || value === 'large'

const readGridSize = (key: string, fallback: GridSize): GridSize => {
  const stored = localStorage.getItem(key)
  return isValidGridSize(stored) ? stored : fallback
}

const photoGridSize = ref<GridSize>(readGridSize(PHOTO_GRID_KEY, 'medium'))
const faceGridSize = ref<GridSize>(readGridSize(FACE_GRID_KEY, 'small'))

// 密度 → 列数与行高。列数响应式跟随容器宽度（与 CSS 媒体查询一致），
// 避免 virtualizer 按 15 列分组而 CSS 实际渲染 10 列导致的错位与虚拟区间错误。
// 行高为估算值，与 CSS 卡片样式近似匹配，保证滚动条/区间计算合理。
const baseColumnsForSize = (size: GridSize): number =>
  size === 'small' ? 15 : size === 'medium' ? 5 : 3

const rowHeightForSize = (size: GridSize): number =>
  size === 'small' ? 110 : size === 'medium' ? 260 : 420

// 视口宽度 → 实际列数（与 VirtualMediaGrid/Detail 的 CSS 媒体查询一致）。
// 宽度变化时（缩放窗口 / 切换密度 / 响应式断点）重新计算列数，保证虚拟分组与真实渲染列数对齐。
const viewportWidth = ref(typeof window !== 'undefined' ? window.innerWidth : 1280)
let resizeRaf = 0
const onViewportResize = () => {
  if (resizeRaf) cancelAnimationFrame(resizeRaf)
  resizeRaf = requestAnimationFrame(() => {
    viewportWidth.value = window.innerWidth
  })
}
if (typeof window !== 'undefined') {
  window.addEventListener('resize', onViewportResize)
}

const columnsForSize = (size: GridSize): number => {
  const base = baseColumnsForSize(size)
  const w = viewportWidth.value
  if (w <= 480) {
    return size === 'small' ? 4 : 2
  }
  if (w <= 768) {
    return size === 'small' ? 6 : size === 'medium' ? 3 : 2
  }
  if (w <= 1200) {
    return size === 'small' ? 10 : size === 'medium' ? 4 : 3
  }
  return base
}

const photoColumns = computed(() => columnsForSize(photoGridSize.value))
const faceColumns = computed(() => columnsForSize(faceGridSize.value))

const photoRowHeight = computed(() => rowHeightForSize(photoGridSize.value))
const faceRowHeight = computed(() => rowHeightForSize(faceGridSize.value))

// 当前 tab 对应的密度状态
const currentGridSize = computed<GridSize>(() =>
  activeTab.value === 'photos' ? photoGridSize.value : faceGridSize.value,
)

const setGridSize = (size: GridSize) => {
  // 切换密度：以切换前第一个可见项作为滚动锚点，切换后尽量保持其在视口附近。
  // 不清空数据、选择状态或分页状态，不重新请求已加载数据。
  const gridRef = activeTab.value === 'photos' ? photosGridRef.value : facesGridRef.value
  const anchorIndex = (gridRef && typeof (gridRef as any).getFirstVisibleIndex === 'function')
    ? (gridRef as any).getFirstVisibleIndex()
    : 0

  if (activeTab.value === 'photos') {
    photoGridSize.value = size
    localStorage.setItem(PHOTO_GRID_KEY, size)
  } else {
    faceGridSize.value = size
    localStorage.setItem(FACE_GRID_KEY, size)
  }

  // 等待列数/行高重算后恢复锚点
  nextTick(() => {
    if (gridRef && typeof (gridRef as any).scrollToIndex === 'function') {
      ;(gridRef as any).scrollToIndex(anchorIndex)
    }
  })
}

// 关联照片 - 无限滚动状态
const photosLoading = ref(false)
const photosError = ref(false)
const photosFinished = ref(false)
const photosPage = ref(1)
const photosPageSize = ref(30)
const photosTotal = ref(0)
// 首次按需加载标记：进入页面只加载当前默认 Tab“照片”第一页；
// “人脸” Tab 首次切换时才加载。已访问过的 Tab 再次切回时恢复数据和滚动位置。
const photosLoadedOnce = ref(false)
const facesLoadedOnce = ref(false)
// 虚拟网格 ref
const photosGridRef = ref<InstanceType<typeof VirtualMediaGrid> | null>(null)
const facesGridRef = ref<InstanceType<typeof VirtualMediaGrid> | null>(null)
// 各 Tab 滚动位置记忆
const photosScrollTop = ref(0)
const facesScrollTop = ref(0)

// 人脸样本 - 无限滚动状态
const facesLoading = ref(false)
const facesError = ref(false)
const facesFinished = ref(false)
const facesPage = ref(1)
const facesPageSize = ref(50)
const facesTotal = ref(0)

// 候选人物懒加载
const candidatePeopleLoaded = ref(false)
// ---- 编辑弹窗状态：与人物列表页一致 ----
const editDialogVisible = ref(false)
const editingPerson = ref<Person | null>(null)
const editSaving = ref(false)

// 编辑弹窗内发起的合并：来源=当前人物，目标=搜索结果所选
const mergeConfirmVisible = ref(false)
const mergeTarget = ref<Person | null>(null)
const mergeSubmitting = ref(false)
const mergeError = ref('')

const personTitle = computed(() => {
  if (!person.value) return '人物详情'
  return person.value.name?.trim() || `未命名人物 #${person.value.id}`
})

const avatarUrl = computed(() => {
  if (!person.value?.representative_face_id) return ''
  // 头像缩略图也使用 updated_at 版本化，避免人物信息更新后展示旧头像。
  const ts = person.value.updated_at
  const v = ts ? String(new Date(ts).getTime()) : String(person.value.representative_face_id)
  return `${apiBaseUrl}/faces/${person.value.representative_face_id}/thumbnail?v=${v}`
})

const candidatePeople = computed(() => {
  if (!person.value) return []
  return sortPeopleForDisplay(allPeople.value.filter(item => item.id !== person.value?.id && item.has_avatar))
})

const formatTime = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN')
}

// 版本化缩略图 URL：使用可反映缩略图变化的版本值（updated_at 时间戳），
// 没有时间戳时回退到 id。配合后端 immutable 长缓存，缩略图重建/旋转后版本变化使旧缓存失效。
const photoVersion = (photo: Photo): string => {
  const ts = photo.updated_at || photo.thumbnail_generated_at
  return ts ? String(new Date(ts).getTime()) : String(photo.id)
}

const faceVersion = (face: Face): string => {
  const ts = face.updated_at || face.excluded_at
  return ts ? String(new Date(ts).getTime()) : String(face.id)
}

const photoThumbnail = (photo: Photo) => `${apiBaseUrl}/photos/${photo.id}/thumbnail?v=${photoVersion(photo)}`
const faceThumbnail = (face: Face) => `${apiBaseUrl}/faces/${face.id}/thumbnail?v=${faceVersion(face)}`
// 候选人物头像：candidate 来自 peopleApi.getList（精简字段，无 face.updated_at），
// 用 candidate.updated_at（人物级，SetRepresentativeFace 会刷新人物 updated_at）作为版本参数，
// 头像变更后版本推进，避免 immutable 长缓存展示旧头像。无 updated_at 时回退到 faceId。
const candidateAvatarUrl = (item: Person) => {
  if (!item.representative_face_id) return ''
  const v = item.updated_at ? String(new Date(item.updated_at).getTime()) : String(item.representative_face_id)
  return `${apiBaseUrl}/faces/${item.representative_face_id}/thumbnail?v=${v}`
}

const candidateLabel = (item: Person) => `${item.name?.trim() || `未命名人物 #${item.id}`} · ${getPersonCategoryLabel(item.category)}`

const clearSelection = () => {
  selectedFaceSet.value = new Set()
}

const resetSelections = () => {
  selectedFaceSet.value = new Set()
  moveTargetPersonId.value = undefined
  mergeSourceIds.value = []
  similarityTargetId.value = undefined
  similarityResult.value = null
  showMoveDialog.value = false
  showMergeDialog.value = false
  showSimilarityDialog.value = false
}

const ensureCandidatePeople = async () => {
  if (candidatePeopleLoaded.value) return
  try {
    const res = await peopleApi.getList({ page: 1, page_size: 200, has_avatar: 'true' })
    allPeople.value = res.data?.data?.items || []
    candidatePeopleLoaded.value = true
  } catch (error) {
    console.error('Failed to load candidate people:', error)
  }
}

// --- 无限滚动：关联照片 ---

const loadMorePhotos = async () => {
  if (photosLoading.value || photosFinished.value) return
  const personId = Number(route.params.id)
  if (!personId) return

  photosLoading.value = true
  photosError.value = false
  try {
    const res = await peopleApi.getPhotos(personId, { page: photosPage.value, page_size: photosPageSize.value })
    const data = res.data?.data as any
    const items: Photo[] = (data && 'items' in data ? data.items : data as Photo[]) || []
    const totalCount: number = (data && 'total' in data ? data.total : items.length) || 0

    const existing = new Set(photos.value.map(p => p.id))
    const fresh = items.filter(p => !existing.has(p.id))
    photos.value = [...photos.value, ...fresh]
    photosTotal.value = totalCount
    photosPage.value += 1
    photosLoadedOnce.value = true

    if (items.length < photosPageSize.value || photos.value.length >= totalCount) {
      photosFinished.value = true
    }
  } catch (error: any) {
    photosError.value = true
    ElMessage.error(error.message || '加载照片失败')
  } finally {
    photosLoading.value = false
  }
}

// --- 无限滚动：人脸样本 ---

const loadMoreFaces = async () => {
  if (facesLoading.value || facesFinished.value) return
  const personId = Number(route.params.id)
  if (!personId) return

  facesLoading.value = true
  facesError.value = false
  try {
    const res = await peopleApi.getFaces(personId, { page: facesPage.value, page_size: facesPageSize.value })
    const data = res.data?.data as any
    const items: Face[] = (data && 'items' in data ? data.items : data as Face[]) || []
    const totalCount: number = (data && 'total' in data ? data.total : items.length) || 0

    const existing = new Set(faces.value.map(f => f.id))
    const fresh = items.filter(f => !existing.has(f.id))
    faces.value = [...faces.value, ...fresh]
    facesTotal.value = totalCount
    facesPage.value += 1
    facesLoadedOnce.value = true

    if (items.length < facesPageSize.value || faces.value.length >= totalCount) {
      facesFinished.value = true
    }
  } catch (error: any) {
    facesError.value = true
    ElMessage.error(error.message || '加载人脸失败')
  } finally {
    facesLoading.value = false
  }
}

// --- 数据加载 ---

const loadData = async () => {
  const personId = Number(route.params.id)
  if (!personId) return

  loading.value = true
  // 重置无限滚动状态
  faces.value = []
  photos.value = []
  facesPage.value = 1
  photosPage.value = 1
  facesFinished.value = false
  photosFinished.value = false
  facesError.value = false
  photosError.value = false
  facesTotal.value = 0
  photosTotal.value = 0
  photosLoadedOnce.value = false
  facesLoadedOnce.value = false
  photosScrollTop.value = 0
  facesScrollTop.value = 0

  try {
    // 首次进入：只加载人物基本信息 + 当前默认 Tab“照片”第一页。
    // 人脸 Tab 首次切换时再加载。
    const personRes = await peopleApi.getById(personId)
    person.value = personRes.data?.data || null
    resetSelections()
    candidatePeopleLoaded.value = false

    await loadMorePhotos()
  } catch (error: any) {
    ElMessage.error(error.message || '加载人物详情失败')
  } finally {
    loading.value = false
  }
}

// 精准刷新：只刷新人物信息，不重载照片和样本，不清空已选人脸
const refreshPerson = async () => {
  const personId = Number(route.params.id)
  if (!personId) return
  try {
    const res = await peopleApi.getById(personId)
    person.value = res.data?.data || null
  } catch (error: any) {
    ElMessage.error(error.message || '刷新人物信息失败')
  }
}

// 精准刷新：刷新人物信息 + 人脸
const refreshPersonAndFaces = async () => {
  const personId = Number(route.params.id)
  if (!personId) return
  try {
    // 重置人脸无限滚动
    faces.value = []
    facesPage.value = 1
    facesFinished.value = false
    facesError.value = false
    facesTotal.value = 0
    facesLoadedOnce.value = false

    const [personRes] = await Promise.all([
      peopleApi.getById(personId),
      loadMoreFaces(),
    ])
    person.value = personRes.data?.data || null
  } catch (error: any) {
    ElMessage.error(error.message || '刷新数据失败')
  }
}

// ---- 编辑弹窗：打开 / 提交 / 合并 ----

const openEditDialog = () => {
  if (!person.value) return
  editingPerson.value = person.value
  editDialogVisible.value = true
}

/**
 * 提交姓名 / 类别修改：成功后仅刷新人物信息，
 * 不重载关联照片与人脸样本，不清空已选人脸。
 */
const handleEditSubmit = async (payload: { name?: string; category?: PersonCategory }) => {
  const current = editingPerson.value
  if (!current) return
  editSaving.value = true
  try {
    const tasks: Promise<unknown>[] = []
    if (payload.name !== undefined) {
      tasks.push(peopleApi.updateName(current.id, payload.name))
    }
    if (payload.category !== undefined) {
      tasks.push(peopleApi.updateCategory(current.id, payload.category))
    }
    await Promise.all(tasks)
    ElMessage.success('人物信息已更新')
    editDialogVisible.value = false
    await refreshPerson()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '保存失败')
  } finally {
    editSaving.value = false
  }
}

/**
 * 编辑弹框中点击搜索结果：打开合并确认弹框。
 * 来源=当前编辑人物（使用已保存信息），目标=搜索结果所选。
 */
const handleEditMergeRequest = (target: Person) => {
  mergeTarget.value = target
  mergeError.value = ''
  mergeConfirmVisible.value = true
}

/**
 * 轮询合并任务直到完成/失败/超时。
 * - completed: 返回 true
 * - failed: 返回 { failed: true, message }
 * - 超时（状态未知）: 返回 'timeout'
 */
const pollMergeJob = async (
  jobId: number,
): Promise<boolean | 'timeout' | { failed: true; message: string }> => {
  const maxPolls = 60
  for (let i = 0; i < maxPolls; i++) {
    await new Promise(resolve => setTimeout(resolve, 2000))
    let job
    try {
      const res = await peopleApi.getMergeJob(jobId)
      job = res.data?.data
    } catch {
      continue
    }
    if (!job) return 'timeout'
    if (job.status === 'completed') return true
    if (job.status === 'failed') {
      return { failed: true, message: job.error_message || '合并任务失败' }
    }
  }
  return 'timeout'
}

/**
 * 确认合并：当前人物合并到目标人物。
 * 成功后跳转到目标人物详情页；失败/超时保留弹框并提示，不跳转。
 */
const handleMergeConfirm = async () => {
  const source = editingPerson.value
  const target = mergeTarget.value
  if (!source || !target) return
  mergeSubmitting.value = true
  mergeError.value = ''
  try {
    const res = await peopleApi.merge(target.id, [source.id])
    const jobId = res.data?.data?.job_id
    if (!jobId) {
      mergeError.value = '合并任务未返回任务 ID，请稍后刷新页面查看结果'
      return
    }
    const result = await pollMergeJob(jobId)
    if (result === 'timeout') {
      mergeError.value = '合并任务超时，请稍后刷新页面查看结果'
      return
    }
    if (typeof result === 'object' && result.failed) {
      mergeError.value = result.message
      return
    }
    // 合并成功：关闭弹框，跳转到目标人物详情页
    mergeConfirmVisible.value = false
    editDialogVisible.value = false
    ElMessage.success('当前人物已合并到目标人物')
    router.push(`/people/${target.id}`)
  } catch (error: any) {
    mergeError.value = error.response?.data?.error?.message || error.message || '合并人物失败'
  } finally {
    mergeSubmitting.value = false
  }
}

const setAvatar = async (faceId: number) => {
  if (!person.value) return
  try {
    avatarSavingFaceId.value = faceId
    await peopleApi.updateAvatar(person.value.id, faceId)
    ElMessage.success('代表头像已更新')
    await refreshPersonAndFaces()
  } catch (error: any) {
    ElMessage.error(error.message || '更新人物头像失败')
  } finally {
    avatarSavingFaceId.value = null
  }
}

const toggleFace = (faceId: number, checked: boolean) => {
  // 选择状态独立于卡片：用 Set 持久化，卡片卸载不清除，重入视口按集合恢复。
  const next = new Set(selectedFaceSet.value)
  if (checked) {
    next.add(faceId)
  } else {
    next.delete(faceId)
  }
  selectedFaceSet.value = next
}

const showReclusterResult = (data: any, baseMessage: string, asyncFollowUp = false) => {
  const evaluated = data?.recluster_evaluated || 0
  const reassigned = data?.recluster_reassigned || 0
  if (reassigned > 0) {
    ElMessage.success(`${baseMessage}（已重新评估 ${evaluated} 张不确定人脸，${reassigned} 张已重新分配）`)
  } else if (evaluated > 0) {
    ElMessage.success(`${baseMessage}（已重新评估 ${evaluated} 张不确定人脸，无需调整）`)
  } else if (asyncFollowUp) {
    ElMessage.success(`${baseMessage}，后台将继续重新评估不确定人脸`)
  } else {
    ElMessage.success(baseMessage)
  }
}

// isLikelyForegroundStillProcessing 判断一个错误是否“像超时”——即请求未拿到明确响应，
// 后端可能仍在处理中。只有这类错误才提示“操作可能仍在后台处理中，请稍后刷新”；
// validation error、4xx、带 response body 的明确 5xx 继续显示后端真实错误。
const isLikelyForegroundStillProcessing = (error: any): boolean => {
  // 无 response（网络中断 / 超时 / 请求未发出）→ 后端状态未知，可能仍在处理。
  if (!error?.response) return true
  // 502/503/504 网关超时类：上游可能仍在处理。
  const status = error.response.status
  return status === 502 || status === 503 || status === 504
}

const splitSelectedFaces = async () => {
  if (selectedFaceIds.value.length === 0) return
  // 双击防重复：若已有前台操作进行中，直接 return，不重复提交。
  if (foregroundBusy.value) return
  try {
    splitting.value = true
    const res = await peopleApi.split(selectedFaceIds.value)
    const data = res.data?.data as any
    const newPerson = data?.person
    showReclusterResult(data, '已拆分为新人物', true)
    if (newPerson?.id) {
      router.push(`/people/${newPerson.id}`)
      return
    }
    await loadData()
  } catch (error: any) {
    if (isLikelyForegroundStillProcessing(error)) {
      ElMessage.warning('操作可能仍在后台处理中，请稍后刷新页面查看结果')
    } else {
      ElMessage.error(error.response?.data?.error?.message || error.message || '拆分人物失败')
    }
  } finally {
    splitting.value = false
  }
}

const excludeSelectedFaces = async () => {
  if (selectedFaceIds.value.length === 0) return
  if (foregroundBusy.value) return

  let reason: 'non_face' | 'low_quality'
  try {
    const action = await ElMessageBox.confirm(
      '排除后，选中的样本将不再参与人物识别和聚类。重新检测照片时仍会保持排除。',
      '选择排除原因',
      {
        distinguishCancelAndClose: true,
        confirmButtonText: '非人脸',
        cancelButtonText: '低质量人脸',
        type: 'warning',
      },
    )
    reason = 'non_face'
  } catch (action: any) {
    if (action === 'cancel') {
      reason = 'low_quality'
    } else {
      return // 'close' - user closed dialog
    }
  }

  try {
    excluding.value = true
    await peopleApi.updateFaceExclusion(selectedFaceIds.value, true, reason)
    ElMessage.success('已排除选中的人脸样本')
    // 操作成功后清空选择
    selectedFaceSet.value = new Set()
    await loadData()
  } catch (error: any) {
    if (isLikelyForegroundStillProcessing(error)) {
      ElMessage.warning('操作可能仍在后台处理中，请稍后刷新页面查看结果')
    } else {
      ElMessage.error(error.response?.data?.error?.message || error.message || '排除人脸失败')
    }
    // If person was cleaned up (no more valid faces), navigate back
    try {
      const res = await peopleApi.getById(Number(route.params.id))
      if (!res.data?.data) {
        ElMessage.info('人物已无有效人脸样本')
        router.push('/people')
      }
    } catch {
      ElMessage.info('人物已无有效人脸样本')
      router.push('/people')
    }
  } finally {
    excluding.value = false
  }
}

const confirmMoveFaces = async () => {
  if (!moveTargetPersonId.value || selectedFaceIds.value.length === 0) return
  // 双击防重复：若已有前台操作进行中，直接 return，不重复提交。
  if (foregroundBusy.value) return
  try {
    moving.value = true
    // 是否移动了“人物全部人脸”：基于人脸总数 facesTotal（即 person.face_count 对应的分页 total）判断，
    // 不再比较 selectedFaceIds.length === faces.length（已加载），避免仅选中已加载全部时误判人物已被清空。
    const movedAll = isAllFacesMoved({
      selectedCount: selectedFaceIds.value.length,
      facesTotal: facesTotal.value,
    })
    const res = await peopleApi.moveFaces(selectedFaceIds.value, moveTargetPersonId.value)
    showReclusterResult(res.data?.data, '人脸已移动到目标人物', true)
    if (movedAll) {
      router.push('/people')
      return
    }
    await loadData()
  } catch (error: any) {
    if (isLikelyForegroundStillProcessing(error)) {
      ElMessage.warning('操作可能仍在后台处理中，请稍后刷新页面查看结果')
    } else {
      ElMessage.error(error.response?.data?.error?.message || error.message || '移动人脸失败')
    }
  } finally {
    moving.value = false
  }
}

const pollMergeJobForMerge = async (jobId: number) => {
  const maxPolls = 60
  for (let i = 0; i < maxPolls; i++) {
    await new Promise(resolve => setTimeout(resolve, 2000))
    try {
      const res = await peopleApi.getMergeJob(jobId)
      const job = res.data?.data
      if (!job) break

      if (job.status === 'completed') {
        ElMessage.success('人物已合并')
        await loadData()
        return
      }

      if (job.status === 'failed') {
        ElMessage.error(job.error_message || '合并任务失败')
        return
      }
    } catch {
      // 轮询出错继续重试
    }
  }
  ElMessage.warning('合并任务超时，请稍后刷新页面查看结果')
}

const confirmMerge = async () => {
  if (!person.value || mergeSourceIds.value.length === 0) return
  try {
    merging.value = true
    const res = await peopleApi.merge(person.value.id, mergeSourceIds.value)
    const jobId = res.data?.data?.job_id
    showMergeDialog.value = false
    mergeSourceIds.value = []
    if (jobId) {
      ElMessage.info('合并任务已提交，正在后台处理...')
      await pollMergeJobForMerge(jobId)
    }
  } catch (error: any) {
    ElMessage.error(error.message || '合并人物失败')
  } finally {
    merging.value = false
  }
}

const calculateSimilarity = async () => {
  if (!person.value || !similarityTargetId.value) return
  try {
    calculatingSimilarity.value = true
    const res = await peopleApi.calculateSimilarity(person.value.id, similarityTargetId.value)
    similarityResult.value = res.data?.data || null
  } catch (error: any) {
    ElMessage.error(error.message || '计算相似度失败')
  } finally {
    calculatingSimilarity.value = false
  }
}

const getSimilarityClass = (score: number) => {
  if (score >= 0.7) return 'similarity-high'
  if (score >= 0.5) return 'similarity-medium'
  return 'similarity-low'
}

const handleDissolve = async () => {
  if (!person.value) return
  try {
    await ElMessageBox.confirm(
      `确认解散「${person.value.name?.trim() || `人物 #${person.value.id}`}」？所有 ${person.value.face_count} 张人脸将回到未聚类状态，由系统重新自动聚类。此操作不可撤销。`,
      '解散人物确认',
      { confirmButtonText: '确认解散', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  dissolving.value = true
  try {
    const res = await peopleApi.dissolvePerson(person.value.id)
    ElMessage.success(`人物已解散，${res.data?.data?.faces_released || 0} 张人脸已释放`)
    router.push('/people')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '解散人物失败')
  } finally {
    dissolving.value = false
  }
}

const handleToggleVisibility = async () => {
  if (!person.value) return
  const willHide = !person.value.hidden
  const displayName = person.value.name?.trim() || `人物 #${person.value.id}`
  if (willHide) {
    try {
      await ElMessageBox.confirm(
        `确定要在人物管理列表中隐藏「${displayName}」吗？\n隐藏后此人物不会出现在默认人物列表中，但不会删除人物、人脸或照片。`,
        '隐藏人物确认',
        { confirmButtonText: '确认隐藏', cancelButtonText: '取消', type: 'warning' },
      )
    } catch {
      return
    }
  }
  togglingVisibility.value = true
  try {
    await peopleApi.updateVisibility([person.value.id], willHide)
    ElMessage.success(willHide ? '已隐藏该人物' : '已恢复显示')
    if (willHide) {
      goBack()
    } else {
      await refreshPerson()
    }
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || error.message || '操作失败')
  } finally {
    togglingVisibility.value = false
  }
}

const goToPhoto = (photoId: number) => {
  router.push(`/photos/${photoId}`)
}

const goBack = () => {
  const query = route.query
  if (query.page || query.page_size || query.search || query.category) {
    router.push({
      path: '/people',
      query: {
        ...(query.page && { page: query.page }),
        ...(query.page_size && { page_size: query.page_size }),
        ...(query.search && { search: query.search }),
        ...(query.category && { category: query.category }),
      }
    })
  } else {
    router.push('/people')
  }
}

// Tab 切换：按需加载 + 恢复各自滚动位置。不清空选择状态、不清空数据。
watch(activeTab, async (tab, prev) => {
  // 离开当前 Tab 前记忆滚动位置
  if (prev === 'photos') {
    photosScrollTop.value = photosGridRef.value?.scrollRef?.scrollTop ?? 0
  } else if (prev === 'faces') {
    facesScrollTop.value = facesGridRef.value?.scrollRef?.scrollTop ?? 0
  }

  // 首次切到人脸 Tab 才加载第一页
  if (tab === 'faces' && !facesLoadedOnce.value && !facesLoading.value) {
    await loadMoreFaces()
  }

  await nextTick()
  // 恢复目标 Tab 的滚动位置，并重新测量虚拟网格（隐藏 pane 变可见后需刷新可见区间）
  const target = tab === 'photos' ? photosGridRef.value : facesGridRef.value
  const top = tab === 'photos' ? photosScrollTop.value : facesScrollTop.value
  // target 可能为 stub 或尚未挂载；measure 仅在真实组件上调用
  if (target && typeof (target as any).measure === 'function') {
    ;(target as any).measure()
  }
  const scrollEl = (target as any)?.scrollRef as HTMLElement | undefined
  if (scrollEl) {
    scrollEl.scrollTop = top
  }
})

watch(() => route.params.id, async () => {
  await loadData()
})

onMounted(async () => {
  await loadData()
})

onBeforeUnmount(() => {
  // 响应式列数的 resize 监听在 columnsForSize 注册处自行清理；
  // 虚拟网格自管理滚动容器与 observer，无需手动 teardown。
  if (resizeRaf) cancelAnimationFrame(resizeRaf)
  if (typeof window !== 'undefined') {
    window.removeEventListener('resize', onViewportResize)
  }
})
</script>

<style scoped>
.people-detail-page {
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

/* 顶部操作台 */
.console-card :deep(.el-card__body) {
  padding: 20px 24px;
}

.console-layout {
  display: flex;
  gap: 24px;
  align-items: flex-start;
}

.console-info {
  flex: 1 1 50%;
  min-width: 0;
}

.console-ops {
  flex: 1 1 50%;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 12px;
  border-left: 1px solid var(--color-border);
  padding-left: 24px;
}

@media (max-width: 768px) {
  .console-layout {
    flex-direction: column;
  }

  .console-ops {
    border-left: none;
    border-top: 1px solid var(--color-border);
    padding-left: 0;
    padding-top: 16px;
  }
}

/* 人物身份摘要 */
.summary-avatar {
  display: flex;
  gap: 14px;
  align-items: center;
  cursor: pointer;
  border-radius: 12px;
  padding: 6px;
  margin: -6px;
  transition: background 0.15s ease;
}

.summary-avatar:hover {
  background: var(--color-bg-soft);
}

.summary-avatar-img {
  flex-shrink: 0;
}

.summary-name-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.summary-name {
  font-size: 20px;
  font-weight: 700;
  color: var(--color-text-primary);
}

.summary-category-chip {
  font-size: 12px;
  font-weight: 600;
  padding: 2px 10px;
  border-radius: 999px;
  white-space: nowrap;
}

.summary-category-chip.is-family {
  background: rgba(245, 108, 108, 0.12);
  color: #c45656;
}

.summary-category-chip.is-friend {
  background: rgba(103, 194, 58, 0.14);
  color: #5a9a3a;
}

.summary-category-chip.is-acquaintance {
  background: rgba(230, 162, 60, 0.14);
  color: #b8821f;
}

.summary-category-chip.is-stranger {
  background: rgba(144, 147, 153, 0.14);
  color: #8a8d93;
}

.summary-edit-btn {
  margin-left: 4px;
  font-weight: 400;
}

.summary-meta {
  margin-top: 6px;
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  align-items: center;
  color: var(--color-text-secondary);
  font-size: 12px;
}

.summary-meta-time {
  cursor: help;
  text-decoration: underline dotted;
}

/* 纠错操作工具条 */
.ops-toolbar {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.ops-toolbar-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.ops-toolbar-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.ops-toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.ops-toolbar-actions .el-button {
  margin-left: 0 !important;
}

.ops-divider {
  width: 1px;
  align-self: stretch;
  background: var(--color-border);
  margin: 2px 0;
}

.dialog-select {
  width: 100%;
}

/* Tab 内容 */
.content-card :deep(.el-card__body) {
  padding: 16px 24px 24px;
}

.content-tabs :deep(.el-tabs__header) {
  margin-bottom: 16px;
}

.content-tabs-wrapper {
  position: relative;
}

/* 视图密度切换控件 */
.density-switcher {
  position: absolute;
  top: 0;
  right: 0;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.density-btn {
  padding: 0;
  width: 30px;
  height: 30px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.density-btn .el-icon {
  font-size: 16px;
}

@media (max-width: 480px) {
  .density-switcher {
    top: auto;
    bottom: -40px;
  }
}

/* 人脸网格 */
.face-grid {
  display: grid;
  gap: 10px;
}

.face-grid.is-small {
  grid-template-columns: repeat(15, minmax(0, 1fr));
}

.face-grid.is-medium {
  grid-template-columns: repeat(5, minmax(0, 1fr));
}

.face-grid.is-large {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.face-card {
  border: 2px solid transparent;
  border-radius: 10px;
  background: #fff;
  overflow: hidden;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.face-card.is-selected {
  border-color: var(--color-primary);
  box-shadow: 0 4px 12px rgba(84, 112, 198, 0.15);
}

.face-image-wrap {
  position: relative;
}

.face-image {
  width: 100%;
  aspect-ratio: 1;
  object-fit: cover;
  display: block;
  background: var(--color-bg-soft);
}

.face-checkbox {
  position: absolute;
  top: 4px;
  left: 4px;
  z-index: 1;
}

.face-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px 8px 4px;
}

.face-info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  min-height: 20px;
}

.face-info-id {
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.face-info-quality {
  font-size: 11px;
  color: var(--color-text-secondary);
}

.face-info-tag {
  font-size: 11px;
  font-weight: 600;
}

.face-info-tag.tag-success {
  color: #67c23a;
}

.face-info-tag.tag-warning {
  color: #e6a23c;
}

.face-info-tag.tag-danger {
  color: #f56c6c;
}

.face-info-actions {
  display: flex;
  gap: 4px;
  margin-top: 2px;
}

.face-info-actions .el-button {
  flex: 1;
  font-size: 11px;
  padding: 4px 0;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
}

/* 照片网格 */
.photo-grid {
  display: grid;
  gap: 14px;
}

.photo-grid.is-small {
  grid-template-columns: repeat(15, minmax(0, 1fr));
}

.photo-grid.is-medium {
  grid-template-columns: repeat(5, minmax(0, 1fr));
}

.photo-grid.is-large {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.photo-card {
  border: 1px solid var(--color-border);
  border-radius: 16px;
  padding: 0;
  background: #fff;
  cursor: pointer;
  overflow: hidden;
  text-align: left;
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

.photo-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 10px 20px rgba(15, 23, 42, 0.08);
}

.photo-image {
  width: 100%;
  aspect-ratio: 1;
  object-fit: cover;
  background: var(--color-bg-soft);
}

.photo-card-main {
  padding: 12px;
}

.photo-title {
  font-weight: 600;
  color: var(--color-text-primary);
  line-height: 1.5;
}

.photo-subtitle {
  margin-top: 4px;
  color: var(--color-text-secondary);
  font-size: 12px;
}

/* 无限滚动哨兵 */
.scroll-sentinel {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 40px;
  margin-top: 16px;
}

.sentinel-status {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.sentinel-error {
  color: var(--color-danger, #f56c6c);
}

/* 候选人选项 */
.candidate-option {
  display: flex;
  align-items: center;
  gap: 12px;
}

.candidate-option-body {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.candidate-option-title {
  color: var(--color-text-primary);
  font-weight: 600;
  line-height: 1.4;
}

.candidate-option-subtitle {
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1.4;
}

/* 相似度结果 */
.similarity-result {
  margin-top: 12px;
}

.similarity-score {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.similarity-label {
  color: var(--color-text-secondary);
  font-size: 14px;
}

.similarity-value {
  font-size: 24px;
  font-weight: 700;
}

.similarity-high {
  color: #67c23a;
}

.similarity-medium {
  color: #e6a23c;
}

.similarity-low {
  color: #f56c6c;
}

.similarity-thresholds {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.threshold-item {
  display: flex;
  align-items: center;
  gap: 8px;
}

.threshold-label {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.threshold-value {
  font-weight: 600;
  color: var(--color-text-primary);
}

/* 响应式 */
@media (max-width: 1200px) {
  .face-grid.is-small {
    grid-template-columns: repeat(10, minmax(0, 1fr));
  }

  .face-grid.is-medium {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .photo-grid.is-small {
    grid-template-columns: repeat(8, minmax(0, 1fr));
  }

  .photo-grid.is-medium {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .photo-grid.is-large {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .people-detail-page {
    padding: 16px;
  }

  .section-card :deep(.el-card__header),
  .section-card :deep(.el-card__body) {
    padding-left: 18px;
    padding-right: 18px;
  }

  .console-card :deep(.el-card__body) {
    padding: 16px 18px;
  }

  .face-grid.is-small {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }

  .face-grid.is-medium {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .face-grid.is-large {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .photo-grid.is-small {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .photo-grid.is-medium {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .photo-grid.is-large {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 480px) {
  .face-grid.is-small {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .face-grid.is-medium,
  .face-grid.is-large {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .photo-grid.is-small {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .photo-grid.is-medium,
  .photo-grid.is-large {
    grid-template-columns: 1fr;
  }
}
</style>
