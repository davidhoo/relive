<template>
  <div class="events-page">
    <PageHeader title="事件浏览" subtitle="基于时空聚类的照片事件" :gradient="true" />

    <div v-if="loading" v-loading="true" class="loading-placeholder" />

    <template v-else-if="events.length > 0">
      <div class="event-grid">
        <div
          v-for="event in events"
          :key="event.id"
          class="event-card"
          @click="router.push(`/events/${event.id}`)"
        >
          <div class="event-cover">
            <el-image
              v-if="event.cover_photo_id"
              :src="getThumbnailUrl(event.cover_photo_id)"
              fit="cover"
              class="cover-img"
            >
              <template #error>
                <div class="cover-placeholder">
                  <el-icon :size="32"><Picture /></el-icon>
                </div>
              </template>
            </el-image>
            <div v-else class="cover-placeholder">
              <el-icon :size="32"><Picture /></el-icon>
            </div>
            <div class="event-photo-count">
              <el-icon><PictureFilled /></el-icon>
              {{ event.photo_count }}
            </div>
          </div>
          <div class="event-info">
            <div class="event-date">{{ formatDateRange(event.start_time, event.end_time) }}</div>
            <div v-if="event.location" class="event-location">
              <el-icon :size="12"><Location /></el-icon>
              {{ event.location }}
            </div>
            <div class="event-tags">
              <el-tag v-if="event.primary_category" size="small" type="info">{{ event.primary_category }}</el-tag>
              <el-tag v-if="event.primary_tag" size="small">{{ event.primary_tag }}</el-tag>
            </div>
            <div class="event-meta">
              <span class="event-score" :title="`事件评分 ${event.event_score.toFixed(1)}`">
                <el-icon :size="12"><Star /></el-icon>
                {{ event.event_score.toFixed(1) }}
              </span>
              <span v-if="event.display_count > 0" class="event-display-count">
                已展示 {{ event.display_count }} 次
              </span>
            </div>
          </div>
        </div>
      </div>

      <div class="pagination-wrapper">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 40, 60]"
          layout="total, sizes, prev, pager, next"
          @current-change="fetchEvents"
          @size-change="fetchEvents"
        />
      </div>
    </template>

    <el-empty v-else description="暂无事件数据，请先运行事件聚类" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Location, Picture, PictureFilled, Star } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import { eventApi } from '@/api/event'
import type { Event } from '@/types/event'

const route = useRoute()
const router = useRouter()

const events = ref<Event[]>([])
const loading = ref(false)
const page = ref(Number(route.query.page) || 1)
const pageSize = ref(Number(route.query.pageSize) || 20)
const total = ref(0)

const syncQuery = () => {
  router.replace({ query: { ...route.query, page: String(page.value), pageSize: String(pageSize.value) } })
}

const fetchEvents = async () => {
  loading.value = true
  syncQuery()
  try {
    const { data: res } = await eventApi.getList(page.value, pageSize.value)
    if (res.success && res.data) {
      events.value = res.data.items || []
      total.value = res.data.total
    }
  } catch {
    ElMessage.error('获取事件列表失败')
  } finally {
    loading.value = false
  }
}

const getThumbnailUrl = (photoId: number) => {
  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'
  return `${baseUrl}/photos/${photoId}/thumbnail`
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

onMounted(() => {
  fetchEvents()
})
</script>

<style scoped>
.events-page {
  padding: var(--spacing-xl);
}

.loading-placeholder {
  min-height: 300px;
}

.event-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: var(--spacing-lg);
}

.event-card {
  background: var(--color-bg-secondary);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
  overflow: hidden;
  cursor: pointer;
  transition: all var(--transition-base);
}

.event-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
  border-color: var(--color-primary);
}

.event-cover {
  position: relative;
  width: 100%;
  aspect-ratio: 16 / 10;
  overflow: hidden;
  background: var(--color-bg-tertiary);
}

.cover-img {
  width: 100%;
  height: 100%;
}

.cover-placeholder {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-tertiary);
}

.event-photo-count {
  position: absolute;
  bottom: 8px;
  right: 8px;
  background: rgba(0, 0, 0, 0.6);
  color: #fff;
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  font-size: var(--font-size-sm);
  display: flex;
  align-items: center;
  gap: 4px;
}

.event-info {
  padding: var(--spacing-md);
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
}

.event-date {
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
}

.event-location {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
  display: flex;
  align-items: center;
  gap: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.event-tags {
  display: flex;
  gap: var(--spacing-xs);
  flex-wrap: wrap;
}

.event-meta {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
  margin-top: var(--spacing-xs);
}

.event-score {
  display: flex;
  align-items: center;
  gap: 2px;
  color: var(--color-warning);
}

.event-display-count {
  color: var(--color-text-tertiary);
}

.pagination-wrapper {
  margin-top: var(--spacing-xl);
  display: flex;
  justify-content: center;
}

@media (max-width: 768px) {
  .events-page {
    padding: var(--spacing-md);
  }

  .event-grid {
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: var(--spacing-md);
  }
}
</style>
