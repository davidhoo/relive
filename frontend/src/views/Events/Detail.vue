<template>
  <div class="event-detail" v-loading="loading">
    <template v-if="event">
      <div class="detail-header">
        <el-button link @click="goBack" class="back-link">
          <el-icon><ArrowLeft /></el-icon>
          返回事件列表
        </el-button>
        <h2 class="detail-title">
          {{ event.primary_tag || event.primary_category || '事件详情' }}
          <span class="detail-date">{{ formatDateRange(event.start_time, event.end_time) }}</span>
        </h2>
      </div>

      <el-card shadow="never" class="summary-card">
        <el-descriptions :column="3" border>
          <el-descriptions-item label="时间范围">{{ formatDateRange(event.start_time, event.end_time) }}</el-descriptions-item>
          <el-descriptions-item label="持续时间">{{ formatDuration(event.duration_hours) }}</el-descriptions-item>
          <el-descriptions-item label="照片数">{{ event.photo_count }}</el-descriptions-item>
          <el-descriptions-item label="位置">{{ event.location || '-' }}</el-descriptions-item>
          <el-descriptions-item label="主分类">
            <el-tag v-if="event.primary_category" size="small" type="info">{{ event.primary_category }}</el-tag>
            <span v-else>-</span>
          </el-descriptions-item>
          <el-descriptions-item label="主标签">
            <el-tag v-if="event.primary_tag" size="small">{{ event.primary_tag }}</el-tag>
            <span v-else>-</span>
          </el-descriptions-item>
          <el-descriptions-item label="事件评分">{{ event.event_score.toFixed(1) }}</el-descriptions-item>
          <el-descriptions-item label="展示次数">{{ event.display_count }}</el-descriptions-item>
          <el-descriptions-item label="上次展示">{{ event.last_displayed_at ? formatTime(event.last_displayed_at) : '从未展示' }}</el-descriptions-item>
        </el-descriptions>
      </el-card>

      <div class="photos-section">
        <h3 class="section-title">照片列表</h3>
        <div v-if="photos.length > 0" class="photo-grid">
          <div
            v-for="photo in photos"
            :key="photo.id"
            class="photo-card"
            @click="router.push(`/photos/${photo.id}`)"
          >
            <el-image
              :src="getThumbnailUrl(photo.id, photo.updated_at)"
              fit="cover"
              class="photo-img"
            >
              <template #error>
                <div class="photo-placeholder">
                  <el-icon :size="24"><Picture /></el-icon>
                </div>
              </template>
            </el-image>
            <div class="photo-caption">
              {{ photo.file_name || '' }}
            </div>
          </div>
        </div>
        <el-empty v-else description="该事件暂无照片" />

        <div v-if="photoTotal > photoPageSize" class="pagination-wrapper">
          <el-pagination
            v-model:current-page="photoPage"
            :page-size="photoPageSize"
            :total="photoTotal"
            layout="total, prev, pager, next"
            @current-change="fetchDetail"
          />
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, Picture } from '@element-plus/icons-vue'
import { eventApi } from '@/api/event'
import type { Event } from '@/types/event'
import type { Photo } from '@/types/photo'

const route = useRoute()
const router = useRouter()

const loading = ref(false)
const event = ref<Event | null>(null)
const photos = ref<Photo[]>([])
const photoPage = ref(1)
const photoPageSize = 50
const photoTotal = ref(0)

const goBack = () => {
  if (window.history.length > 1) {
    router.back()
  } else {
    router.push('/events')
  }
}

const fetchDetail = async () => {
  const id = Number(route.params.id)
  if (!id) return
  loading.value = true
  try {
    const { data: res } = await eventApi.getDetail(id, photoPage.value, photoPageSize)
    if (res.success && res.data) {
      event.value = res.data.event
      photos.value = res.data.photos?.items || []
      photoTotal.value = res.data.photos?.total || 0
    }
  } catch {
    ElMessage.error('获取事件详情失败')
  } finally {
    loading.value = false
  }
}

const getThumbnailUrl = (photoId: number, version?: string) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  const params = new URLSearchParams()
  if (version) params.set('v', version)
  const query = params.toString()
  return `${baseUrl}/photos/${photoId}/thumbnail${query ? `?${query}` : ''}`
}

const formatDateRange = (start: string, end: string) => {
  const s = new Date(start)
  const e = new Date(end)
  const fmt = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  const fmtTime = (d: Date) => `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  const sDate = fmt(s)
  const eDate = fmt(e)
  if (sDate === eDate) {
    return `${sDate} ${fmtTime(s)} - ${fmtTime(e)}`
  }
  return `${sDate} - ${eDate}`
}

const formatDuration = (hours: number) => {
  if (hours < 1) return `${Math.round(hours * 60)} 分钟`
  if (hours < 24) return `${hours.toFixed(1)} 小时`
  return `${(hours / 24).toFixed(1)} 天`
}

const formatTime = (t: string) => {
  const d = new Date(t)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

onMounted(() => {
  fetchDetail()
})
</script>

<style scoped>
.event-detail {
  padding: var(--spacing-xl);
}

.detail-header {
  margin-bottom: var(--spacing-xl);
}

.back-link {
  margin-bottom: var(--spacing-md);
  font-size: var(--font-size-base);
  color: var(--color-text-secondary);
}

.back-link:hover {
  color: var(--color-primary);
}

.detail-title {
  font-size: var(--font-size-3xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
  display: flex;
  align-items: baseline;
  gap: var(--spacing-md);
}

.detail-date {
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-normal);
  color: var(--color-text-secondary);
}

.summary-card {
  margin-bottom: var(--spacing-xl);
}

.photos-section {
  margin-top: var(--spacing-lg);
}

.section-title {
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin-bottom: var(--spacing-lg);
}

.photo-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: var(--spacing-md);
}

.photo-card {
  border-radius: var(--radius-md);
  overflow: hidden;
  cursor: pointer;
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border);
  transition: all var(--transition-base);
}

.photo-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
  border-color: var(--color-primary);
}

.photo-img {
  width: 100%;
  aspect-ratio: 1;
}

.photo-placeholder {
  width: 100%;
  aspect-ratio: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-tertiary);
  background: var(--color-bg-tertiary);
}

.photo-caption {
  padding: var(--spacing-xs) var(--spacing-sm);
  font-size: var(--font-size-xs);
  color: var(--color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pagination-wrapper {
  margin-top: var(--spacing-xl);
  display: flex;
  justify-content: center;
}

@media (max-width: 768px) {
  .event-detail {
    padding: var(--spacing-md);
  }

  .detail-title {
    flex-direction: column;
    gap: var(--spacing-xs);
    font-size: var(--font-size-2xl);
  }

  .photo-grid {
    grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
  }
}
</style>
