<template>
  <div class="photos-page">
    <!-- 页面标题 -->
    <div class="page-header animate-fade-in">
      <h1 class="page-title">
        <span class="text-gradient">照片管理</span>
      </h1>
      <p class="page-subtitle">浏览和管理您的照片集合</p>
    </div>

    <!-- 扫描路径列表 -->
    <div class="scan-paths-card modern-card animate-fade-in" v-loading="scanPathLoading">
      <div class="scan-paths-header">
        <div class="scan-paths-title">
          <el-icon class="title-icon"><FolderOpened /></el-icon>
          <span>扫描路径</span>
          <el-tag type="info" size="small" effect="plain" class="count-tag">{{ scanPaths.length }}</el-tag>
        </div>
        <div class="scan-paths-actions">
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
          <el-link type="primary" @click="goToConfig" class="manage-link">
            <el-icon><Setting /></el-icon>
            管理路径
          </el-link>
        </div>
      </div>

      <el-table
        :data="scanPaths"
        style="width: 100%"
        class="scan-path-table"
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
              <el-tag v-if="row.is_default" type="success" size="small" effect="light">默认</el-tag>
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
            <span class="photo-count">{{ pathPhotoCounts[row.path] || 0 }}</span>
          </template>
        </el-table-column>

        <el-table-column prop="enabled" label="状态" width="80" align="center">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small" effect="light">
              {{ row.enabled ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>

        <el-table-column prop="last_scanned_at" label="上次扫描" width="140" align="center">
          <template #default="{ row }">
            <div class="scan-time-cell">
              <!-- 扫描中状态 -->
              <template v-if="isPathScanning(row)">
                <el-tag type="primary" size="small" effect="light">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  扫描中...
                </el-tag>
              </template>
              <!-- 重建中状态 -->
              <template v-else-if="isPathRebuilding(row)">
                <el-tag type="warning" size="small" effect="light">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  重建中...
                </el-tag>
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

        <el-table-column label="操作" width="150" align="center">
          <template #default="{ row }">
            <el-button-group>
              <el-button
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
            </el-button-group>
          </template>
        </el-table-column>
      </el-table>

      <el-empty v-if="scanPaths.length === 0 && !scanPathLoading" description="暂无扫描路径" :image-size="80">
        <el-button type="primary" @click="goToConfig">
          <el-icon><Setting /></el-icon>
          前往配置
        </el-button>
      </el-empty>
    </div>

    <!-- 照片网格 -->
    <div class="photos-grid-card modern-card animate-fade-in" v-loading="loading">
      <!-- 空状态：系统中没有照片 -->
      <el-empty v-if="!photos.length && !loading && systemTotal === 0" description="暂无照片" :image-size="120">
        <el-button type="primary" @click="goToConfig">
          <el-icon><Setting /></el-icon>
          前往配置添加路径
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
              :type="searchQuery === category ? 'primary' : 'info'"
              class="filter-tag"
              @click="handleFilterClick(category)"
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
              :type="searchQuery === tag ? 'primary' : 'info'"
              class="filter-tag"
              @click="handleFilterClick(tag)"
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

        <!-- 统计信息和筛选 -->
        <div class="photos-stats">
          <div class="stats-left">
            <div class="stat-item">
              <el-icon class="stat-icon"><Picture /></el-icon>
              <span class="stat-text">共 <strong>{{ total }}</strong> 张照片</span>
            </div>
            <div class="stat-item" v-if="filterAnalyzed">
              <el-icon class="stat-icon"><Filter /></el-icon>
              <span class="stat-text">筛选结果</span>
            </div>
          </div>
          <div class="stats-right">
            <el-radio-group v-model="filterAnalyzed" @change="handleSearch" size="default" class="filter-group">
              <el-radio-button label="">全部</el-radio-button>
              <el-radio-button label="true">已分析</el-radio-button>
              <el-radio-button label="false">未分析</el-radio-button>
            </el-radio-group>
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
              @click="gotoDetail(photo.id)"
            >
              <div class="photo-image-wrapper">
                <el-image
                  :src="getPhotoThumbnailUrl(photo.id)"
                  :preview-src-list="[getPhotoUrl(photo.id)]"
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
                <div class="photo-badge badge-unanalyzed" v-else>
                  <el-icon><QuestionFilled /></el-icon>
                  <span>未分析</span>
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
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { ArrowDown, ArrowUp } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { photoApi } from '@/api/photo'
import { configApi, type ScanPathConfig } from '@/api/config'
import type { Photo } from '@/types/photo'

const router = useRouter()

const photos = ref<Photo[]>([])
const loading = ref(false)
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const systemTotal = ref(0) // 系统中所有照片的总数（不带筛选）
const searchQuery = ref('')
const filterAnalyzed = ref('')
const scanPaths = ref<ScanPathConfig[]>([])
const scanPathLoading = ref(false)
const scanningPathId = ref<string>('')
const rebuildingPathId = ref<string>('')
const currentScanPath = ref<string>('') // 当前正在扫描的路径
const currentScanType = ref<'scan' | 'rebuild' | ''>('') // 当前扫描类型
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

// 获取照片缩略图 URL
const getPhotoThumbnailUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return `${baseUrl}/photos/${photoId}/thumbnail`
}

// 获取照片原图 URL（用于预览）
const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return `${baseUrl}/photos/${photoId}/image`
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

// 前往配置页面
const goToConfig = () => {
  router.push('/config')
}

// 加载系统总照片数（不带任何筛选）
const loadSystemTotal = async () => {
  try {
    const res = await photoApi.getList({ page_size: 1 })
    systemTotal.value = res.data?.data?.total || 0
  } catch (error: any) {
    console.error('Failed to load system total:', error)
  }
}

// 重置搜索条件
const resetSearch = () => {
  searchQuery.value = ''
  filterAnalyzed.value = ''
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

    if (filterAnalyzed.value) {
      params.analyzed = filterAnalyzed.value === 'true'
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
  currentPage.value = 1
  loadPhotos()
}

// 分页处理
const handlePageChange = () => {
  loadPhotos()
}

// 加载扫描路径
const loadScanPaths = async () => {
  scanPathLoading.value = true
  try {
    const config = await configApi.getScanPaths()
    scanPaths.value = config.paths || []
    // 加载每个路径的照片数量
    await loadPathPhotoCounts()
  } catch (error: any) {
    console.error('Failed to load scan paths:', error)
    ElMessage.error('加载扫描路径失败')
  } finally {
    scanPathLoading.value = false
  }
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

// 点击分类/标签筛选
const handleFilterClick = (value: string) => {
  if (searchQuery.value === value) {
    // 如果已经选中了，取消筛选
    searchQuery.value = ''
  } else {
    searchQuery.value = value
  }
  currentPage.value = 1
  loadPhotos()
}

// 点击路径名称搜索
const handlePathClick = (row: ScanPathConfig) => {
  if (searchQuery.value === row.path) {
    // 如果已经选中了，取消筛选
    searchQuery.value = ''
  } else {
    searchQuery.value = row.path
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
    ElMessage.info(`「${path.name}」扫描任务已启动，正在后台处理...`)

    // 开始轮询进度
    startPollingScanProgress(path.name)
  } catch (error: any) {
    scanningPathId.value = ''
    currentScanPath.value = ''
    currentScanType.value = ''
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
    ElMessage.info(`「${path.name}」重建任务已启动，正在后台处理...`)

    // 开始轮询进度
    startPollingScanProgress(path.name)
  } catch (error: any) {
    rebuildingPathId.value = ''
    currentScanPath.value = ''
    currentScanType.value = ''
    ElMessage.error(error.message || '重建照片失败')
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
        scanningPathId.value = ''
        rebuildingPathId.value = ''
        currentScanPath.value = ''
        currentScanType.value = ''
        return
      }

      if (is_running) {
        // 任务进行中，显示进度
        const percent = task.total_files > 0
          ? Math.round((task.processed_files / task.total_files) * 100)
          : 0
        console.log(`[${pathName}] 进度: ${percent}% (${task.processed_files}/${task.total_files})`)
      } else {
        // 任务完成
        clearInterval(scanProgressTimer!)
        scanProgressTimer = null
        scanningPathId.value = ''
        rebuildingPathId.value = ''
        currentScanPath.value = ''
        currentScanType.value = ''

        // 显示结果
        if (task.type === 'scan') {
          ElMessage.success(`「${pathName}」扫描完成，新增 ${task.new_photos || 0} 张照片`)
        } else {
          ElMessage.success(
            `「${pathName}」重建完成：新增 ${task.new_photos || 0} 张，更新 ${task.updated_photos || 0} 张`
          )
        }

        // 刷新数据
        await loadPhotos()
        await loadScanPaths()
        await loadPathPhotoCounts()
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
    await loadPathPhotoCounts()
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

  // 保存搜索关键词
  if (searchQuery.value) {
    query.search = searchQuery.value
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

onMounted(() => {
  // Load scan paths first
  loadScanPaths()

  // 加载系统总照片数
  loadSystemTotal()

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

  // 恢复搜索关键词
  if (query.search) {
    searchQuery.value = String(query.search)
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
  padding: var(--spacing-2xl);
  background: var(--color-bg-primary);
  min-height: 100vh;
}

/* ============ 页面标题 ============ */
.page-header {
  margin-bottom: var(--spacing-2xl);
}

.page-title {
  font-size: var(--font-size-4xl);
  font-weight: var(--font-weight-bold);
  margin-bottom: var(--spacing-sm);
  line-height: 1.2;
  color: var(--color-text-primary);
}

.page-subtitle {
  font-size: var(--font-size-lg);
  color: var(--color-text-secondary);
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
  display: flex;
  gap: var(--spacing-sm);
}

.filter-group :deep(.el-radio-button__inner) {
  border-radius: var(--radius-sm);
}

/* ============ 扫描路径卡片 ============ */
.scan-paths-card {
  margin-bottom: var(--spacing-xl);
  padding: var(--spacing-lg) !important;
}

.scan-paths-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--spacing-md);
  padding-bottom: var(--spacing-md);
  border-bottom: 1px solid var(--color-border);
}

.scan-paths-title {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  font-size: var(--font-size-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
}

.scan-paths-title .title-icon {
  font-size: 20px;
  color: var(--color-primary);
}

.count-tag {
  margin-left: var(--spacing-xs);
}

.manage-link {
  display: flex;
  align-items: center;
  gap: var(--spacing-xs);
  font-size: var(--font-size-sm);
}

.scan-paths-actions {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
}

/* 清理按钮样式 */
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
}

/* 照片数量 */
.photo-count {
  font-weight: var(--font-weight-medium);
  color: var(--color-text-primary);
}

/* 按钮组间隙 */
:deep(.el-button-group) {
  display: flex;
  gap: var(--spacing-xs);
}

:deep(.el-button-group .el-button) {
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
.photos-grid-card {
  padding: var(--spacing-xl) !important;
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

  .page-title {
    font-size: var(--font-size-2xl);
  }

  .scan-paths-card {
    padding: var(--spacing-md) !important;
  }

  .scan-paths-header {
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-sm);
  }

  .photos-grid-card {
    padding: var(--spacing-lg) !important;
  }

  .search-section {
    flex-direction: column;
    align-items: stretch;
  }

  .search-btn {
    width: 100%;
  }

  .photos-stats {
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-md);
  }

  .stats-left {
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-sm);
    width: 100%;
  }

  .stats-right {
    width: 100%;
  }

  .filter-group {
    width: 100%;
  }

  .filter-group :deep(.el-radio-button) {
    flex: 1;
  }

  .filter-group :deep(.el-radio-button__inner) {
    width: 100%;
  }

  .photo-grid {
    grid-template-columns: repeat(3, 1fr);
  }

  .pagination-wrapper {
    overflow-x: auto;
  }

  .pagination-wrapper :deep(.el-pagination) {
    flex-wrap: nowrap;
  }
}

@media (max-width: 480px) {
  .page-title {
    font-size: var(--font-size-xl);
  }

  .page-subtitle {
    font-size: var(--font-size-base);
  }

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

</style>
