<template>
  <div class="face-quality-page">
    <div class="page-header">
      <div class="page-title">
        <h2>人脸质检</h2>
        <span v-if="stats" class="page-subtitle">
          待审核 {{ stats.pending_review }} · 待补证据 {{ stats.historical_missing_evidence }} ·
          待重试 {{ stats.rescore_retryable }} · 自动隔离 {{ stats.auto_excluded }} · 已确认 {{ stats.manual_confirmed }}
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
      <el-tab-pane :label="`待人工审核 (${stats?.pending_review ?? 0})`" name="pending_review" />
      <el-tab-pane :label="`历史人脸待补证据 (${stats?.historical_missing_evidence ?? 0})`" name="historical_missing_evidence" />
      <el-tab-pane :label="`待重试/处理异常 (${stats?.rescore_retryable ?? 0})`" name="rescore_retryable" />
      <el-tab-pane :label="`自动隔离 (${stats?.auto_excluded ?? 0})`" name="auto_excluded" />
      <el-tab-pane :label="`已人工确认 (${stats?.manual_confirmed ?? 0})`" name="manual_confirmed" />
    </el-tabs>

    <!-- 历史补证据任务状态卡 -->
    <div v-if="activeTab === 'historical_missing_evidence'" class="rescore-card">
      <div class="rescore-card-header">
        <span class="rescore-title">历史补证据任务</span>
        <el-button size="small" type="primary" @click="openCalibrationDialog">启动校准任务</el-button>
        <el-button
          v-if="eligibleCalibrationRuns.length > 0"
          size="small"
          type="danger"
          @click="openFullEnforceDialog"
        >启动全量 enforce</el-button>
      </div>
      <div v-if="latestRun" class="rescore-card-body">
        <span>运行 #{{ latestRun.id }} · {{ rescoreModeLabel(latestRun.mode) }} · {{ rescoreStatusLabel(latestRun.status) }}<span v-if="latestRun.retry_of_run_id"> · 重试自 #{{ latestRun.retry_of_run_id }}</span> · 管线 {{ pipelineLabel(latestRun.pipeline_version) }} · 范围 {{ scopeLabel(latestRun.target_scope) }}</span>
        <span>目标 {{ latestRun.target_face_count }} Face / {{ latestRun.target_photo_count }} 照片</span>
        <span>已获证据 {{ latestRun.processed_face_count }} · 人工覆盖 {{ latestRun.superseded_manual_count }} · 真实灰区 {{ latestRun.review_required_count }} · 自动隔离 {{ latestRun.auto_excluded_count }} · 待重试/未匹配 {{ latestRun.retryable_count }} · 终态照片 {{ latestRun.processed_photo_count }}<span v-if="latestRun.eligible_for_enforce"> · ✅ 可作为 v2 enforce 校准</span><span v-else-if="latestRun.pipeline_version === 'legacy_v1'"> · ⚠️ v1 不可作为 v2 enforce 校准</span></span>
        <span v-if="latestRun.last_error" class="rescore-error">{{ latestRun.last_error }}</span>
        <div class="rescore-actions">
          <el-button v-if="latestRun.status === 'running'" size="small" @click="pauseRun(latestRun.id)">暂停</el-button>
          <el-button v-if="latestRun.status === 'paused'" size="small" type="primary" @click="resumeRun(latestRun.id)">继续</el-button>
          <el-button v-if="latestRun.status === 'running' || latestRun.status === 'paused'" size="small" @click="cancelRun(latestRun.id)">取消</el-button>
          <el-button
            v-if="canRetryRun(latestRun)"
            size="small"
            type="warning"
            @click="openRetryDialog(latestRun)"
          >重试运行 #{{ latestRun.id }}</el-button>
          <el-button v-if="latestRun.auto_excluded_count > 0" size="small" type="warning" @click="restoreRun(latestRun.id)">按本运行恢复自动隔离</el-button>
        </div>
      </div>
      <div v-else class="rescore-empty">暂无运行。历史缺证据样本不需要人工逐张确认，可启动校准任务补齐模型证据。</div>
    </div>

    <div class="filter-bar">
      <el-select v-model="filters.reason" placeholder="原因" clearable size="small" style="width: 140px" @change="reload">
        <el-option label="非人脸" value="non_face" />
        <el-option label="低质量" value="low_quality" />
      </el-select>
      <el-select v-model="filters.source" placeholder="来源" clearable size="small" style="width: 120px" @change="reload">
        <el-option label="自动" value="auto" />
        <el-option label="人工" value="manual" />
      </el-select>
      <div class="time-filter">
        <span class="time-label">质检事件时间</span>
        <el-date-picker
          v-model="timeRange"
          type="datetimerange"
          range-separator="至"
          start-placeholder="事件开始时间"
          end-placeholder="事件结束时间"
          size="small"
          value-format="YYYY-MM-DDTHH:mm:ssZ"
          @change="onTimeChange"
        />
        <span class="time-hint">不是照片拍摄时间</span>
      </div>
      <div class="page-select">
        <el-checkbox
          :model-value="pageSelectAll"
          :indeterminate="pageSelectIndeterminate"
          @change="toggleSelectAllPage"
        >
          全选本页（{{ items.length }}）
        </el-checkbox>
        <el-button size="small" plain @click="invertSelectPage" :disabled="items.length === 0">反选本页</el-button>
        <span v-if="selectedIds.size > 0" class="batch-count">已选 {{ selectedIds.size }} 项（跨页累计）</span>
        <el-button v-if="selectedIds.size > 0" size="small" type="danger" @click="batchAction('mark_non_face')">改为非人脸</el-button>
        <el-button v-if="selectedIds.size > 0" size="small" type="warning" @click="batchAction('mark_low_quality')">改为低质量</el-button>
        <el-button v-if="selectedIds.size > 0" size="small" type="primary" @click="batchAction('accept')">确认可识别</el-button>
        <el-button v-if="selectedIds.size > 0" size="small" plain @click="clearSelection">清空选择</el-button>
      </div>
    </div>

    <div v-loading="loading" class="card-grid">
      <div v-if="items.length === 0 && !loading" class="empty-state">{{ emptyStateText }}</div>
      <div
        v-for="item in items"
        :key="item.event_id"
        class="face-card"
        :class="{ selected: selectedIds.has(item.event_id) }"
        @click="openDetail(item)"
      >
        <!-- 独立选择热区：可聚焦 button(role=checkbox)，≥40x40px，事件 stopPropagation 不触发 openDetail -->
        <button
          type="button"
          class="select-hotzone"
          role="checkbox"
          :aria-checked="selectedIds.has(item.event_id) ? 'true' : 'false'"
          :aria-label="`选择样本 ${item.event_id}`"
          @click.stop="onSelectClick(item.event_id, $event)"
          @keydown="onSelectKeydown(item.event_id, $event)"
        >
          <span class="select-mark" :class="{ checked: selectedIds.has(item.event_id) }">✓</span>
        </button>
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
          <span v-if="isV2(item)" class="card-v2">v2 独立复核</span>
          <span v-else-if="isLegacy(item)" class="card-legacy">v1 同源</span>
          <span v-else-if="hasEvidence(item)">{{ (item.face_validity_score * 100).toFixed(0) }}% 有效</span>
          <span v-else class="card-na">有效性未采集</span>
          <span class="card-rule">{{ item.rule_version }}</span>
        </div>
      </div>
    </div>

    <div class="pager" v-if="total > 0">
      <el-pagination
        background
        layout="prev, pager, next, sizes"
        :total="total"
        :page-size="pageSize"
        :current-page="page"
        :page-sizes="[24, 48, 96]"
        @current-change="onPageChange"
        @size-change="onPageSizeChange"
      />
    </div>

    <!-- 详情抽屉 -->
    <el-drawer v-model="detailVisible" title="人脸质检详情" size="540px" destroy-on-close>
      <div v-if="current" class="detail-content">
        <div class="detail-preview">
          <img v-if="current.face_id" :src="faceThumbnail(current.face_id)" class="detail-face" />
          <div class="detail-photo-frame">
            <el-image
              v-if="current.photo_id"
              :src="photoThumbnail(current.photo_id, current.event_id)"
              fit="contain"
              :preview-src-list="previewSrcList"
              preview-teleported
              class="detail-photo"
              @error="onPhotoError"
              @load="onPhotoLoad"
            >
              <template #error>
                <div class="detail-photo-error">
                  <span>照片缩略图不可用</span>
                  <router-link :to="`/photos/${current.photo_id}`" class="photo-detail-link">查看照片详情</router-link>
                </div>
              </template>
            </el-image>
            <div v-else class="detail-photo-error">
              <span>照片缩略图不可用</span>
            </div>
          </div>
        </div>
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="判定">{{ decisionLabel(current.decision) }}</el-descriptions-item>
          <el-descriptions-item label="原因" v-if="current.reason">{{ reasonLabel(current.reason) }}</el-descriptions-item>
          <el-descriptions-item label="来源">{{ current.source === 'auto' ? '自动' : '人工' }}</el-descriptions-item>
          <el-descriptions-item label="有效性">
            <span v-if="hasEvidence(current)">{{ (current.face_validity_score * 100).toFixed(1) }}%</span>
            <span v-else>未采集（历史人脸）</span>
          </el-descriptions-item>
          <el-descriptions-item label="质量分">
            <span v-if="hasEvidence(current)">{{ (current.quality_score * 100).toFixed(1) }}%</span>
            <span v-else>未采集</span>
          </el-descriptions-item>
          <el-descriptions-item label="原因码" v-if="hasEvidence(current) && current.reason_codes && current.reason_codes.length">
            {{ current.reason_codes.join(', ') }}
          </el-descriptions-item>
          <el-descriptions-item label="规则版本">{{ current.rule_version }}</el-descriptions-item>
          <el-descriptions-item label="模型版本">{{ current.model_version || '—' }}</el-descriptions-item>
          <el-descriptions-item label="人脸框">
            x={{ current.bbox_x.toFixed(3) }} y={{ current.bbox_y.toFixed(3) }}
            w={{ current.bbox_width.toFixed(3) }} h={{ current.bbox_height.toFixed(3) }}
          </el-descriptions-item>
          <el-descriptions-item label="时间">{{ current.created_at }}</el-descriptions-item>
          <el-descriptions-item label="证据" v-if="hasEvidence(current) && current.evidence_json">
            <pre class="evidence-json">{{ formatEvidence(current.evidence_json) }}</pre>
          </el-descriptions-item>
        </el-descriptions>

        <!-- v2 独立复核证据：四组结构化证据，不再压成单一有效性百分比 -->
        <div v-if="isV2(current)" class="v2-evidence">
          <div class="v2-evidence-title">独立复核证据（independent_v2）</div>
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item label="主检测分">
              {{ (current.evidence_v2!.primary_detector_score * 100).toFixed(1) }}%
              <span class="v2-hint">（准入线 65%，低于此值的新检测不持久化）</span>
            </el-descriptions-item>
            <el-descriptions-item label="独立复核">
              <el-tag size="small" :type="verifierTagType(current.evidence_v2!.verification_status)">
                {{ verifierStatusLabel(current.evidence_v2!.verification_status) }}
              </el-tag>
              <span class="v2-score">置信度 {{ (current.evidence_v2!.verifier_score * 100).toFixed(1) }}%</span>
              <span class="v2-model">{{ current.evidence_v2!.verifier_name }} {{ current.evidence_v2!.verifier_version }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="原图人脸框">
              {{ current.evidence_v2!.face_box_width_px }} × {{ current.evidence_v2!.face_box_height_px }} px
              <span class="v2-hint">（原图 {{ current.evidence_v2!.original_width }} × {{ current.evidence_v2!.original_height }} px）</span>
            </el-descriptions-item>
            <el-descriptions-item label="上下文裁剪">
              {{ current.evidence_v2!.context_crop_width_px }} × {{ current.evidence_v2!.context_crop_height_px }} px
              <span class="v2-hint">（扩展比例 {{ current.evidence_v2!.context_expand_ratio }}）</span>
            </el-descriptions-item>
            <el-descriptions-item label="质量特征">
              清晰度 {{ current.evidence_v2!.sharpness_norm?.toFixed(2) ?? '—' }} ·
              亮度 {{ current.evidence_v2!.brightness_norm?.toFixed(1) ?? '—' }} ·
              对比度 {{ current.evidence_v2!.contrast_norm?.toFixed(2) ?? '—' }} ·
              遮挡 {{ current.evidence_v2!.occluded ? '是' : '否' }}
              <div class="v2-hint">计算域：{{ current.evidence_v2!.quality_domain || '—' }} · 版本 {{ current.evidence_v2!.quality_version || '—' }}</div>
            </el-descriptions-item>
            <el-descriptions-item label="原因码" v-if="current.evidence_v2!.reason_codes && current.evidence_v2!.reason_codes.length">
              {{ current.evidence_v2!.reason_codes.join(', ') }}
            </el-descriptions-item>
            <el-descriptions-item label="系统建议" v-if="current.suggested_decision">
              <el-tag size="small" type="warning">{{ decisionLabel(current.suggested_decision) }}</el-tag>
              <span class="v2-hint">（shadow 校准建议，已降级为待审核）</span>
            </el-descriptions-item>
          </el-descriptions>
        </div>
        <!-- legacy_v1 旧版同源指标：仅供历史追溯，不得作为人工判断依据 -->
        <el-alert
          v-else-if="isLegacy(current)"
          class="legacy-alert"
          type="info"
          :closable="false"
          show-icon
          title="旧版同源指标（legacy_v1），仅供历史追溯"
          description="此证据由 v1 score-known-faces 在已旋转展示缩略图上复用同一套 InsightFace 检测产生，属同源启发式证据，不得作为是否为脸的人工判断依据。"
        />

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

    <!-- 校准任务二次确认 -->
    <el-dialog v-model="calibrationVisible" title="启动校准任务" width="460px">
      <el-form label-width="100px">
        <el-form-item label="照片上限">
          <el-input-number v-model="calibrationPhotoLimit" :min="100" :max="100000" :step="1000" />
        </el-form-item>
      </el-form>
      <div class="confirm-hint">只写证据，不自动排除。校准完成后人工核验结果，再决定是否启动全量 enforce。</div>
      <template #footer>
        <el-button @click="calibrationVisible = false">取消</el-button>
        <el-button type="primary" :loading="creatingRun" @click="doCreateCalibration">启动校准</el-button>
      </template>
    </el-dialog>

    <!-- 全量 enforce 二次确认：下拉选择后端 eligible_for_enforce=true 的校准 run -->
    <el-dialog v-model="fullEnforceVisible" title="启动全量 enforce" width="460px">
      <el-form label-width="120px">
        <el-form-item label="合格校准运行">
          <el-select v-model="fullEnforceCalibrationId" placeholder="选择已通过的校准运行" style="width: 100%">
            <el-option
              v-for="r in eligibleCalibrationRuns"
              :key="r.id"
              :label="`#${r.id} · 目标 ${r.target_face_count} Face · 已获证据 ${r.processed_face_count}`"
              :value="r.id"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <div class="confirm-hint">仅高确定性非人脸/严重低质量会移出人物聚类；可按本运行恢复。不会自动启动后续运行。</div>
      <template #footer>
        <el-button @click="fullEnforceVisible = false">取消</el-button>
        <el-button type="danger" :loading="creatingRun" :disabled="!fullEnforceCalibrationId" @click="doCreateFullEnforce">启动全量 enforce</el-button>
      </template>
    </el-dialog>

    <!-- 按运行重试二次确认：只重试技术失败样本，仍为 shadow，不自动隔离 -->
    <el-dialog v-model="retryVisible" title="重试运行" width="460px">
      <div class="confirm-hint">
        将基于运行 #{{ retrySourceRun?.id }} 的 {{ retrySourceRun?.retryable_count }} 条技术失败样本创建新的 shadow 校准运行。
        只重试技术失败样本，仍为 shadow，不自动隔离。
      </div>
      <template #footer>
        <el-button @click="retryVisible = false">取消</el-button>
        <el-button type="primary" :loading="creatingRun" @click="doRetryRun">重试</el-button>
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
  FaceQualityRescoreRun,
  FaceQualityReviewItem,
  FaceQualityState,
  FaceQualityStats,
} from '@/types/people'

const route = useRoute()
const apiBaseUrl = (import.meta as any).env?.VITE_API_BASE_URL || '/api/v1'
const activeTab = ref<FaceQualityState>('pending_review')
const stats = ref<FaceQualityStats | null>(null)
const items = ref<FaceQualityReviewItem[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)

const PAGE_SIZE_STORAGE_KEY = 'face_quality_page_size'
const readStoredPageSize = (): number => {
  try {
    const v = localStorage.getItem(PAGE_SIZE_STORAGE_KEY)
    if (v === '24' || v === '48' || v === '96') return Number(v)
  } catch { /* ignore */ }
  return 48
}
const pageSize = ref<number>(readStoredPageSize())

const filters = reactive<{ reason: string; source: string; rule_version: string; start_time?: string; end_time?: string }>({
  reason: '',
  source: '',
  rule_version: '',
})
const timeRange = ref<[string, string] | null>(null)
const selectedIds = ref<Set<number>>(new Set())
// Shift 连选锚点：仅在当前 items 闭区间生效。翻页/切 Tab/筛选/批量成功/清空时重置。
const selectionAnchorId = ref<number | null>(null)

const detailVisible = ref(false)
const current = ref<FaceQualityReviewItem | null>(null)

const restoreVisible = ref(false)
const restoreRuleVersion = ref('')
const restoreLimit = ref(500)
const restoring = ref(false)

// 历史重评分运行面板。
const latestRun = ref<FaceQualityRescoreRun | null>(null)
const rescoreRuns = ref<FaceQualityRescoreRun[]>([])
const calibrationVisible = ref(false)
const calibrationPhotoLimit = ref(1000)
const fullEnforceVisible = ref(false)
const fullEnforceCalibrationId = ref<number | null>(null)
const creatingRun = ref(false)
const retryVisible = ref(false)
const retrySourceRun = ref<FaceQualityRescoreRun | null>(null)

// 后端裁决的合格校准 run（eligible_for_enforce=true）。前端不得以 status=completed 自行推断。
const eligibleCalibrationRuns = computed(() =>
  rescoreRuns.value.filter(r => r.eligible_for_enforce === true),
)

// 可重试的 run：calibration 且 completed/completed_with_errors 且有待重试样本。
const canRetryRun = (r: FaceQualityRescoreRun | null): boolean => {
  if (!r) return false
  if (r.mode !== 'calibration') return false
  if (r.status !== 'completed' && r.status !== 'completed_with_errors') return false
  return r.retryable_count > 0
}

const ruleVersionOptions = computed(() => {
  if (!stats.value?.by_rule_version) return []
  return Object.keys(stats.value.by_rule_version).sort()
})

const faceThumbnail = (faceId: number) => `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`
// 详情照片走受权限保护的 /photos/:id/thumbnail 接口（后端负责授权/HEIC 回退/被动生成/缓存）。
// version 仅用于浏览器缓存隔离，使用 event_id；后端忽略该查询参数，不新增路由。
const photoThumbnail = (photoId: number, version: number) =>
  `${apiBaseUrl}/photos/${photoId}/thumbnail?v=${version}`

// 预览源使用受保护的 /photos/:id/image（非缩略图），不把文件路径暴露到 DOM。
const previewSrcList = computed<string[]>(() => {
  if (!current.value?.photo_id) return []
  return [`${apiBaseUrl}/photos/${current.value.photo_id}/image`]
})

const emptyStateText = computed(() => {
  if (activeTab.value === 'historical_missing_evidence') {
    return '此类记录没有模型证据，不需要人工逐张确认。可启动校准任务补齐证据。'
  }
  if (activeTab.value === 'rescore_retryable') {
    return '重评分遇到可重试技术问题或未匹配样本。修复条件后可重试。'
  }
  return '暂无样本'
})

// 证据可用性：后端依据 evidence_json 是否可解析决定。前端将缺失字段视为 false。
const hasEvidence = (item: FaceQualityReviewItem) => item.quality_evidence_available === true

// v2 独立复核证据：evidence_pipeline=independent_v2 且后端已解析出 evidence_v2。
const isV2 = (item: FaceQualityReviewItem) =>
  item.evidence_pipeline === 'independent_v2' && !!item.evidence_v2
// legacy_v1 旧版同源指标：仅供历史追溯。
const isLegacy = (item: FaceQualityReviewItem) => item.evidence_pipeline === 'legacy_v1'

const verifierStatusLabel = (s: string) =>
  ({ face: '确认为脸', no_face: '未检测到脸', uncertain: '无法可靠判断', error: '验证失败' } as Record<string, string>)[s] ?? s
const verifierTagType = (s: string): 'success' | 'info' | 'warning' | 'danger' =>
  ({ face: 'success', no_face: 'danger', uncertain: 'warning', error: 'warning' } as Record<string, 'success' | 'info' | 'warning' | 'danger'>)[s] ?? 'info'

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

const rescoreModeLabel = (m: string) => (m === 'calibration' ? '校准' : '全量')
const rescoreStatusLabel = (s: string) => ({
  queued: '排队', running: '运行中', paused: '已暂停',
  completed: '已完成', completed_with_errors: '完成但有错误',
  failed: '失败', cancelled: '已取消',
}[s] || s)

const pipelineLabel = (p?: string) =>
  p === 'independent_v2' ? 'v2 独立复核' : p === 'legacy_v1' ? 'v1 同源' : (p || '—')
const scopeLabel = (s?: string) =>
  s === 'all_non_manual_faces_without_independent_v2' ? '全部无人工/v2 结论'
  : s === 'historical_backfill_missing' ? '历史缺证据'
  : (s || '—')

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
      page_size: pageSize.value,
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

const loadRescoreRuns = async () => {
  try {
    const res = await peopleApi.listFaceQualityRescoreRuns(10)
    const runs = res.data.data?.items ?? []
    rescoreRuns.value = runs
    latestRun.value = runs.length > 0 ? runs[0]! : null
  } catch {
    // 静默：面板不影响主流程。
  }
}

const reload = async () => {
  page.value = 1
  clearSelection()
  await Promise.all([loadStats(), loadReviews()])
}

const onTabChange = async () => {
  page.value = 1
  clearSelection()
  if (activeTab.value === 'historical_missing_evidence') {
    await Promise.all([loadReviews(), loadRescoreRuns()])
  } else {
    await loadReviews()
  }
}

const onPageChange = (p: number) => {
  page.value = p
  // 翻页保留选择，允许跨已浏览页累积；但重置 Shift 锚点。
  selectionAnchorId.value = null
  loadReviews()
}

const onPageSizeChange = (size: number) => {
  pageSize.value = size
  try { localStorage.setItem(PAGE_SIZE_STORAGE_KEY, String(size)) } catch { /* ignore */ }
  // 切页大小：回第 1 页、保留筛选与已选 ID、重置 Shift 锚点。
  page.value = 1
  selectionAnchorId.value = null
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

// ---- 选择热区 + Shift 连选 ----

const toggleSelect = (id: number) => {
  const next = new Set(selectedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selectedIds.value = next
}

// 普通点击：切换并设为锚点。
const onSelectClick = (id: number, ev: MouseEvent) => {
  if (ev.shiftKey && selectionAnchorId.value !== null) {
    shiftSelect(id)
    return
  }
  toggleSelect(id)
  selectionAnchorId.value = id
}

// Shift 点击：仅当锚点与目标都在当前 items 时，按 index 闭区间批量操作。
const shiftSelect = (targetId: number) => {
  const anchor = selectionAnchorId.value
  if (anchor === null) {
    toggleSelect(targetId)
    selectionAnchorId.value = targetId
    return
  }
  const ids = items.value.map(i => i.event_id)
  const ai = ids.indexOf(anchor)
  const ti = ids.indexOf(targetId)
  if (ai < 0 || ti < 0) {
    // 锚点或目标不在当前页 → 退化为普通选择。
    toggleSelect(targetId)
    selectionAnchorId.value = targetId
    return
  }
  const lo = Math.min(ai, ti)
  const hi = Math.max(ai, ti)
  const range = ids.slice(lo, hi + 1)
  const next = new Set(selectedIds.value)
  // 目标原本未选 → 整段选中；原本已选 → 整段取消。
  const targetSelected = next.has(targetId)
  for (const id of range) {
    if (targetSelected) next.delete(id)
    else next.add(id)
  }
  selectedIds.value = next
}

// 键盘：Space/Enter 在热区聚焦时执行同一选择逻辑（无 Shift 按普通选择）。
const onSelectKeydown = (id: number, ev: KeyboardEvent) => {
  if (ev.key !== ' ' && ev.key !== 'Enter') return
  ev.preventDefault()
  ev.stopPropagation()
  if (ev.shiftKey && selectionAnchorId.value !== null) {
    shiftSelect(id)
    return
  }
  toggleSelect(id)
  selectionAnchorId.value = id
}

// 本页全选三态：基于当前 items 的 event_id，不含其他页。
const pageEventIds = computed(() => items.value.map(i => i.event_id))
const pageSelectAll = computed(() =>
  pageEventIds.value.length > 0 && pageEventIds.value.every(id => selectedIds.value.has(id)),
)
const pageSelectIndeterminate = computed(() =>
  !pageSelectAll.value && pageEventIds.value.some(id => selectedIds.value.has(id)),
)
const toggleSelectAllPage = (checked: boolean) => {
  const next = new Set(selectedIds.value)
  if (checked) {
    for (const id of pageEventIds.value) next.add(id)
  } else {
    for (const id of pageEventIds.value) next.delete(id)
  }
  selectedIds.value = next
  selectionAnchorId.value = null
}
const invertSelectPage = () => {
  const next = new Set(selectedIds.value)
  for (const id of pageEventIds.value) {
    if (next.has(id)) next.delete(id)
    else next.add(id)
  }
  selectedIds.value = next
  selectionAnchorId.value = null
}
const clearSelection = () => {
  selectedIds.value = new Set()
  selectionAnchorId.value = null
}

// 详情照片加载失败反馈。el-image 的 #error 插槽已提供占位，这里仅记录状态。
const detailPhotoError = ref(false)
const onPhotoError = () => { detailPhotoError.value = true }
const onPhotoLoad = () => { detailPhotoError.value = false }

const openDetail = (item: FaceQualityReviewItem) => {
  current.value = item
  detailPhotoError.value = false
  detailVisible.value = true
}

const applyDecision = async (eventIds: number[], action: FaceQualityAction) => {
  try {
    await peopleApi.applyFaceQualityDecision(eventIds, action)
    ElMessage.success('已应用')
    clearSelection()
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

// ---- 历史重评分运行控制 ----

const openCalibrationDialog = () => {
  calibrationPhotoLimit.value = 1000
  calibrationVisible.value = true
}

const doCreateCalibration = async () => {
  creatingRun.value = true
  try {
    await peopleApi.createFaceQualityRescoreRun({
      mode: 'calibration',
      photo_limit: calibrationPhotoLimit.value,
    })
    ElMessage.success('校准任务已创建（只写证据，不自动排除）')
    calibrationVisible.value = false
    await loadRescoreRuns()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '创建校准任务失败')
  } finally {
    creatingRun.value = false
  }
}

const openFullEnforceDialog = () => {
  fullEnforceCalibrationId.value = eligibleCalibrationRuns.value[0]?.id ?? null
  fullEnforceVisible.value = true
}

const doCreateFullEnforce = async () => {
  // 必须选择后端 eligible_for_enforce=true 的校准 run；服务端会再次验证。
  if (!fullEnforceCalibrationId.value) {
    ElMessage.warning('请选择已通过的校准运行')
    return
  }
  creatingRun.value = true
  try {
    await peopleApi.createFaceQualityRescoreRun({
      mode: 'full',
      calibration_run_id: fullEnforceCalibrationId.value,
    })
    ElMessage.success('全量 enforce 任务已创建')
    fullEnforceVisible.value = false
    await loadRescoreRuns()
  } catch (e: any) {
    // 展示服务端错误，不在前端绕过。
    ElMessage.error(e?.response?.data?.error?.message || '创建全量任务失败')
  } finally {
    creatingRun.value = false
  }
}

const openRetryDialog = (run: FaceQualityRescoreRun) => {
  retrySourceRun.value = run
  retryVisible.value = true
}

const doRetryRun = async () => {
  if (!retrySourceRun.value) return
  creatingRun.value = true
  try {
    await peopleApi.retryFaceQualityRescoreRun(retrySourceRun.value.id)
    ElMessage.success('重试运行已创建（shadow，不自动隔离）')
    retryVisible.value = false
    await loadRescoreRuns()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '创建重试运行失败')
  } finally {
    creatingRun.value = false
  }
}

const pauseRun = async (id: number) => {
  try {
    await peopleApi.pauseFaceQualityRescoreRun(id)
    await loadRescoreRuns()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '暂停失败')
  }
}
const resumeRun = async (id: number) => {
  try {
    await peopleApi.resumeFaceQualityRescoreRun(id)
    await loadRescoreRuns()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '恢复失败')
  }
}
const cancelRun = async (id: number) => {
  try {
    await ElMessageBox.confirm('取消后未处理目标停止，已处理审计记录保留。是否继续？', '取消运行', {
      confirmButtonText: '取消运行',
      cancelButtonText: '返回',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await peopleApi.cancelFaceQualityRescoreRun(id)
    await loadRescoreRuns()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '取消失败')
  }
}
const restoreRun = async (id: number) => {
  try {
    const res = await peopleApi.restoreAutoFaceQualityRescoreRun(id)
    const restored = res.data.data?.restored ?? 0
    ElMessage.success(`已按运行 ${id} 恢复 ${restored} 项`)
    await Promise.all([loadStats(), loadReviews(), loadRescoreRuns()])
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || '按运行恢复失败')
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
.rescore-card { border: 1px solid var(--el-border-color-lighter); border-radius: 8px; padding: 12px; margin-bottom: 12px; background: var(--el-fill-color-light); }
.rescore-card-header { display: flex; gap: 8px; align-items: center; margin-bottom: 8px; }
.rescore-title { font-weight: 600; margin-right: auto; }
.rescore-card-body { display: flex; flex-direction: column; gap: 4px; font-size: 13px; color: var(--el-text-color-regular); }
.rescore-error { color: var(--el-color-danger); font-size: 12px; }
.rescore-actions { display: flex; gap: 8px; margin-top: 4px; }
.rescore-empty { font-size: 13px; color: var(--el-text-color-secondary); }
.filter-bar { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; margin-bottom: 12px; }
.time-filter { display: flex; gap: 6px; align-items: center; }
.time-label { font-size: 13px; color: var(--el-text-color-regular); white-space: nowrap; }
.time-hint { font-size: 12px; color: var(--el-text-color-placeholder); white-space: nowrap; }
.page-select { display: flex; gap: 12px; align-items: center; margin-left: auto; flex-wrap: wrap; }
.batch-count { font-size: 13px; color: var(--el-text-color-secondary); }
.card-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 12px; min-height: 200px; }
.empty-state { grid-column: 1 / -1; text-align: center; color: var(--el-text-color-secondary); padding: 48px 0; }
.face-card { position: relative; border: 1px solid var(--el-border-color-lighter); border-radius: 8px; padding: 8px; cursor: pointer; background: var(--el-bg-color); transition: border-color 0.15s; }
.face-card:hover { border-color: var(--el-color-primary); }
.face-card.selected { border-color: var(--el-color-primary); box-shadow: 0 0 0 2px var(--el-color-primary-light-7); }
/* 独立选择热区：≥40x40px，层级高于图片，事件 stopPropagation 不触发 openDetail。 */
.select-hotzone {
  position: absolute; top: 4px; left: 4px; z-index: 3;
  width: 40px; height: 40px; border: none; border-radius: 50%;
  background: rgba(0, 0, 0, 0.35); color: #fff; cursor: pointer;
  display: flex; align-items: center; justify-content: center; padding: 0;
}
.select-hotzone:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 2px; }
.select-mark { font-size: 18px; line-height: 1; opacity: 0; }
.select-mark.checked { opacity: 1; }
.card-thumb { width: 100%; aspect-ratio: 1; object-fit: cover; border-radius: 6px; background: var(--el-fill-color-light); }
.card-thumb-placeholder { width: 100%; aspect-ratio: 1; display: flex; align-items: center; justify-content: center; color: var(--el-text-color-placeholder); background: var(--el-fill-color-light); border-radius: 6px; }
.card-meta { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 6px; }
.card-footer { display: flex; justify-content: space-between; font-size: 12px; color: var(--el-text-color-secondary); margin-top: 4px; }
.card-na { color: var(--el-text-color-placeholder); font-style: italic; }
.card-rule { font-family: monospace; }
.card-v2 { color: var(--el-color-success); font-weight: 600; }
.card-legacy { color: var(--el-text-color-secondary); font-style: italic; }
.v2-evidence { margin-top: 16px; }
.v2-evidence-title { font-weight: 600; margin-bottom: 8px; color: var(--el-color-success); }
.v2-hint { color: var(--el-text-color-secondary); font-size: 12px; margin-left: 4px; }
.v2-score { margin-left: 8px; font-family: monospace; }
.v2-model { margin-left: 8px; font-size: 12px; color: var(--el-text-color-secondary); }
.legacy-alert { margin-top: 16px; }
.pager { display: flex; justify-content: center; margin-top: 16px; }
.detail-content { padding: 0 4px; }
.detail-preview { display: flex; gap: 12px; margin-bottom: 16px; }
.detail-face { width: 120px; height: 120px; object-fit: cover; border-radius: 8px; }
/* 固定高度容器：横图竖图均 contain 完整展示，留白可见。 */
.detail-photo-frame { flex: 1; min-width: 0; height: 220px; border-radius: 8px; background: var(--el-fill-color-light); display: flex; align-items: center; justify-content: center; }
.detail-photo { width: 100%; height: 100%; border-radius: 8px; }
.detail-photo :deep(.el-image__inner) { width: 100%; height: 100%; object-fit: contain; }
.detail-photo-error { display: flex; flex-direction: column; gap: 6px; align-items: flex-start; justify-content: center; width: 100%; height: 100%; min-height: 80px; padding: 12px; border: 1px dashed var(--el-border-color); border-radius: 8px; color: var(--el-text-color-secondary); font-size: 13px; }
.photo-detail-link { font-size: 13px; }
.evidence-json { max-height: 200px; overflow: auto; font-size: 11px; white-space: pre-wrap; word-break: break-all; margin: 0; }
.detail-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
.confirm-hint { font-size: 13px; color: var(--el-color-warning); margin: 8px 0; }
</style>
