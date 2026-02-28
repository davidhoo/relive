<template>
  <div class="photos-page">
    <!-- 工具栏 -->
    <el-card shadow="never" style="margin-bottom: 20px">
      <el-row :gutter="20">
        <el-col :span="8">
          <el-input
            v-model="searchQuery"
            placeholder="搜索照片 (路径、设备ID、标签...)"
            clearable
            @clear="handleSearch"
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
        </el-col>
        <el-col :span="16">
          <div style="display: flex; justify-content: space-between; align-items: center">
            <div>
              <el-radio-group v-model="filterAnalyzed" @change="handleSearch">
                <el-radio-button label="">全部</el-radio-button>
                <el-radio-button label="true">已分析</el-radio-button>
                <el-radio-button label="false">未分析</el-radio-button>
              </el-radio-group>
            </div>
            <div>
              <el-button type="primary" @click="handleScan">
                <el-icon><FolderOpened /></el-icon>
                扫描照片
              </el-button>
            </div>
          </div>
        </el-col>
      </el-row>
    </el-card>

    <!-- 照片网格 -->
    <el-card shadow="never" v-loading="loading">
      <el-empty v-if="!photos.length && !loading" description="暂无照片" />
      <el-row :gutter="16" v-else>
        <el-col
          :span="4"
          v-for="photo in photos"
          :key="photo.id"
          style="margin-bottom: 16px"
        >
          <div class="photo-card" @click="gotoDetail(photo.id)">
            <el-image
              :src="getPhotoUrl(photo.id)"
              :preview-src-list="[getPhotoUrl(photo.id)]"
              fit="cover"
              class="photo-image"
            />
            <div class="photo-info">
              <div class="photo-score" v-if="photo.is_analyzed">
                <el-tag size="small" type="success">
                  {{ photo.overall_score?.toFixed(1) }}
                </el-tag>
              </div>
              <div class="photo-score" v-else>
                <el-tag size="small" type="info">未分析</el-tag>
              </div>
              <div class="photo-meta">
                <el-tooltip :content="photo.file_path" placement="top">
                  <span class="photo-name">{{ getFileName(photo.file_path) }}</span>
                </el-tooltip>
              </div>
            </div>
          </div>
        </el-col>
      </el-row>

      <!-- 分页 -->
      <div style="margin-top: 20px; text-align: center">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handlePageChange"
          @current-change="handlePageChange"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { photoApi } from '@/api/photo'
import type { Photo } from '@/types/photo'

const router = useRouter()

const photos = ref<Photo[]>([])
const loading = ref(false)
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const searchQuery = ref('')
const filterAnalyzed = ref('')

// 获取照片 URL
const getPhotoUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return `${baseUrl}/photos/${photoId}/image`
}

// 获取文件名
const getFileName = (filePath: string) => {
  return filePath.split('/').pop() || filePath
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
      params.is_analyzed = filterAnalyzed.value === 'true'
    }

    const res = await photoApi.getList(params)
    photos.value = res.data?.items || []
    total.value = res.data?.total || 0
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

// 扫描照片
const handleScan = async () => {
  try {
    loading.value = true
    const res = await photoApi.scan()
    ElMessage.success(`扫描完成，新增 ${res.data?.new_count || 0} 张照片`)
    await loadPhotos()
  } catch (error: any) {
    ElMessage.error(error.message || '扫描照片失败')
  } finally {
    loading.value = false
  }
}

// 跳转到详情页
const gotoDetail = (photoId: number) => {
  router.push(`/photos/${photoId}`)
}

onMounted(() => {
  loadPhotos()
})
</script>

<style scoped>
.photos-page {
  padding: 20px;
}

.photo-card {
  cursor: pointer;
  border-radius: 8px;
  overflow: hidden;
  transition: all 0.3s;
  background: #fff;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
}

.photo-card:hover {
  transform: translateY(-4px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
}

.photo-image {
  width: 100%;
  height: 200px;
  display: block;
}

.photo-info {
  padding: 8px;
}

.photo-score {
  margin-bottom: 4px;
}

.photo-meta {
  font-size: 12px;
  color: #606266;
}

.photo-name {
  display: block;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
