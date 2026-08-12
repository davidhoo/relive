<template>
  <div class="face-quality-page">
    <div class="page-header">
      <div class="page-title">
        <h2>人脸质检</h2>
        <span v-if="stats" class="page-subtitle">
          待审核 {{ stats.pending_review }} · 自动隔离 {{ stats.auto_excluded }} · 已确认 {{ stats.manual_confirmed }}
        </span>
      </div>
      <div class="header-actions">
        <el-select v-model="filters.rule_version" placeholder="规则版本" clearable size="small" style="width: 130px" @change="reload">
          <el-option v-for="rv in ruleVersionOptions" :key="rv" :label="rv" :value="rv" />
        </el-select>
        <el-button size="small" plain @click="reload">刷新</el-button>
        <el-button size="small" type="warning" @click="openRestoreDialog">按规则版本恢复</el-button>
      </div>
    </div>

    <el-tabs v-model="activeTab" class="quality-tabs" @tab-change="onTabChange">
      <el-tab-pane label="待人工审核" name="pending_review" />
      <el-tab-pane label="自动隔离" name="auto_excluded" />
      <el-tab-pane label="已人工确认" name="manual_confirmed" />
    </el-tabs>

    <div class="filter-bar">
      <el-select v-model="filters.reason" placeholder="原因" clearable size="small" style="width: 140px" @change="reload">
        <el-option label="非人脸" value="non_face" />
        <el-option label="低质量" value="low_quality" />
      </el-select>
      <el-select v-model="filters.source" placeholder="来源" clearable size="small" style="width: 120px" @change="reload">
        <el-option label="自动" value="auto" />
        <el-option label="人工" value="manual" />
      </el-select>
      <el-date-picker
        v-model="timeRange"
        type="datetimerange"
        range-separator="至"
        start-placeholder="开始时间"
        end-placeholder="结束时间"
        size="small"
        value-format="YYYY-MM-DDTHH:mm:ssZ"
        @change="onTimeChange"
      />
      <div class="batch-bar" v-if="selectedIds.size > 0">
        <span class="batch-count">已选 {{ selectedIds.size }} 项</span>
        <el-button size="small" type="danger" @click="batchAction('mark_non_face')">改为非人脸</el-button>
        <el-button size="small" type="warning" @click="batchAction('mark_low_quality')">改为低质量</el-button>
        <el-button size="small" type="primary" @click="batchAction('accept')">确认可识别</el-button>
        <el-button size="small" plain @click="selectedIds.clear()">取消选择</el-button>
      </div>
    </div>

    <div v-loading="loading" class="card-grid">
      <div v-if="items.length === 0 && !loading" class="empty-state">暂无样本</div>
      <div
        v-for="item in items"
        :key="item.event_id"
        class="face-card"
        :class="{ selected: selectedIds.has(item.event_id) }"
        @click="openDetail(item)"
      >
        <el-checkbox
          :model-value="selectedIds.has(item.event_id)"
          class="card-checkbox"
          @click.stop
          @change="toggleSelect(item.event_id)"
        />
        <img v-if="item.face_id" :src="faceThumbnail(item.face_id)" :alt="`face-${item.event_id}`" class="card-thumb" />
        <div v-else class="card-thumb-placeholder">无裁剪</div>
        <div class="card-meta">
          <el-tag size="small" :type="decisionTagType(item.decision)">{{ decisionLabel(item.decision) }}</el-tag>
          <el-tag v-if="item.reason" size="small" :type="item.reason === 'non_face' ? 'danger' : 'warning'">
            {{ reasonLabel(item.reason) }}
          </el-tag>
          <el-tag size="small" type="info">{{ item.source === 'auto' ? '自动' : '人工' }}</el-tag>
        </div>
        <div class="card-footer">
          <span>{{ (item.face_validity_score * 100).toFixed(0) }}% 有效</span>
          <span class="card-rule">{{ item.rule_version }}</span>
        </div>
      </div>
    </div>

    <div class="pager" v-if="total > pageSize">
      <el-pagination
        background
        layout="prev, pager, next"
        :total="total"
        :page-size="pageSize"
        :current-page="page"
        @current-change="onPageChange"
      />
    </div>

    <!-- 详情抽屉 -->
    <el-drawer v-model="detailVisible" title="人脸质检详情" size="540px" destroy-on-close>
      <div v-if="current" class="detail-content">
        <div class="detail-preview">
          <img v-if="current.face_id" :src="faceThumbnail(current.face_id)" class="detail-face" />
          <img v-if="current.photo_thumbnail" :src="photoThumbUrl(current.photo_thumbnail)" class="detail-photo" />
        </div>
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="判定">{{ decisionLabel(current.decision) }}</el-descriptions-item>
          <el-descriptions-item label="原因" v-if="current.reason">{{ reasonLabel(current.reason) }}</el-descriptions-item>
          <el-descriptions-item label="来源">{{ current.source === 'auto' ? '自动' : '人工' }}</el-descriptions-item>
          <el-descriptions-item label="有效性">{{ (current.face_validity_score * 100).toFixed(1) }}%</el-descriptions-item>
          <el-descriptions-item label="质量分">{{ (current.quality_score * 100).toFixed(1) }}%</el-descriptions-item>
          <el-descriptions-item label="原因码">{{ (current.reason_codes || []).join(', ') || '—' }}</el-descriptions-item>
          <el-descriptions-item label="规则版本">{{ current.rule_version }}</el-descriptions-item>
          <el-descriptions-item label="模型版本">{{ current.model_version || '—' }}</el-descriptions-item>
          <el-descriptions-item label="人脸框">
            x={{ current.bbox_x.toFixed(3) }} y={{ current.bbox_y.toFixed(3) }}
            w={{ current.bbox_width.toFixed(3) }} h={{ current.bbox_height.toFixed(3) }}
          </el-descriptions-item>
          <el-descriptions-item label="时间">{{ current.created_at }}</el-descriptions-item>
          <el-descriptions-item label="证据" v-if="current.evidence_json">
            <pre class="evidence-json">{{ formatEvidence(current.evidence_json) }}</pre>
          </el-descriptions-item>
        </el-descriptions>

        <div class="detail-actions">
          <el-button size="small" type="danger" @click="singleAction('mark_non_face')">改为非人脸</el-button>
          <el-button size="small" type="warning" @click="singleAction('mark_low_quality')">改为低质量</el-button>
          <el-button size="small" type="primary" @click="singleAction('accept')">确认可识别</el-button>
          <el-button size="small" @click="singleAction('restore')">恢复参与识别</el-button>
        </div>
      </div>
    </el-drawer>

    <!-- 按规则版本恢复 -->
    <el-dialog v-model="restoreVisible" title="按规则版本恢复自动排除" width="420px">
      <el-form label-width="100px">
        <el-form-item label="规则版本">
          <el-select v-model="restoreRuleVersion" placeholder="选择规则版本" style="width: 100%">
            <el-option v-for="rv in ruleVersionOptions" :key="rv" :label="rv" :value="rv" />
          </el-select>
        </el-form-item>
        <el-form-item label="数量上限">
          <el-input-number v-model="restoreLimit" :min="1" :max="5000" :step="100" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="restoreVisible = false">取消</el-button>
        <el-button type="primary" :loading="restoring" @click="doRestore">恢复</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { peopleApi } from '@/api/people'
import type {
  FaceQualityAction,
  FaceQualityReviewItem,
  FaceQualityStats,
} from '@/types/people'

const route = useRoute()
const apiBaseUrl = (import.meta as any).env?.VITE_API_BASE_URL || '/api/v1'

const activeTab = ref<'pending_review' | 'auto_excluded' | 'manual_confirmed'>('pending_review')
const stats = ref<FaceQualityStats | null>(null)
const items = ref<FaceQualityReviewItem[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = 24

const filters = reactive<{ reason: string; source: string; rule_version: string; start_time?: string; end_time?: string }>({
  reason: '',
  source: '',
  rule_version: '',
})
const timeRange = ref<[string, string] | null>(null)
const selectedIds = ref<Set<number>>(new Set())

const detailVisible = ref(false)
const current = ref<FaceQualityReviewItem | null>(null)

const restoreVisible = ref(false)
const restoreRuleVersion = ref('')
const restoreLimit = ref(500)
const restoring = ref(false)

const ruleVersionOptions = computed(() => {
  if (!stats.value?.by_rule_version) return []
  return Object.keys(stats.value.by_rule_version).sort()
})

const faceThumbnail = (faceId: number) => `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`
const photoThumbUrl = (path: string) => `${apiBaseUrl}/../${path}`

const decisionLabel = (d: string) => ({
  accepted: '已接受',
  non_face: '非人脸',
  low_quality: '低质量',
  review_required: '待审核',
}[d] || d)

const decisionTagType = (d: string): 'success' | 'danger' | 'warning' | 'info' => ({
  accepted: 'success',
  non_face: 'danger',
  low_quality: 'warning',
  review_required: 'info',
}[d] as any) || 'info'

const reasonLabel = (r: string) => ({ non_face: '非人脸', low_quality: '低质量' }[r] || r)

const formatEvidence = (json: string) => {
  try {
    return JSON.stringify(JSON.parse(json), null, 2)
  } catch {
    return json
  }
}

const loadStats = async () => {
  try {
    const res = await peopleApi.getFaceQualityStats()
    stats.value = res.data.data ?? null
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '加载统计失败')
  }
}

const loadReviews = async () => {
  loading.value = true
  try {
    const res = await peopleApi.listFaceQualityReviews({
      state: activeTab.value,
      reason: filters.reason || undefined,
      source: filters.source || undefined,
      rule_version: filters.rule_version || undefined,
      start_time: filters.start_time,
      end_time: filters.end_time,
      page: page.value,
      page_size: pageSize,
    })
    const data = res.data.data
    if (data) {
      items.value = data.items
      total.value = data.total
    }
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '加载列表失败')
  } finally {
    loading.value = false
  }
}

const reload = async () => {
  page.value = 1
  selectedIds.value.clear()
  await Promise.all([loadStats(), loadReviews()])
}

const onTabChange = async () => {
  page.value = 1
  selectedIds.value.clear()
  await loadReviews()
}

const onPageChange = (p: number) => {
  page.value = p
  loadReviews()
}

const onTimeChange = (val: [string, string] | null) => {
  if (val) {
    filters.start_time = val[0]
    filters.end_time = val[1]
  } else {
    filters.start_time = undefined
    filters.end_time = undefined
  }
  reload()
}

const toggleSelect = (id: number) => {
  const next = new Set(selectedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selectedIds.value = next
}

const openDetail = (item: FaceQualityReviewItem) => {
  current.value = item
  detailVisible.value = true
}

const applyDecision = async (eventIds: number[], action: FaceQualityAction) => {
  try {
    await peopleApi.applyFaceQualityDecision(eventIds, action)
    ElMessage.success('已应用')
    selectedIds.value.clear()
    detailVisible.value = false
    await Promise.all([loadStats(), loadReviews()])
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '操作失败')
  }
}

const batchAction = async (action: FaceQualityAction) => {
  const ids = Array.from(selectedIds.value)
  if (ids.length === 0) return
  await applyDecision(ids, action)
}

const singleAction = async (action: FaceQualityAction) => {
  if (!current.value) return
  if (action === 'restore') {
    try {
      await ElMessageBox.confirm('恢复后该样本将回到待聚类状态，重新参与人物聚合。是否继续？', '恢复确认', {
        confirmButtonText: '恢复',
        cancelButtonText: '取消',
        type: 'info',
      })
    } catch {
      return
    }
  }
  await applyDecision([current.value.event_id], action)
}

const openRestoreDialog = () => {
  restoreRuleVersion.value = filters.rule_version || ''
  restoreVisible.value = true
}

const doRestore = async () => {
  if (!restoreRuleVersion.value) {
    ElMessage.warning('请选择规则版本')
    return
  }
  restoring.value = true
  try {
    const res = await peopleApi.restoreAutoFaceQuality(restoreRuleVersion.value, restoreLimit.value)
    const restored = res.data.data?.restored ?? 0
    ElMessage.success(`已恢复 ${restored} 项`)
    restoreVisible.value = false
    await Promise.all([loadStats(), loadReviews()])
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '恢复失败')
  } finally {
    restoring.value = false
  }
}

onMounted(async () => {
  const photoId = route.query.photo_id
  if (typeof photoId === 'string' && photoId) {
    // 从照片详情页跳入：默认停在待审核 Tab，便于处理该照片样本。
    activeTab.value = 'pending_review'
  }
  await reload()
})
</script>

<style scoped>
.face-quality-page { padding: 16px 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.page-title { display: flex; align-items: baseline; gap: 12px; }
.page-title h2 { margin: 0; font-size: 20px; }
.page-subtitle { font-size: 13px; color: var(--el-text-color-secondary); }
.header-actions { display: flex; gap: 8px; align-items: center; }
.quality-tabs { margin-bottom: 8px; }
.filter-bar { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; margin-bottom: 12px; }
.batch-bar { display: flex; gap: 8px; align-items: center; margin-left: auto; }
.batch-count { font-size: 13px; color: var(--el-text-color-secondary); }
.card-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 12px; min-height: 200px; }
.empty-state { grid-column: 1 / -1; text-align: center; color: var(--el-text-color-secondary); padding: 48px 0; }
.face-card { position: relative; border: 1px solid var(--el-border-color-lighter); border-radius: 8px; padding: 8px; cursor: pointer; background: var(--el-bg-color); transition: border-color 0.15s; }
.face-card:hover { border-color: var(--el-color-primary); }
.face-card.selected { border-color: var(--el-color-primary); box-shadow: 0 0 0 2px var(--el-color-primary-light-7); }
.card-checkbox { position: absolute; top: 4px; left: 4px; z-index: 2; }
.card-thumb { width: 100%; aspect-ratio: 1; object-fit: cover; border-radius: 6px; background: var(--el-fill-color-light); }
.card-thumb-placeholder { width: 100%; aspect-ratio: 1; display: flex; align-items: center; justify-content: center; color: var(--el-text-color-placeholder); background: var(--el-fill-color-light); border-radius: 6px; }
.card-meta { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 6px; }
.card-footer { display: flex; justify-content: space-between; font-size: 12px; color: var(--el-text-color-secondary); margin-top: 4px; }
.card-rule { font-family: monospace; }
.pager { display: flex; justify-content: center; margin-top: 16px; }
.detail-content { padding: 0 4px; }
.detail-preview { display: flex; gap: 12px; margin-bottom: 16px; }
.detail-face { width: 120px; height: 120px; object-fit: cover; border-radius: 8px; }
.detail-photo { flex: 1; max-height: 120px; object-fit: cover; border-radius: 8px; }
.evidence-json { max-height: 200px; overflow: auto; font-size: 11px; white-space: pre-wrap; word-break: break-all; margin: 0; }
.detail-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
</style>
