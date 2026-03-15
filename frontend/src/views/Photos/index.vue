<template>
  <div class="photos-page">
    <PageHeader title="照片管理" subtitle="浏览和管理您的照片集合" :gradient="true" />

    <!-- 扫描路径列表 -->
    <el-card shadow="never" class="scan-paths-card animate-fade-in" v-loading="scanPathLoading">
      <template #header>
        <SectionHeader :icon="FolderOpened" :title="`扫描路径 (${scanPaths.length})`">
        <template #actions>
          <div class="scan-paths-actions">
            <div class="auto-scan-inline">
              <el-tooltip content="全局自动扫描开关" placement="top">
                <el-switch
                  v-model="autoScanConfig.enabled"
                  size="small"
                  active-text="自动扫描"
                  @change="handleAutoScanToggle"
                />
              </el-tooltip>
              <el-select
                v-if="autoScanConfig.enabled"
                v-model="autoScanConfig.interval_minutes"
                size="small"
                class="auto-scan-interval-select"
                @change="handleAutoScanIntervalChange"
              >
                <el-option :value="10" label="10 分钟" />
                <el-option :value="30" label="30 分钟" />
                <el-option :value="60" label="1 小时" />
                <el-option :value="120" label="2 小时" />
                <el-option :value="720" label="12 小时" />
                <el-option :value="1440" label="1 天" />
              </el-select>
            </div>
            <el-button
              type="danger"
              size="small"
              plain
              :loading="cleaningUp"
              @click="handleCleanup"
              class="cleanup-btn"
              title="清理数据库中所有文件已不存在的照片记录"
            >
              <el-icon><Delete /></el-icon>
              清理
            </el-button>
            <el-button type="primary" size="small" @click="handleAddPath" class="manage-btn">
              <el-icon><Plus /></el-icon>
              添加路径
            </el-button>
          </div>
        </template>
      </SectionHeader>
      </template>

      <el-table
        :data="scanPaths"
        class="full-width-table scan-path-table"
        size="small"
      >
        <el-table-column prop="name" label="路径名称" min-width="120">
          <template #default="{ row }">
            <div class="path-name-cell">
              <el-icon class="path-icon"><Folder /></el-icon>
              <span
                class="path-name clickable"
                :class="{ active: searchQuery === row.path }"
                @click="handlePathClick(row)"
                :title="`点击搜索: ${row.path}`"
              >
                {{ row.name }}
              </span>
            </div>
          </template>
        </el-table-column>

        <el-table-column prop="path" label="路径" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="path-text" :title="row.path">{{ row.path }}</span>
          </template>
        </el-table-column>

        <el-table-column label="照片数" width="80" align="center">
          <template #default="{ row }">
            <span class="photo-count">{{ getDisplayPathPhotoCount(row.path) }}</span>
          </template>
        </el-table-column>

        <el-table-column label="状态" width="160" align="center">
          <template #default="{ row }">
            <div class="derived-status-icons">
              <el-tooltip :content="row.enabled ? '点击禁用' : '点击启用'" placement="top">
                <el-switch
                  v-model="row.enabled"
                  size="small"
                  @change="handleToggleEnabled(row)"
                  style="margin-right: 4px;"
                />
              </el-tooltip>
              <el-tooltip :content="getPathAnalysisDerivedTooltip(row.path)" placement="top">
                <span class="derived-status-icon" :class="getPathAnalysisDerivedState(row.path)">
                  <el-icon><MagicStick /></el-icon>
                </span>
              </el-tooltip>
              <el-tooltip :content="getPathThumbnailDerivedTooltip(row.path)" placement="top">
                <span class="derived-status-icon" :class="getPathThumbnailDerivedState(row.path)">
                  <el-icon><Files /></el-icon>
                </span>
              </el-tooltip>
              <el-tooltip :content="getPathGeocodeDerivedTooltip(row.path)" placement="top">
                <span class="derived-status-icon" :class="getPathGeocodeDerivedState(row.path)">
                  <el-icon><Location /></el-icon>
                </span>
              </el-tooltip>
              <el-tooltip :content="row.auto_scan_enabled ? '自动扫描' : '仅手动扫描'" placement="top">
                <span class="derived-status-icon" :class="row.auto_scan_enabled ? 'is-ready' : 'is-idle'">
                  <el-icon><Timer /></el-icon>
                </span>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <el-table-column prop="last_scanned_at" label="上次扫描" width="140" align="center">
          <template #default="{ row }">
            <div class="scan-time-cell">
              <!-- 扫描中状态 -->
              <template v-if="isPathScanning(row)">
                <span class="scan-time scanning">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  扫描中...
                </span>
              </template>
              <!-- 重建中状态 -->
              <template v-else-if="isPathRebuilding(row)">
                <span class="scan-time rebuilding">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  重建中...
                </span>
              </template>
              <!-- 已扫描状态 -->
              <template v-else-if="row.last_scanned_at">
                <el-tooltip :content="formatDateTime(row.last_scanned_at)" placement="top">
                  <span class="scan-time">{{ formatRelativeTime(row.last_scanned_at) }}</span>
                </el-tooltip>
              </template>
              <!-- 未扫描状态 -->
              <el-tag v-else type="warning" size="small" effect="light">未扫描</el-tag>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="操作" width="250" align="center">
          <template #default="{ row }">
            <div class="path-action-group">
              <el-button
                v-if="shouldShowStopButton(row)"
                type="danger"
                size="small"
                plain
                :loading="currentTaskStatus === 'stopping'"
                :disabled="currentTaskStatus === 'stopping'"
                @click="handleStopTask(row)"
                class="scan-btn"
              >
                {{ currentTaskStatus === 'stopping' ? '停止中' : '停止' }}
              </el-button>
              <el-button
                v-if="shouldShowScanButton(row)"
                type="primary"
                size="small"
                plain
                :disabled="!row.enabled || scanningPathId === row.id"
                :loading="scanningPathId === row.id"
                @click="handleScanPath(row)"
                class="scan-btn"
              >
                扫描
              </el-button>
              <el-button
                v-if="shouldShowRebuildButton(row)"
                type="warning"
                size="small"
                plain
                :disabled="!row.enabled || rebuildingPathId === row.id"
                :loading="rebuildingPathId === row.id"
                @click="handleRebuildPath(row)"
                class="rebuild-btn"
                title="重建照片：重新扫描文件、提取 EXIF、计算哈希、地理编码（保留 AI 分析结果）"
              >
                重建
              </el-button>
              <el-button
                size="small"
                plain
                :disabled="isPathTaskActive(row)"
                @click="handleEditPath(row)"
                class="edit-btn"
              >
                编辑
              </el-button>
              <el-button
                type="danger"
                size="small"
                plain
                :disabled="isPathTaskActive(row)"
                @click="handleDeletePath(row)"
                class="delete-btn"
              >
                删除
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>

      <!-- 回收站（虚拟路径） -->
      <div
        v-if="excludedCount > 0"
        class="recycle-bin-row"
        :class="{ active: filterStatus === 'excluded' }"
        @click="handleRecycleBinClick"
      >
        <div class="recycle-bin-cell name">
          <el-icon class="path-icon" style="color: var(--el-color-danger)"><Delete /></el-icon>
          <span class="path-name">回收站</span>
        </div>
        <div class="recycle-bin-cell path">已排除的照片</div>
        <div class="recycle-bin-cell count">{{ excludedCount }}</div>
      </div>

      <el-empty v-if="scanPaths.length === 0 && !scanPathLoading" description="暂无扫描路径" :image-size="80">
        <el-button type="primary" @click="handleAddPath">
          <el-icon><Plus /></el-icon>
          添加路径
        </el-button>
      </el-empty>
    </el-card>

    <!-- 照片列表 -->
    <el-card shadow="never" class="photos-grid-card animate-fade-in" v-loading="loading">
      <template #header>
        <SectionHeader :icon="Picture" :title="`照片列表（共 ${total} 张）`">
        <template #actions>
          <div class="photos-list-actions">
            <el-radio-group v-model="filterAnalyzed" @change="handleSearch" size="default" class="filter-group">
              <el-radio-button label="">全部</el-radio-button>
              <el-radio-button label="true">已分析</el-radio-button>
              <el-radio-button label="false">未分析</el-radio-button>
            </el-radio-group>
            <el-radio-group v-model="filterThumbnail" @change="handleSearch" size="default" class="filter-group">
              <el-radio-button label="">全部</el-radio-button>
              <el-radio-button label="true">有缩略</el-radio-button>
              <el-radio-button label="false">无缩略</el-radio-button>
            </el-radio-group>
            <el-radio-group v-model="filterGPS" @change="handleSearch" size="default" class="filter-group">
              <el-radio-button label="">全部</el-radio-button>
              <el-radio-button label="true">有位置</el-radio-button>
              <el-radio-button label="false">无位置</el-radio-button>
            </el-radio-group>
          </div>
        </template>
      </SectionHeader>
      </template>

      <!-- 空状态：系统中没有照片 -->
      <el-empty v-if="!photos.length && !loading && systemTotal === 0" description="暂无照片" :image-size="120">
        <el-button type="primary" @click="handleAddPath">
          <el-icon><Plus /></el-icon>
          添加扫描路径
        </el-button>
      </el-empty>

      <!-- 空状态：搜索结果为空 -->
      <el-empty v-else-if="!photos.length && !loading && systemTotal > 0" description="未找到匹配的照片" :image-size="120">
        <p class="empty-hint">系统中共有 {{ systemTotal }} 张照片，但没有符合当前搜索条件的结果</p>
        <el-button type="primary" @click="resetSearch">
          <el-icon><Refresh /></el-icon>
          清除搜索条件
        </el-button>
      </el-empty>

      <!-- 照片网格 -->
      <div v-else>
        <div class="photos-toolbar">
        <!-- 搜索区域 -->
        <div class="search-section">
          <el-input
            v-model="searchQuery"
            placeholder="搜索照片 (路径、设备ID、标签...)"
            clearable
            @clear="handleSearch"
            @keyup.enter="handleSearch"
            class="search-input-with-btn"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-button type="primary" @click="handleSearch" class="search-btn">
            搜索
          </el-button>
        </div>

        <!-- 分类筛选 -->
        <div class="filter-section" v-if="categories.length > 0">
          <div class="filter-label">
            <el-icon><Collection /></el-icon>
            <span>分类</span>
          </div>
          <div class="filter-tags">
            <el-tag
              v-for="category in categories"
              :key="category"
              :type="filterCategory === category ? 'primary' : 'info'"
              class="filter-tag"
              @click="handleCategoryClick(category)"
            >
              {{ category }}
            </el-tag>
          </div>
        </div>

        <!-- 标签筛选 -->
        <div class="filter-section" v-if="tags.length > 0">
          <div class="filter-label">
            <el-icon><PriceTag /></el-icon>
            <span>标签</span>
            <el-tag type="info" size="small" effect="plain" class="count-tag">{{ tags.length }}</el-tag>
          </div>
          <div class="filter-tags">
            <el-tag
              v-for="tag in displayedTags"
              :key="tag"
              :type="filterTag === tag ? 'primary' : 'info'"
              class="filter-tag"
              @click="handleTagClick(tag)"
            >
              {{ tag }}
            </el-tag>
            <el-button
              v-if="tags.length > TAGS_DISPLAY_LIMIT"
              link
              size="small"
              class="collapse-btn"
              @click="tagsCollapsed = !tagsCollapsed"
            >
              <el-icon class="collapse-icon">
                <ArrowDown v-if="tagsCollapsed" />
                <ArrowUp v-else />
              </el-icon>
              {{ tagsCollapsed ? `展开全部 (${tags.length})` : '收起' }}
            </el-button>
          </div>
        </div>

        <!-- 统计信息 -->
        <div class="photos-stats" v-if="filterAnalyzed">
          <div class="stats-left">
            <div class="stat-item">
              <el-icon class="stat-icon"><Filter /></el-icon>
              <span class="stat-text">当前显示筛选结果</span>
            </div>
          </div>
        </div>
        </div>

        <div class="photo-grid">
          <div
            v-for="(photo, index) in photos"
            :key="photo.id"
            class="photo-col"
          >
            <div
              class="photo-card photo-card-parallax animate-scale-in"
              :style="{ animationDelay: `${index * 30}ms` }"
              :class="{ 'is-selected': selectedPhotos.has(photo.id) }"
              @click="selectedPhotos.size > 0 ? toggleSelectPhoto(photo.id) : gotoDetail(photo.id)"
            >
              <div class="photo-image-wrapper">
                <!-- 选择按钮 -->
                <div
                  class="photo-select-btn"
                  :class="{ selected: selectedPhotos.has(photo.id) }"
                  @click.stop="toggleSelectPhoto(photo.id)"
                >
                  <el-icon v-if="selectedPhotos.has(photo.id)"><Select /></el-icon>
                </div>
                <el-image
                    :src="getPhotoThumbnailUrl(photo.id, photo.updated_at)"
                  :preview-src-list="[]"
                  fit="cover"
                  class="photo-image"
                  loading="lazy"
                >
                  <template #error>
                    <div class="image-error">
                      <el-icon><PictureFilled /></el-icon>
                      <span>加载失败</span>
                    </div>
                  </template>
                  <template #placeholder>
                    <div class="image-loading">
                      <el-icon class="is-loading"><Loading /></el-icon>
                    </div>
                  </template>
                </el-image>

                <!-- 分析状态徽章 -->
                <div class="photo-badge" v-if="photo.ai_analyzed" :class="getScoreClass(photo.overall_score)">
                  <el-icon><Star /></el-icon>
                  <span>{{ photo.overall_score?.toFixed(1) }}</span>
                </div>

                <div class="photo-status-icons">
                  <span
                    class="photo-status-icon"
                    :class="photo.ai_analyzed ? 'is-ready' : 'is-idle'"
                    title="AI 分析状态"
                  >
                    <el-icon><MagicStick /></el-icon>
                  </span>
                  <span class="photo-status-icon" :class="photo.thumbnail_status === 'ready' ? 'is-ready' : 'is-idle'" title="缩略图状态">
                    <el-icon><Files /></el-icon>
                  </span>
                  <span
                    class="photo-status-icon"
                    :class="photo.location ? 'is-ready' : 'is-idle'"
                    :title="photo.gps_latitude && photo.gps_longitude ? 'GPS 位置状态' : '无 GPS 信息'"
                  >
                    <el-icon><Location /></el-icon>
                  </span>
                </div>

                <!-- 悬停信息 -->
                <div class="photo-overlay">
                  <div class="photo-info">
                    <div class="photo-name" :title="getFileName(photo.file_path)">
                      {{ getFileName(photo.file_path) }}
                    </div>
                    <div class="photo-meta">
                      <span v-if="photo.taken_at" class="meta-item">
                        <el-icon><Clock /></el-icon>
                        {{ formatDate(photo.taken_at) }}
                      </span>
                      <span v-if="photo.width && photo.height" class="meta-item">
                        <el-icon><FullScreen /></el-icon>
                        {{ photo.width }}×{{ photo.height }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 分页 -->
        <div class="pagination-wrapper">
          <el-pagination
            v-model:current-page="currentPage"
            v-model:page-size="pageSize"
            :page-sizes="[20, 50, 100]"
            :total="total"
            layout="total, sizes, prev, pager, next, jumper"
            @size-change="handlePageChange"
            @current-change="handlePageChange"
            background
          />
        </div>
      </div>
    </el-card>

    <!-- 选中照片悬浮工具栏 -->
    <Transition name="float-toolbar">
      <div v-if="selectedPhotos.size > 0" class="selection-toolbar">
        <el-button
          :icon="Close"
          circle
          size="small"
          @click="selectedPhotos = new Set()"
          title="取消选择"
        />
        <span class="selection-count">已选中 {{ selectedPhotos.size }} 张照片</span>
        <el-tooltip content="设置位置" placement="top">
          <el-button
            :icon="Location"
            circle
            @click="showBatchLocationPicker = true"
            :loading="batchLocationLoading"
          />
        </el-tooltip>
        <el-tooltip :content="filterStatus === 'excluded' ? '恢复选中照片' : '移除选中照片'" placement="top">
          <el-button
            :type="filterStatus === 'excluded' ? 'success' : 'danger'"
            :icon="filterStatus === 'excluded' ? RefreshLeft : Delete"
            circle
            @click="filterStatus === 'excluded' ? handleRestoreSelected() : handleExcludeSelected()"
            :loading="excludingPhotos"
          />
        </el-tooltip>
      </div>
    </Transition>

    <!-- 添加/编辑扫描路径对话框 -->
    <el-dialog
      v-model="addPathDialogVisible"
      :title="isEditPath ? '编辑扫描路径' : '添加扫描路径'"
      width="600px"
    >
      <el-form :model="pathForm" label-width="100px">
        <el-form-item label="名称" required>
          <el-input v-model="pathForm.name" placeholder="例如: iPhone 2025-11" />
        </el-form-item>
        <el-form-item label="路径" required>
          <div class="input-with-button">
            <el-input v-model="pathForm.path" placeholder="/path/to/photos" />
            <el-button @click="pathBrowserVisible = true">
              <el-icon><FolderOpened /></el-icon>
              浏览
            </el-button>
            <el-button @click="handleValidatePath" :loading="validatingPath">验证</el-button>
          </div>
          <div v-if="pathValidationResult" :class="['validation-result', pathValidationResult.valid ? 'valid' : 'invalid']">
            <el-icon v-if="pathValidationResult.valid"><CircleCheck /></el-icon>
            <el-icon v-else><CircleClose /></el-icon>
            <span>{{ pathValidationResult.valid ? '路径有效' : pathValidationResult.error }}</span>
          </div>
        </el-form-item>
        <el-form-item label="设置">
          <el-switch v-model="pathForm.enabled" active-text="启用" inactive-text="禁用" style="margin-right: 24px;" />
          <el-switch v-model="pathForm.auto_scan_enabled" active-text="自动扫描" inactive-text="仅手动" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="addPathDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleSavePath" :loading="savingPath">保存</el-button>
      </template>
    </el-dialog>

    <!-- 路径浏览器 -->
    <PathBrowser v-model="pathBrowserVisible" :initial-path="pathForm.path" @select="(path: string) => pathForm.path = path" />

    <!-- 批量设置位置 -->
    <LocationPicker
      v-model:visible="showBatchLocationPicker"
      @confirm="handleBatchLocationConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { ArrowDown, ArrowUp, CircleCheck, CircleClose, Clock, Close, Collection, Delete, Files, Filter, Folder, FolderOpened, FullScreen, Loading, Location, MagicStick, Picture, PictureFilled, Plus, PriceTag, QuestionFilled, Refresh, RefreshLeft, Search, Select, Star, SwitchButton, Timer } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import PathBrowser from '@/components/PathBrowser.vue'
import LocationPicker from '@/components/LocationPicker.vue'
import { photoApi } from '@/api/photo'
import { configApi, type ScanPathConfig, type AutoScanConfig } from '@/api/config'
import type { Photo } from '@/types/photo'
import { useUserStore } from '@/stores/user'
import { v4 as uuidv4 } from 'uuid'

const router = useRouter()
const userStore = useUserStore()

const photos = ref<Photo[]>([])
const loading = ref(false)
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const systemTotal = ref(0) // 系统中所有照片的总数（不带筛选）
const searchQuery = ref('')
const filterCategory = ref('')  // 分类精确筛选
const filterTag = ref('')       // 标签筛选
const filterAnalyzed = ref('')
const filterThumbnail = ref('')
const filterGPS = ref('')
const filterStatus = ref('') // '': active(默认), 'excluded': 回收站
const scanPaths = ref<ScanPathConfig[]>([])
const scanPathLoading = ref(false)
const scanningPathId = ref<string>('')
const rebuildingPathId = ref<string>('')
const currentTaskId = ref<string>('')
const currentTaskStatus = ref<string>('')
const currentScanPath = ref<string>('') // 当前正在扫描的路径
const currentScanType = ref<'scan' | 'rebuild' | ''>('') // 当前扫描类型
const cleaningUp = ref(false)
const autoScanConfig = ref<AutoScanConfig>(configApi.getDefaultAutoScanConfig())
const selectedPhotos = ref<Set<number>>(new Set())
const excludingPhotos = ref(false)
const showBatchLocationPicker = ref(false)
const batchLocationLoading = ref(false)

const toggleSelectPhoto = (id: number) => {
  const next = new Set(selectedPhotos.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  selectedPhotos.value = next
}

const handleExcludeSelected = async () => {
  const ids = Array.from(selectedPhotos.value)
  try {
    await ElMessageBox.confirm(
      `确定要移除选中的 ${ids.length} 张照片吗？移除后照片将进入回收站。`,
      '确认移除',
      { type: 'warning', confirmButtonText: '确认移除', cancelButtonText: '取消' }
    )
  } catch {
    return
  }

  excludingPhotos.value = true
  try {
    await photoApi.batchUpdateStatus({ photo_ids: ids, status: 'excluded' })
    ElMessage.success(`已移除 ${ids.length} 张照片`)
    selectedPhotos.value = new Set()
    loadPhotos()
    loadPhotoCounts()
    loadPathDerivedStatus()
  } catch (error: any) {
    ElMessage.error(error.message || '移除失败')
  } finally {
    excludingPhotos.value = false
  }
}

const handleRestoreSelected = async () => {
  const ids = Array.from(selectedPhotos.value)
  excludingPhotos.value = true
  try {
    await photoApi.batchUpdateStatus({ photo_ids: ids, status: 'active' })
    ElMessage.success(`已恢复 ${ids.length} 张照片`)
    selectedPhotos.value = new Set()
    loadPhotos()
    loadPhotoCounts()
    loadPathDerivedStatus()
  } catch (error: any) {
    ElMessage.error(error.message || '恢复失败')
  } finally {
    excludingPhotos.value = false
  }
}

const handleBatchLocationConfirm = async (coords: { latitude: number; longitude: number }) => {
  const ids = Array.from(selectedPhotos.value)
  batchLocationLoading.value = true
  let success = 0
  let failed = 0
  for (const id of ids) {
    try {
      await photoApi.setLocation(id, coords)
      success++
    } catch {
      failed++
    }
  }
  batchLocationLoading.value = false
  if (failed === 0) {
    ElMessage.success(`已为 ${success} 张照片设置位置`)
  } else {
    ElMessage.warning(`成功 ${success} 张，失败 ${failed} 张`)
  }
  selectedPhotos.value = new Set()
  loadPhotos()
  loadPathDerivedStatus()
}

const excludedCount = ref(0)
const categories = ref<string[]>([])
const tags = ref<string[]>([])

// 标签折叠状态
const tagsCollapsed = ref(true)
const TAGS_DISPLAY_LIMIT = 15

// 计算要显示的标签（根据折叠状态）
const displayedTags = computed(() => {
  if (tagsCollapsed.value && tags.value.length > TAGS_DISPLAY_LIMIT) {
    return tags.value.slice(0, TAGS_DISPLAY_LIMIT)
  }
  return tags.value
})

// 存储每个路径的照片数量（从数据库获取）
const pathPhotoCounts = ref<Record<string, number>>({})
const pathPhotoCountDeltas = ref<Record<string, number>>({})
const pathDerivedStatus = ref<Record<string, any>>({})

const getDisplayPathPhotoCount = (path: string) => {
  return Math.max(0, (pathPhotoCounts.value[path] || 0) + (pathPhotoCountDeltas.value[path] || 0))
}

const updatePathPhotoCountDelta = (path: string, task: any) => {
  if (!path) return

  pathPhotoCountDeltas.value = {
    ...pathPhotoCountDeltas.value,
    [path]: (task?.new_photos || 0) - (task?.deleted_photos || 0)
  }
}

const clearPathPhotoCountDelta = (path?: string) => {
  if (!path) {
    pathPhotoCountDeltas.value = {}
    return
  }

  const next = { ...pathPhotoCountDeltas.value }
  delete next[path]
  pathPhotoCountDeltas.value = next
}

// 获取每个路径的照片数量
const loadPathPhotoCounts = async () => {

  if (scanPaths.value.length === 0) return

  try {
    const paths = scanPaths.value.map(p => p.path)
    const res = await photoApi.countByPaths({ paths })
    pathPhotoCounts.value = res.data?.data?.counts || {}
  } catch (error) {
    console.error('Failed to load path photo counts:', error)
  }
}

const loadPathDerivedStatus = async () => {
  if (scanPaths.value.length === 0) return

  try {
    const paths = scanPaths.value.map(p => p.path)
    const res = await photoApi.countDerivedStatusByPaths({ paths })
    pathDerivedStatus.value = res.data?.data?.stats || {}
  } catch (error) {
    console.error('Failed to load path derived status:', error)
  }
}

const getPathAnalysisDerivedState = (path: string) => {
  const stats = pathDerivedStatus.value[path]
  if (!stats || !stats.photo_total) return 'is-idle'
  if ((stats.analyzed_total || 0) <= 0) return 'is-idle'
  if ((stats.analyzed_total || 0) >= stats.photo_total) return 'is-ready'
  return 'is-progress'
}

const getPathAnalysisDerivedTooltip = (path: string) => {
  const stats = pathDerivedStatus.value[path]
  if (!stats || !stats.photo_total) return 'AI 分析：无照片'
  return `AI 分析：${stats.analyzed_total || 0}/${stats.photo_total}`
}

const getPathThumbnailDerivedState = (path: string) => {
  const stats = pathDerivedStatus.value[path]
  if (!stats || !stats.thumbnail_total) return 'is-idle'
  if (stats.thumbnail_failed > 0) return 'is-failed'
  if (stats.thumbnail_ready >= stats.thumbnail_total) return 'is-ready'
  return 'is-progress'
}

const getPathThumbnailDerivedTooltip = (path: string) => {
  const stats = pathDerivedStatus.value[path]
  if (!stats || !stats.thumbnail_total) return '缩略图：无照片'
  if (stats.thumbnail_failed > 0) return `缩略图：失败 ${stats.thumbnail_failed}`
  return `缩略图：${stats.thumbnail_ready}/${stats.thumbnail_total}`
}

const getPathGeocodeDerivedState = (path: string) => {
  const stats = pathDerivedStatus.value[path]
  if (!stats || !stats.geocode_total) return 'is-idle'
  if (stats.geocode_failed > 0) return 'is-failed'
  if (stats.geocode_ready >= stats.geocode_total) return 'is-ready'
  return 'is-progress'
}

const getPathGeocodeDerivedTooltip = (path: string) => {
  const stats = pathDerivedStatus.value[path]
  if (!stats || !stats.geocode_total) return 'GPS：无GPS照片'
  if (stats.geocode_failed > 0) return `GPS：失败 ${stats.geocode_failed}`
  return `GPS：${stats.geocode_ready}/${stats.geocode_total}`
}

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

// 获取文件名
const getFileName = (filePath: string) => {
  return filePath.split('/').pop() || filePath
}

// 格式化日期
const formatDate = (dateStr: string) => {
  try {
    const date = new Date(dateStr)
    return date.toLocaleDateString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit'
    })
  } catch {
    return ''
  }
}

// 获取分数等级样式
const getScoreClass = (score?: number) => {
  if (!score) return 'badge-low'
  if (score >= 8) return 'badge-excellent'
  if (score >= 6) return 'badge-good'
  if (score >= 4) return 'badge-medium'
  return 'badge-low'
}

// 格式化完整日期时间
const formatDateTime = (dateStr: string) => {
  try {
    const date = new Date(dateStr)
    return date.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit'
    })
  } catch {
    return ''
  }
}

// 格式化相对时间
const formatRelativeTime = (dateStr: string) => {
  try {
    const date = new Date(dateStr)
    const now = new Date()
    const diff = now.getTime() - date.getTime()
    const seconds = Math.floor(diff / 1000)
    const minutes = Math.floor(seconds / 60)
    const hours = Math.floor(minutes / 60)
    const days = Math.floor(hours / 24)

    if (seconds < 60) return '刚刚'
    if (minutes < 60) return `${minutes}分钟前`
    if (hours < 24) return `${hours}小时前`
    if (days < 7) return `${days}天前`
    if (days < 30) return `${Math.floor(days / 7)}周前`
    if (days < 365) return `${Math.floor(days / 30)}个月前`
    return `${Math.floor(days / 365)}年前`
  } catch {
    return ''
  }
}

// 添加/编辑路径相关状态
const addPathDialogVisible = ref(false)
const pathBrowserVisible = ref(false)
const savingPath = ref(false)
const validatingPath = ref(false)
const pathValidationResult = ref<{ valid: boolean; error?: string } | null>(null)
const pathForm = ref({ id: '', name: '', path: '', enabled: true, auto_scan_enabled: true })
const isEditPath = ref(false)

// 添加路径
const handleAddPath = () => {
  isEditPath.value = false
  pathForm.value = { id: '', name: '', path: '', enabled: true, auto_scan_enabled: true }
  pathValidationResult.value = null
  addPathDialogVisible.value = true
}

// 编辑路径
const handleEditPath = (path: ScanPathConfig) => {
  isEditPath.value = true
  pathForm.value = { id: path.id, name: path.name, path: path.path, enabled: path.enabled, auto_scan_enabled: path.auto_scan_enabled }
  pathValidationResult.value = null
  addPathDialogVisible.value = true
}

// 验证路径
const handleValidatePath = async () => {
  if (!pathForm.value.path) {
    ElMessage.warning('请输入路径')
    return
  }
  validatingPath.value = true
  try {
    const result = await configApi.validatePath(pathForm.value.path)
    pathValidationResult.value = result
    if (result.valid) {
      ElMessage.success('路径验证成功')
    }
  } catch (error: any) {
    ElMessage.error('路径验证失败')
  } finally {
    validatingPath.value = false
  }
}

// 保存路径
const handleSavePath = async () => {
  if (!pathForm.value.name || !pathForm.value.path) {
    ElMessage.warning('请填写完整信息')
    return
  }
  savingPath.value = true
  try {
    let newPaths: ScanPathConfig[]

    if (isEditPath.value) {
      // 编辑现有路径
      newPaths = scanPaths.value.map(p =>
        p.id === pathForm.value.id
          ? { ...p, name: pathForm.value.name, path: pathForm.value.path, enabled: pathForm.value.enabled, auto_scan_enabled: pathForm.value.auto_scan_enabled }
          : p
      )
    } else {
      // 添加新路径
      const newPath: ScanPathConfig = {
        id: uuidv4(),
        name: pathForm.value.name,
        path: pathForm.value.path,
        is_default: false,
        enabled: pathForm.value.enabled,
        auto_scan_enabled: pathForm.value.auto_scan_enabled,
        created_at: new Date().toISOString(),
      }
      newPaths = [...scanPaths.value, newPath]
    }

    await configApi.updateScanPaths({ paths: newPaths })
    ElMessage.success(isEditPath.value ? '修改成功' : '添加成功')
    addPathDialogVisible.value = false
    await loadScanPaths()
  } catch (error: any) {
    ElMessage.error(isEditPath.value ? '修改失败' : '添加失败')
  } finally {
    savingPath.value = false
  }
}

// 切换路径启用状态
const handleToggleEnabled = async (path: ScanPathConfig) => {
  try {
    await configApi.updateScanPaths({ paths: scanPaths.value })
    ElMessage.success(path.enabled ? '已启用' : '已禁用')
  } catch (error: any) {
    ElMessage.error('操作失败')
    path.enabled = !path.enabled
  }
}

// 删除路径
const handleDeletePath = async (path: ScanPathConfig) => {
  try {
    const paths = scanPaths.value.map(p => p.path)
    const res = await photoApi.countByPaths({ paths: [path.path] })
    const photoCount = res.data?.data?.counts?.[path.path] || 0

    let message = `确定要删除扫描路径「${path.name}」吗？`
    if (photoCount > 0) {
      message += `<br><br><strong style="color: var(--el-color-danger)">警告：该路径下有 ${photoCount} 张照片，删除路径将同时删除这些照片的数据库记录和缩略图！</strong>`
    }

    await ElMessageBox.confirm(message, '确认删除', {
      type: 'warning',
      dangerouslyUseHTMLString: true,
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    })

    const result = await configApi.deleteScanPath(path.id)
    ElMessage.success(result.message || '删除成功')
    await loadScanPaths()
    loadPhotoCounts()
    loadCategoriesAndTags()
    loadPhotos()
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || '删除失败')
    }
  }
}

// 加载系统照片计数（active + excluded，单条 SQL）
const loadPhotoCounts = async () => {
  try {
    const res = await photoApi.getCounts()
    const data = res.data?.data
    systemTotal.value = data?.active_count || 0
    excludedCount.value = data?.excluded_count || 0
  } catch (error: any) {
    console.error('Failed to load photo counts:', error)
  }
}

// 重置搜索条件
const resetSearch = () => {
  searchQuery.value = ''
  filterCategory.value = ''
  filterTag.value = ''
  filterAnalyzed.value = ''
  filterThumbnail.value = ''
  filterGPS.value = ''
  filterStatus.value = ''
  currentPage.value = 1
  loadPhotos()
}

// 加载照片列表
const loadPhotos = async () => {
  loading.value = true
  try {
    const params: any = {
      page: currentPage.value,
      page_size: pageSize.value,
    }

    if (searchQuery.value) {
      params.search = searchQuery.value
    }

    if (filterCategory.value) {
      params.category = filterCategory.value
    }

    if (filterTag.value) {
      params.tag = filterTag.value
    }

    if (filterAnalyzed.value) {
      params.analyzed = filterAnalyzed.value
    }

    if (filterThumbnail.value) {
      params.has_thumbnail = filterThumbnail.value
    }

    if (filterGPS.value) {
      params.has_gps = filterGPS.value
    }

    if (filterStatus.value) {
      params.status = filterStatus.value
    }

    const res = await photoApi.getList(params)
    photos.value = res.data?.data?.items || []
    total.value = res.data?.data?.total || 0
  } catch (error: any) {
    ElMessage.error(error.message || '加载照片列表失败')
  } finally {
    loading.value = false
  }
}

// 搜索处理
const handleSearch = () => {
  // 搜索时清除分类和标签筛选
  filterCategory.value = ''
  filterTag.value = ''
  currentPage.value = 1
  loadPhotos()
}

// 分页处理
const handlePageChange = () => {
  loadPhotos()
}

// 加载自动扫描配置
const loadAutoScanConfig = async () => {
  autoScanConfig.value = await configApi.getAutoScanConfig()
}

// 保存自动扫描配置（开关切换）
const handleAutoScanToggle = async () => {
  try {
    await configApi.updateAutoScanConfig(autoScanConfig.value)
    ElMessage.success(autoScanConfig.value.enabled ? '自动扫描已开启' : '自动扫描已关闭')
  } catch (error: any) {
    ElMessage.error('保存自动扫描配置失败')
  }
}

// 保存自动扫描配置（频率变更）
const handleAutoScanIntervalChange = async () => {
  try {
    await configApi.updateAutoScanConfig(autoScanConfig.value)
    ElMessage.success('扫描频率已更新')
  } catch (error: any) {
    ElMessage.error('保存自动扫描配置失败')
  }
}

// 加载扫描路径
const loadScanPaths = async () => {
  scanPathLoading.value = true
  try {
    const config = await configApi.getScanPaths()
    scanPaths.value = config.paths || []
  } catch (error: any) {
    console.error('Failed to load scan paths:', error)
    ElMessage.error('加载扫描路径失败')
  } finally {
    scanPathLoading.value = false  // 路径列表立即可见
  }
  // 派生状态异步加载，不阻塞路径表显示
  loadPathDerivedStatus().then(() => {
    const counts: Record<string, number> = {}
    for (const [path, status] of Object.entries(pathDerivedStatus.value)) {
      counts[path] = (status as any).photo_total || 0
    }
    pathPhotoCounts.value = counts
  })
}

// 加载分类和标签
const loadCategoriesAndTags = async () => {
  try {
    const [categoriesRes, tagsRes] = await Promise.all([
      photoApi.getCategories(),
      photoApi.getTags()
    ])
    categories.value = categoriesRes.data?.data || []
    tags.value = tagsRes.data?.data || []
  } catch (error: any) {
    console.error('Failed to load categories and tags:', error)
  }
}

// 点击分类筛选
const handleCategoryClick = (value: string) => {
  if (filterCategory.value === value) {
    filterCategory.value = ''
  } else {
    filterCategory.value = value
  }
  // 分类和标签互斥
  filterTag.value = ''
  currentPage.value = 1
  loadPhotos()
}

// 点击标签筛选
const handleTagClick = (value: string) => {
  if (filterTag.value === value) {
    filterTag.value = ''
  } else {
    filterTag.value = value
  }
  // 标签和分类互斥
  filterCategory.value = ''
  currentPage.value = 1
  loadPhotos()
}

// 点击路径名称搜索
const handlePathClick = (row: ScanPathConfig) => {
  // 退出回收站模式
  filterStatus.value = ''
  filterCategory.value = ''
  filterTag.value = ''
  if (searchQuery.value === row.path) {
    searchQuery.value = ''
  } else {
    searchQuery.value = row.path
  }
  currentPage.value = 1
  loadPhotos()
}

const handleRecycleBinClick = () => {
  if (filterStatus.value === 'excluded') {
    // 已经在回收站模式，退出
    filterStatus.value = ''
  } else {
    filterStatus.value = 'excluded'
    searchQuery.value = ''
    filterCategory.value = ''
    filterTag.value = ''
  }
  currentPage.value = 1
  loadPhotos()
}

// 扫描指定路径
// 异步扫描指定路径
const handleScanPath = async (path: ScanPathConfig) => {
  if (!path.enabled) {
    ElMessage.warning('该路径已禁用，无法扫描')
    return
  }

  try {
    scanningPathId.value = path.id
    currentScanPath.value = path.path
    currentScanType.value = 'scan'
    const res = await photoApi.startScan({ path: path.path })
    currentTaskId.value = res.data?.data?.task_id || ''
    currentTaskStatus.value = 'running'
    ElMessage.info(`「${path.name}」扫描任务已启动，正在后台处理...`)

    // 开始轮询进度
    startPollingScanProgress(path.name)
  } catch (error: any) {
    scanningPathId.value = ''
    currentScanPath.value = ''
    currentScanType.value = ''
    currentTaskId.value = ''
    currentTaskStatus.value = ''
    ElMessage.error(error.message || '扫描照片失败')
  }
}

// 异步重建指定路径
const handleRebuildPath = async (path: ScanPathConfig) => {
  if (!path.enabled) {
    ElMessage.warning('该路径已禁用，无法重建')
    return
  }

  try {
    rebuildingPathId.value = path.id
    currentScanPath.value = path.path
    currentScanType.value = 'rebuild'
    const res = await photoApi.startRebuild({ path: path.path })
    currentTaskId.value = res.data?.data?.task_id || ''
    currentTaskStatus.value = 'running'
    ElMessage.info(`「${path.name}」重建任务已启动，正在后台处理...`)

    // 开始轮询进度
    startPollingScanProgress(path.name)
  } catch (error: any) {
    rebuildingPathId.value = ''
    currentScanPath.value = ''
    currentScanType.value = ''
    currentTaskId.value = ''
    currentTaskStatus.value = ''
    ElMessage.error(error.message || '重建照片失败')
  }
}

const clearCurrentTaskState = () => {
  clearPathPhotoCountDelta(currentScanPath.value)
  scanningPathId.value = ""
  rebuildingPathId.value = ""
  currentTaskId.value = ""
  currentTaskStatus.value = ""
  currentScanPath.value = ""
  currentScanType.value = ""
}

const handleStopTask = async (path: ScanPathConfig) => {
  if (!currentTaskId.value) {
    ElMessage.warning('当前没有可停止的任务')
    return
  }

  try {
    await photoApi.stopScanTask(currentTaskId.value)
    currentTaskStatus.value = 'stopping'
    ElMessage.info(`已请求停止「${path.name}」任务，正在等待当前文件处理完成...`)
  } catch (error: any) {
    ElMessage.error(error.message || '停止任务失败')
  }
}

// 轮询扫描进度
let scanProgressTimer: number | null = null

const startPollingScanProgress = (pathName: string) => {
  // 清除之前的定时器
  if (scanProgressTimer) {
    clearInterval(scanProgressTimer)
  }

  // 每 2 秒查询一次进度
  scanProgressTimer = window.setInterval(async () => {
    try {
      const res = await photoApi.getScanTask()
      const { task, is_running } = res.data?.data || {}

      if (!task) {
        // 没有任务信息，停止轮询
        clearInterval(scanProgressTimer!)
        scanProgressTimer = null
        clearCurrentTaskState()
        return
      }

      currentTaskId.value = task.id || ''
      currentTaskStatus.value = task.status || ''
      currentScanPath.value = task.path || currentScanPath.value
      currentScanType.value = task.type || currentScanType.value

      if (is_running) {
        updatePathPhotoCountDelta(task.path || currentScanPath.value, task)
        // 任务进行中，显示进度
        const discovered = task.discovered_files || task.total_files || 0
        const percent = discovered > 0
          ? Math.round((task.processed_files / discovered) * 100)
          : 0
        console.log(`[${pathName}] 进度: ${percent}% (${task.processed_files}/${discovered}) status=${task.status}`)
        await loadPathDerivedStatus()
      } else {
        // 任务完成
        clearInterval(scanProgressTimer!)
        scanProgressTimer = null
        clearCurrentTaskState()

        // 显示结果
        if (task.status === 'stopped') {
          ElMessage.info(`「${pathName}」任务已停止，已处理 ${task.processed_files || 0} 张照片`)
        } else if (task.status === 'interrupted') {
          ElMessage.warning(`「${pathName}」任务已中断，请重新扫描或重建`)
        } else if (task.status === 'failed') {
          ElMessage.error(task.error_message || `「${pathName}」任务失败`)
        } else if (task.type === 'scan') {
          ElMessage.success(`「${pathName}」扫描完成，新增 ${task.new_photos || 0} 张照片`)
        } else {
          ElMessage.success(
            `「${pathName}」重建完成：新增 ${task.new_photos || 0} 张，更新 ${task.updated_photos || 0} 张，删除 ${task.deleted_photos || 0} 张`
          )
        }

        // 刷新数据
        clearPathPhotoCountDelta(task.path || currentScanPath.value)
        await loadPhotos()
        await loadScanPaths()
        await Promise.all([loadPathPhotoCounts(), loadPathDerivedStatus()])
      }
    } catch (error: any) {
      console.error('查询扫描进度失败:', error)
      // 发生错误时继续轮询，不中断
    }
  }, 2000) // 2 秒轮询一次
}

// 清理不存在文件的照片
const handleCleanup = async () => {
  try {
    cleaningUp.value = true
    const res = await photoApi.cleanup()
    const { total_count = 0, deleted_count = 0, skipped_count = 0 } = res.data?.data || {}

    if (deleted_count > 0) {
      ElMessage.success(
        `清理完成：检查了 ${total_count} 张照片，删除了 ${deleted_count} 个不存在文件的记录${skipped_count > 0 ? `，跳过 ${skipped_count} 个` : ''}`
      )
    } else {
      ElMessage.info('清理完成：没有发现文件不存在的照片')
    }

    // Reload photos to update the list
    await loadPhotos()
    // 刷新路径照片数量
    await Promise.all([loadPathPhotoCounts(), loadPathDerivedStatus()])
  } catch (error: any) {
    ElMessage.error(error.message || '清理照片失败')
  } finally {
    cleaningUp.value = false
  }
}

// 跳转到详情页
const gotoDetail = (photoId: number) => {
  const query: any = {
    page: currentPage.value,
    pageSize: pageSize.value
  }

  // 保存筛选条件
  if (filterAnalyzed.value) {
    query.analyzed = filterAnalyzed.value
  }
  if (filterThumbnail.value) {
    query.has_thumbnail = filterThumbnail.value
  }
  if (filterGPS.value) {
    query.has_gps = filterGPS.value
  }
  if (filterStatus.value) {
    query.status = filterStatus.value
  }

  // 保存搜索关键词
  if (searchQuery.value) {
    query.search = searchQuery.value
  }
  if (filterCategory.value) {
    query.category = filterCategory.value
  }
  if (filterTag.value) {
    query.tag = filterTag.value
  }

  router.push({
    path: `/photos/${photoId}`,
    query
  })
}

// 检查是否有正在进行的扫描任务
const checkOngoingScanTask = async () => {
  try {
    const res = await photoApi.getScanTask()
    const { task, is_running } = res.data?.data || {}

    if (is_running && task) {
      // 有正在进行的任务，设置状态
      currentScanPath.value = task.path
      currentScanType.value = task.type
      currentTaskId.value = task.id || ''
      currentTaskStatus.value = task.status || ''

      // 找到对应的路径并设置扫描状态
      const pathConfig = scanPaths.value.find(p => p.path === task.path)
      if (pathConfig) {
        if (task.type === 'scan') {
          scanningPathId.value = pathConfig.id
        } else if (task.type === 'rebuild') {
          rebuildingPathId.value = pathConfig.id
        }
      }

      // 开始轮询进度
      startPollingScanProgress(pathConfig?.name || task.path)
    }
  } catch (error) {
    console.error('Failed to check ongoing scan task:', error)
  }
}

// 判断路径是否正在扫描
const isPathScanning = (path: ScanPathConfig) => {
  return currentScanPath.value === path.path && currentScanType.value === 'scan'
}

// 判断路径是否正在重建
const isPathRebuilding = (path: ScanPathConfig) => {
  return currentScanPath.value === path.path && currentScanType.value === 'rebuild'
}

const isPathTaskActive = (path: ScanPathConfig) => {
  return currentScanPath.value === path.path && ['running', 'stopping'].includes(currentTaskStatus.value)
}

const shouldShowStopButton = (path: ScanPathConfig) => {
  return isPathTaskActive(path)
}

const shouldShowScanButton = (path: ScanPathConfig) => {
  if (shouldShowStopButton(path)) return false
  if (isPathScanning(path)) return true
  if (isPathRebuilding(path)) return false
  return !path.last_scanned_at
}

const shouldShowRebuildButton = (path: ScanPathConfig) => {
  if (shouldShowStopButton(path)) return false
  if (isPathRebuilding(path)) return true
  if (isPathScanning(path)) return false
  return !!path.last_scanned_at
}

onMounted(() => {
  // Load scan paths and auto scan config
  loadScanPaths()
  loadAutoScanConfig()

  // 加载系统照片计数
  loadPhotoCounts()

  // 加载分类和标签
  loadCategoriesAndTags()

  // 检查是否有正在进行的扫描任务
  checkOngoingScanTask()

  // 从 URL 参数恢复状态
  const query = router.currentRoute.value.query

  // 恢复分页参数
  if (query.page) {
    currentPage.value = Number(query.page)
  }
  if (query.pageSize) {
    pageSize.value = Number(query.pageSize)
  }

  // 恢复筛选条件
  if (query.analyzed) {
    filterAnalyzed.value = String(query.analyzed)
  }
  if (query.has_thumbnail) {
    filterThumbnail.value = String(query.has_thumbnail)
  }
  if (query.has_gps) {
    filterGPS.value = String(query.has_gps)
  }
  if (query.status) {
    filterStatus.value = String(query.status)
  }

  // 恢复搜索关键词
  if (query.search) {
    searchQuery.value = String(query.search)
  }
  if (query.category) {
    filterCategory.value = String(query.category)
  }
  if (query.tag) {
    filterTag.value = String(query.tag)
  }

  loadPhotos()
})

// 暴露刷新方法供外部调用
defineExpose({
  refresh: loadPhotos
})
</script>

<style scoped>
/* ============ Photos 页面容器 - WeDance 风格 ============ */
.photos-page {
  padding: var(--spacing-xl);
  background: var(--color-bg-primary);
  min-height: 100vh;
}

.text-gradient {
  color: var(--color-primary);
}

/* ============ 工具栏 ============ */
.toolbar-card {
  margin-bottom: var(--spacing-xl);
  padding: var(--spacing-xl) !important;
}

.filter-group {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 4px;
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border);
  border-radius: 14px;
}

.filter-group :deep(.el-radio-button__inner) {
  min-width: 72px;
  height: 32px;
  padding: 0 16px;
  border: none !important;
  border-radius: 10px !important;
  background: transparent;
  color: var(--color-text-secondary);
  box-shadow: none !important;
  font-size: 13px;
  font-weight: var(--font-weight-medium);
  line-height: 32px;
}

.filter-group :deep(.el-radio-button__original-radio:checked + .el-radio-button__inner) {
  background: var(--color-primary);
  color: #fff;
  box-shadow: 0 8px 18px rgba(0, 184, 148, 0.18) !important;
}

/* ============ 扫描路径卡片 ============ */
.scan-paths-card {
  margin-bottom: var(--spacing-xl);
}

.scan-paths-card :deep(.el-card__body) {
  padding: var(--spacing-md);
}

.scan-paths-card > :deep(.section-header) {
  margin-bottom: var(--spacing-md);
}

.scan-paths-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.auto-scan-inline {
  display: flex;
  align-items: center;
  gap: 8px;
}

.auto-scan-interval-select {
  width: 100px;
}

.count-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 32px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid #d9d9d9;
  background: #fff;
  color: var(--color-text-secondary);
  font-size: 13px;
  font-weight: var(--font-weight-medium);
  line-height: 1;
}

.manage-btn,
.cleanup-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  height: 32px;
  padding-inline: 14px;
  border-radius: 999px;
  font-size: 13px;
  font-weight: var(--font-weight-medium);
}

.manage-btn {
  border-color: var(--color-border) !important;
  color: var(--color-text-secondary) !important;
  background: #fff !important;
}

.manage-btn:hover:not(:disabled) {
  border-color: var(--color-primary) !important;
  color: var(--color-primary) !important;
  background: #fff !important;
}

.cleanup-btn {
  background-color: #fff1f0 !important;
  border-color: #ffa39e !important;
  color: #cf1322 !important;
}

.cleanup-btn:hover:not(:disabled) {
  background-color: #ffccc7 !important;
  border-color: #ff7875 !important;
  color: #a8071a !important;
}

.cleanup-btn:disabled {
  background-color: #f5f5f5 !important;
  border-color: #d9d9d9 !important;
  color: #999 !important;
}

.scan-path-table {
  border-radius: var(--radius-sm);
  overflow: hidden;
}

.scan-path-table :deep(.el-table__header) {
  background: var(--color-bg-secondary);
}

.path-name-cell {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
}

.path-icon {
  font-size: 16px;
  color: var(--color-primary);
}

.path-name {
  font-weight: var(--font-weight-medium);
  color: var(--color-text-primary);
}

.path-name.clickable {
  cursor: pointer;
  transition: all var(--transition-fast);
  padding: 2px 6px;
  border-radius: var(--radius-sm);
}

.path-name.clickable:hover {
  color: var(--color-primary);
  background-color: var(--color-bg-secondary);
}

.path-name.clickable.active {
  color: white;
  background-color: var(--color-primary);
  font-weight: var(--font-weight-semibold);
}

.path-name.clickable.active:hover {
  background-color: var(--color-primary-dark);
}

.path-text {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
  font-family: monospace;
}

.scan-time-cell {
  display: flex;
  align-items: center;
  justify-content: center;
}

.scan-time {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.scan-time.scanning {
  color: var(--color-primary);
}

.scan-time.rebuilding {
  color: var(--color-warning);
}

.scan-time .is-loading {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

/* 照片数量 */
.derived-status-icons {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.derived-status-icon {
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: rgba(80, 80, 80, 0.72);
  color: rgba(255, 255, 255, 0.82);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
}

.derived-status-icon.is-ready {
  background: rgba(103, 194, 58, 0.92);
  color: #fff;
}

.derived-status-icon.is-progress {
  background: rgba(230, 162, 60, 0.92);
  color: #fff;
}

.derived-status-icon.is-failed {
  background: rgba(245, 108, 108, 0.92);
  color: #fff;
}

.derived-status-icon.is-idle {
  background: rgba(80, 80, 80, 0.72);
  color: rgba(255, 255, 255, 0.82);
}

.derived-status-icon :deep(.el-icon) {
  font-size: 10px;
}

.photo-count {
  font-weight: var(--font-weight-medium);
  color: var(--color-text-primary);
}

/* 操作列按钮组 */
.path-action-group {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--spacing-xs);
  width: 100%;
}

.path-action-group :deep(.el-button) {
  min-width: 56px;
  border-radius: var(--radius-sm);
}

/* 扫描按钮浅色样式 */
.scan-btn {
  background-color: #f0f9f4 !important;
  border-color: #a8d5ba !important;
  color: #0d8a4f !important;
}

.scan-btn:hover:not(:disabled) {
  background-color: #e0f2e9 !important;
  border-color: #7bc49a !important;
  color: #0a6b3d !important;
}

.scan-btn:disabled {
  background-color: #f5f5f5 !important;
  border-color: #d9d9d9 !important;
  color: #999 !important;
}

/* 重建按钮样式 */
.rebuild-btn {
  background-color: #fff7e6 !important;
  border-color: #ffd591 !important;
  color: #d46b08 !important;
}

.rebuild-btn:hover:not(:disabled) {
  background-color: #ffe7ba !important;
  border-color: #ffc53d !important;
  color: #ad4e00 !important;
}

.rebuild-btn:disabled {
  background-color: #f5f5f5 !important;
  border-color: #d9d9d9 !important;
  color: #999 !important;
}

/* ============ 照片网格卡片 ============ */
.photos-grid-card :deep(.el-card__body) {
  padding: var(--spacing-xl);
}

.photos-grid-card > :deep(.section-header) {
  margin-bottom: var(--spacing-lg);
}

.photos-list-actions {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  flex-wrap: wrap;
}

.photos-toolbar {
  margin-bottom: var(--spacing-lg);
  padding: var(--spacing-lg);
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
}

/* 空状态提示 */
.empty-hint {
  margin: var(--spacing-md) 0 var(--spacing-lg);
  color: var(--color-text-secondary);
  font-size: var(--font-size-sm);
  text-align: center;
}

/* 搜索区域 */
.search-section {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  margin-bottom: var(--spacing-lg);
  padding-bottom: var(--spacing-lg);
  border-bottom: 1px solid var(--color-border);
}

.search-input-with-btn {
  flex: 1;
}

.search-input-with-btn :deep(.el-input__wrapper) {
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow-sm);
}

.search-input-with-btn :deep(.el-input__wrapper:hover) {
  box-shadow: var(--shadow-md);
}

.search-input-with-btn :deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 2px rgba(0, 184, 148, 0.2);
}

.search-btn {
  background: var(--color-primary);
  border: none;
  border-radius: var(--radius-sm);
  font-weight: var(--font-weight-semibold);
  padding-left: var(--spacing-xl);
  padding-right: var(--spacing-xl);
}

.search-btn:hover {
  background: var(--color-primary-dark);
}

/* 统计信息 */
.photos-stats {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--spacing-xl);
  margin-bottom: var(--spacing-lg);
  padding: var(--spacing-md);
  background: var(--color-bg-secondary);
  border-radius: var(--radius-sm);
  flex-wrap: wrap;
}

.stats-left {
  display: flex;
  align-items: center;
  gap: var(--spacing-xl);
}

.stats-right {
  display: flex;
  align-items: center;
}

.stat-item {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  color: var(--color-text-secondary);
  font-size: var(--font-size-base);
}

.stat-icon {
  font-size: 20px;
  color: var(--color-primary);
}

.stat-text strong {
  color: var(--color-text-primary);
  font-weight: var(--font-weight-bold);
  font-size: var(--font-size-lg);
}

/* ============ 照片网格 ============ */
.photo-grid {
  margin-top: var(--spacing-lg);
  display: grid;
  grid-template-columns: repeat(10, 1fr);
  gap: var(--spacing-md);
}

.photo-col {
  margin-bottom: 0;
}

.photo-card {
  cursor: pointer;
  transition: all var(--transition-base);
}

.photo-card-parallax {
  transition: all var(--transition-base);
}

.photo-image-wrapper {
  position: relative;
  width: 100%;
  aspect-ratio: 1;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--color-bg-secondary);
  box-shadow: var(--shadow-sm);
  transition: all var(--transition-base);
  border: 1px solid var(--color-border);
}

.photo-card:hover .photo-image-wrapper {
  box-shadow: var(--shadow-lg);
  border-color: var(--color-primary);
}

/* ============ 照片选择按钮 ============ */
.photo-select-btn {
  position: absolute;
  top: 8px;
  left: 8px;
  z-index: 10;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.7);
  background: rgba(0, 0, 0, 0.25);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0;
  transition: opacity 0.2s, background 0.2s, border-color 0.2s;
}

.photo-card:hover .photo-select-btn {
  opacity: 1;
}

.photo-select-btn.selected {
  opacity: 1;
  background: #e05a3a;
  border-color: #e05a3a;
  color: #fff;
}

.photo-select-btn.selected .el-icon {
  font-size: 14px;
}

.photo-select-btn:hover:not(.selected) {
  background: rgba(0, 0, 0, 0.4);
  border-color: #fff;
}

.photo-card.is-selected .photo-image-wrapper {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}

.photo-card.is-selected .photo-image-wrapper::after {
  content: '';
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  z-index: 5;
  pointer-events: none;
}

.photo-image {
  width: 100%;
  height: 100%;
  transition: transform var(--transition-base);
}

.photo-card:hover .photo-image {
  transform: scale(1.05);
}

/* 图片加载状态 */
.image-loading,
.image-error {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--spacing-sm);
  color: var(--color-text-tertiary);
  background: var(--color-bg-secondary);
}

.image-loading .el-icon,
.image-error .el-icon {
  font-size: 48px;
}

/* 分析状态徽章 */
.photo-status-icons {
  position: absolute;
  left: 10px;
  bottom: 10px;
  display: flex;
  align-items: center;
  gap: 6px;
  z-index: 3;
  pointer-events: none;
}

.photo-status-icon {
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: rgba(0, 0, 0, 0.32);
  color: rgba(255, 255, 255, 0.7);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.16);
  backdrop-filter: blur(8px);
}

.photo-status-icon.is-ready {
  background: rgba(103, 194, 58, 0.92);
  color: #fff;
}

.photo-status-icon.is-idle {
  background: rgba(80, 80, 80, 0.72);
  color: rgba(255, 255, 255, 0.82);
}

.photo-status-icon :deep(.el-icon) {
  font-size: 10px;
}

.photo-badge {
  position: absolute;
  top: var(--spacing-sm);
  right: var(--spacing-sm);
  padding: 4px 12px;
  border-radius: var(--radius-full);
  background: rgba(255, 255, 255, 0.95);
  color: var(--color-text-primary);
  font-size: var(--font-size-xs);
  font-weight: var(--font-weight-semibold);
  display: flex;
  align-items: center;
  gap: 4px;
  z-index: 2;
  transition: transform var(--transition-base);
  box-shadow: var(--shadow-sm);
}

.photo-card:hover .photo-badge {
  transform: scale(1.05);
}

.badge-excellent {
  background: var(--color-primary);
  color: white;
}

.badge-good {
  background: var(--color-success);
  color: white;
}

.badge-medium {
  background: var(--color-warning);
  color: white;
}

.badge-low {
  background: var(--color-error);
  color: white;
}

.badge-unanalyzed {
  background: var(--color-info);
  color: white;
}

/* 悬停信息遮罩 */
.photo-overlay {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  background: linear-gradient(to top, rgba(0, 0, 0, 0.7), transparent);
  padding: var(--spacing-md);
  transform: translateY(100%);
  transition: transform var(--transition-base);
  z-index: 1;
}

.photo-card:hover .photo-overlay {
  transform: translateY(0);
}

.photo-info {
  color: white;
}

.photo-name {
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-semibold);
  margin-bottom: var(--spacing-sm);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.photo-meta {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
  font-size: var(--font-size-xs);
  color: rgba(255, 255, 255, 0.9);
}

.meta-item {
  display: flex;
  align-items: center;
  gap: 4px;
}

/* ============ 分页 ============ */
.pagination-wrapper {
  display: flex;
  justify-content: center;
  margin-top: var(--spacing-xl);
  padding-top: var(--spacing-lg);
  border-top: 1px solid var(--color-border);
}

.pagination-wrapper :deep(.el-pagination) {
  gap: var(--spacing-sm);
}

.pagination-wrapper :deep(.el-pager li) {
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.pagination-wrapper :deep(.el-pager li:hover) {
  background: var(--color-primary);
  color: white;
}

.pagination-wrapper :deep(.el-pager li.is-active) {
  background: var(--color-primary);
  color: white;
}

/* ============ 响应式设计 ============ */
@media (max-width: 1400px) {
  .photo-grid {
    grid-template-columns: repeat(8, 1fr);
  }
}

@media (max-width: 1200px) {
  .photos-page {
    padding: var(--spacing-lg);
  }

  .photo-grid {
    grid-template-columns: repeat(6, 1fr);
  }
}

@media (max-width: 992px) {
  .photo-grid {
    grid-template-columns: repeat(5, 1fr);
  }
}

@media (max-width: 768px) {
  .photos-page {
    padding: var(--spacing-md);
  }


  .scan-paths-card {
    padding: var(--spacing-md) !important;
  }
}

@media (max-width: 480px) {

  .photo-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

/* 分类和标签筛选 */
.filter-section {
  display: flex;
  align-items: flex-start;
  gap: var(--spacing-md);
  margin-bottom: var(--spacing-md);
  padding: var(--spacing-md) 0;
  border-bottom: 1px solid var(--color-border);
}

.filter-label {
  display: flex;
  align-items: center;
  gap: var(--spacing-xs);
  color: var(--color-text-secondary);
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-medium);
  white-space: nowrap;
  padding-top: 6px;
}

.filter-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--spacing-sm);
  flex: 1;
}

.filter-tag {
  cursor: pointer;
  transition: all 0.2s ease;
}

.filter-tag:hover {
  transform: translateY(-2px);
  box-shadow: 0 4px 8px rgba(0, 0, 0, 0.15);
}

/* 折叠按钮 */
.collapse-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  color: var(--color-text-secondary);
  font-size: var(--font-size-sm);
  padding: 4px 8px;
  height: auto;
  margin-left: var(--spacing-xs);
}

.collapse-btn:hover {
  color: var(--color-primary);
}

.collapse-icon {
  font-size: 12px;
}

.recycle-bin-row {
  display: flex;
  align-items: center;
  padding: 8px 12px;
  border-top: 1px solid var(--el-border-color-lighter);
  cursor: pointer;
  transition: background-color var(--transition-fast);
  font-size: var(--font-size-sm);
}

.recycle-bin-row:hover {
  background-color: var(--color-bg-secondary);
}

.recycle-bin-row.active {
  background-color: var(--el-color-danger-light-9);
}

.recycle-bin-cell {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
}

.recycle-bin-cell.name {
  min-width: 120px;
}

.recycle-bin-cell.path {
  flex: 1;
  color: var(--color-text-secondary);
}

.recycle-bin-cell.count {
  width: 80px;
  justify-content: center;
  color: var(--el-color-danger);
  font-weight: var(--font-weight-medium);
}

/* ============ 添加路径对话框 ============ */
.input-with-button {
  display: flex;
  gap: 12px;
  align-items: center;
  width: 100%;
}

.input-with-button .el-input {
  flex: 1;
  min-width: 0;
}

.input-with-button .el-button {
  flex-shrink: 0;
}

.validation-result {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  font-size: 14px;
}

.validation-result.valid {
  color: var(--color-success);
}

.validation-result.invalid {
  color: var(--color-error);
}

/* ============ 选中照片悬浮工具栏 ============ */
.selection-toolbar {
  position: fixed;
  bottom: 32px;
  right: 32px;
  z-index: 100;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 20px;
  background: #1a1a2e;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: var(--radius-lg, 12px);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.35);
}

.selection-count {
  font-size: 14px;
  font-weight: var(--font-weight-medium, 500);
  color: #fff;
  white-space: nowrap;
}

.float-toolbar-enter-active,
.float-toolbar-leave-active {
  transition: all 0.3s ease;
}

.float-toolbar-enter-from,
.float-toolbar-leave-to {
  opacity: 0;
  transform: translateY(16px);
}

</style>
