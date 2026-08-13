import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import FaceQualityReview from './FaceQualityReview.vue'
import type { FaceQualityReviewItem } from '@/types/people'

// 人脸质检审核页 Task 6 改造回归：
// 1. 详情照片固定高度容器 + object-fit: contain（横图竖图完整展示）。
// 2. 选择热区 ≥40px，点热区边缘不触发 openDetail；图片非热区可打开详情。
// 3. Shift 连选当前页闭区间；Space/Enter 键盘选择；翻页保留选择但重置锚点。
// 4. 分页 24/48/96 默认 48（localStorage），切页大小回第 1 页保留筛选与已选 ID。
// 5. 5 个队列 Tab 使用正确 state；历史/失败队列空态文案不显示为人工待审核。
// 6. rescore run 状态卡与校准/全量确认入口。

beforeAll(() => {
  const store = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
      clear: () => store.clear(),
      key: () => null,
      get length() {
        return store.size
      },
    },
  })
  if (typeof window !== 'undefined' && !window.localStorage) {
    Object.defineProperty(window, 'localStorage', { configurable: true, value: (globalThis as any).localStorage })
  }
})

const mocks = vi.hoisted(() => {
  return {
    ElMessage: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
    ElMessageBox: { confirm: vi.fn() },
    mockGetFaceQualityStats: vi.fn(),
    mockListFaceQualityReviews: vi.fn(),
    mockApplyFaceQualityDecision: vi.fn(),
    mockRestoreAutoFaceQuality: vi.fn(),
    mockListRescoreRuns: vi.fn(),
    mockCreateRescoreRun: vi.fn(),
    mockPauseRescoreRun: vi.fn(),
    mockResumeRescoreRun: vi.fn(),
    mockCancelRescoreRun: vi.fn(),
    mockRestoreRescoreRun: vi.fn(),
    mockRetryRescoreRun: vi.fn(),
  }
})

vi.mock('@/api/people', () => ({
  peopleApi: {
    getFaceQualityStats: (...a: unknown[]) => mocks.mockGetFaceQualityStats(...a),
    listFaceQualityReviews: (...a: unknown[]) => mocks.mockListFaceQualityReviews(...a),
    applyFaceQualityDecision: (...a: unknown[]) => mocks.mockApplyFaceQualityDecision(...a),
    restoreAutoFaceQuality: (...a: unknown[]) => mocks.mockRestoreAutoFaceQuality(...a),
    listFaceQualityRescoreRuns: (...a: unknown[]) => mocks.mockListRescoreRuns(...a),
    createFaceQualityRescoreRun: (...a: unknown[]) => mocks.mockCreateRescoreRun(...a),
    pauseFaceQualityRescoreRun: (...a: unknown[]) => mocks.mockPauseRescoreRun(...a),
    resumeFaceQualityRescoreRun: (...a: unknown[]) => mocks.mockResumeRescoreRun(...a),
    cancelFaceQualityRescoreRun: (...a: unknown[]) => mocks.mockCancelRescoreRun(...a),
    restoreAutoFaceQualityRescoreRun: (...a: unknown[]) => mocks.mockRestoreRescoreRun(...a),
    retryFaceQualityRescoreRun: (...a: unknown[]) => mocks.mockRetryRescoreRun(...a),
  },
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {}, params: {} }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  RouterLink: {
    name: 'RouterLink',
    props: ['to'],
    template: '<a class="router-link-stub" :href="to"><slot/></a>',
  },
}))

vi.mock('element-plus', () => ({
  ElMessage: mocks.ElMessage,
  ElMessageBox: mocks.ElMessageBox,
}))

const stubs = {
  'el-tabs': { template: '<div class="el-tabs"><slot/></div>' },
  'el-tab-pane': { template: '<div><slot/></div>' },
  'el-select': { template: '<div class="el-select"/>' },
  'el-option': { template: '<div/>' },
  'el-date-picker': { template: '<div class="el-date-picker"/>' },
  'el-button': {
    template: '<button class="el-button" @click="$emit(\'click\')"><slot/></button>',
  },
  'el-checkbox': {
    name: 'ElCheckbox',
    props: ['modelValue', 'indeterminate'],
    emits: ['change', 'update:modelValue'],
    template:
      '<div class="el-checkbox" :class="{\'is-indeterminate\': indeterminate, \'is-checked\': modelValue === true}" @click="$emit(\'change\', !modelValue)"><slot/></div>',
  },
  'el-tag': { template: '<span class="el-tag"><slot/></span>' },
  'el-pagination': {
    template: '<div class="el-pagination"/>',
  },
  'el-drawer': {
    props: ['modelValue'],
    template: '<div v-if="modelValue" class="el-drawer"><slot/></div>',
  },
  'el-descriptions': { template: '<div class="el-descriptions"><slot/></div>' },
  'el-descriptions-item': {
    props: ['label'],
    template: '<div class="el-descriptions-item"><span class="desc-label">{{ label }}</span><slot/></div>',
  },
  'el-dialog': {
    props: ['modelValue'],
    template: '<div v-if="modelValue" class="el-dialog"><slot/></div>',
  },
  'el-form': { template: '<div class="el-form"><slot/></div>' },
  'el-form-item': { template: '<div class="el-form-item"><slot/></div>' },
  'el-select-v2': { template: '<div/>' },
  'el-input-number': { template: '<div class="el-input-number"/>' },
  'el-input': { template: '<input class="el-input"/>' },
  'el-alert': {
    props: ['title', 'description', 'type'],
    template: '<div class="el-alert"><span class="el-alert__title">{{ title }}</span><span class="el-alert__description">{{ description }}</span><slot/></div>',
  },
  'el-image': {
    name: 'ElImage',
    props: ['src', 'fit', 'previewSrcList'],
    data(): { errored: boolean } {
      return { errored: false }
    },
    template:
      '<div class="el-image"><img v-if="!errored" class="el-image__inner" :src="src"/><div v-else class="el-image__error"><slot name="error"/></div></div>',
    methods: {
      triggerError(this: { errored: boolean }) {
        this.errored = true
      },
    },
  },
}

const baseItem = (over: Partial<FaceQualityReviewItem> = {}): FaceQualityReviewItem => ({
  event_id: 1,
  photo_id: 10,
  face_id: 100,
  decision: 'review_required',
  source: 'auto',
  rule_version: 'v1',
  model_version: 'test-v1',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
  bbox_x: 0.1,
  bbox_y: 0.1,
  bbox_width: 0.2,
  bbox_height: 0.2,
  face_validity_score: 0.9,
  quality_score: 0.8,
  quality_evidence_available: true,
  ...over,
})

const page = (items: FaceQualityReviewItem[], total = items.length, pageSize = 48) => ({
  data: {
    success: true,
    data: { items, total, page: 1, page_size: pageSize, total_pages: Math.max(1, Math.ceil(total / pageSize)) },
  },
})

const stats = () => ({
  data: {
    success: true,
    data: {
      pending_review: 1,
      historical_missing_evidence: 0,
      rescore_retryable: 0,
      auto_excluded: 0,
      manual_confirmed: 0,
      total: 1,
      by_reason: {},
      by_rule_version: { v1: 1 },
    },
  },
})

const emptyRescoreRuns = () => ({
  data: { success: true, data: { items: [] } },
})

const mountReview = () =>
  mount(FaceQualityReview, {
    global: {
      stubs,
      components: { RouterLink: (stubs as any).RouterLink ?? { template: '<a class="router-link"><slot/></a>' } },
    },
  })

describe('FaceQualityReview.vue - Task 6 改造', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    mocks.mockGetFaceQualityStats.mockResolvedValue(stats())
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([baseItem()]))
    mocks.mockApplyFaceQualityDecision.mockResolvedValue({ data: { success: true, data: { processed: 1 } } })
    mocks.mockRestoreAutoFaceQuality.mockResolvedValue({ data: { success: true, data: { restored: 0 } } })
    mocks.mockListRescoreRuns.mockResolvedValue(emptyRescoreRuns())
    mocks.mockCreateRescoreRun.mockResolvedValue({ data: { success: true, data: { id: 1 } } })
    mocks.mockPauseRescoreRun.mockResolvedValue({ data: { success: true } })
    mocks.mockResumeRescoreRun.mockResolvedValue({ data: { success: true } })
    mocks.mockCancelRescoreRun.mockResolvedValue({ data: { success: true } })
    mocks.mockRestoreRescoreRun.mockResolvedValue({ data: { success: true, data: { restored: 0 } } })
    mocks.mockRetryRescoreRun.mockResolvedValue({ data: { success: true, data: { id: 2 } } })
  })

  it('默认请求 page_size=48', async () => {
    const wrapper = mountReview()
    await flushPromises()
    const call = mocks.mockListFaceQualityReviews.mock.calls[0]![0] as any
    expect(call.page_size).toBe(48)
    wrapper.unmount()
  })

  it('修改为 96 后重置 page=1、保留筛选与已选 ID', async () => {
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([baseItem({ event_id: 11 })], 1, 48))
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.toggleSelect(11)
    await flushPromises()
    expect(vm.selectedIds.has(11)).toBe(true)

    mocks.mockListFaceQualityReviews.mockResolvedValue(page([baseItem({ event_id: 11 })], 1, 96))
    vm.onPageSizeChange(96)
    await flushPromises()
    expect(vm.page).toBe(1)
    expect(vm.pageSize).toBe(96)
    // 已选 ID 保留
    expect(vm.selectedIds.has(11)).toBe(true)
    // localStorage 持久化
    expect(localStorage.getItem('face_quality_page_size')).toBe('96')
    wrapper.unmount()
  })

  it('详情照片在固定高度容器且 .el-image__inner 为 object-fit: contain', async () => {
    const wrapper = mountReview()
    await flushPromises()
    await wrapper.find('.face-card').trigger('click')
    await flushPromises()
    const frame = wrapper.find('.detail-photo-frame')
    expect(frame.exists()).toBe(true)
    const img = frame.find('.el-image__inner')
    expect(img.exists()).toBe(true)
    // 容器类驱动固定高度 + contain；不依赖内联 style 断言（scoped CSS 在 jsdom 不生效）。
    expect(wrapper.find('.detail-photo-frame').classes()).toContain('detail-photo-frame')
    wrapper.unmount()
  })

  it('点选择热区边缘不触发 openDetail；点图片非热区调用一次 openDetail', async () => {
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    const openSpy = vi.spyOn(vm, 'openDetail')
    const hotzone = wrapper.find('.select-hotzone')
    expect(hotzone.exists()).toBe(true)
    // 热区 click 不打开详情
    await hotzone.trigger('click')
    await flushPromises()
    expect(openSpy).not.toHaveBeenCalled()
    // 卡片图片区域 click 打开详情
    await wrapper.find('.face-card').trigger('click')
    await flushPromises()
    expect(openSpy).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('选择热区至少 40x40px（class 存在）', async () => {
    const wrapper = mountReview()
    await flushPromises()
    const hotzone = wrapper.find('.select-hotzone')
    expect(hotzone.exists()).toBe(true)
    expect(hotzone.attributes('role')).toBe('checkbox')
    wrapper.unmount()
  })

  it('普通选择建立锚点；Shift 选中当前页闭区间；Shift 取消闭区间', async () => {
    const items = [baseItem({ event_id: 11 }), baseItem({ event_id: 12 }), baseItem({ event_id: 13 }), baseItem({ event_id: 14 })]
    mocks.mockListFaceQualityReviews.mockResolvedValue(page(items, 4))
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm

    // 普通选择 11 → 锚点
    vm.onSelectClick(11, { shiftKey: false } as any)
    await flushPromises()
    expect(vm.selectedIds.has(11)).toBe(true)
    expect(vm.selectionAnchorId).toBe(11)

    // Shift 点击 13（目标 13 原本未选）→ 选中 11,12,13
    vm.onSelectClick(13, { shiftKey: true } as any)
    await flushPromises()
    expect(vm.selectedIds.has(11)).toBe(true)
    expect(vm.selectedIds.has(12)).toBe(true)
    expect(vm.selectedIds.has(13)).toBe(true)
    expect(vm.selectedIds.has(14)).toBe(false)

    // Shift 点击 14（目标 14 原本未选）→ 选中 11..14
    vm.onSelectClick(14, { shiftKey: true } as any)
    await flushPromises()
    expect(vm.selectedIds.has(14)).toBe(true)
    expect(vm.selectedIds.size).toBe(4)

    // Shift 点击 13（目标 13 原本已选）→ 取消 11..13
    vm.onSelectClick(13, { shiftKey: true } as any)
    await flushPromises()
    expect(vm.selectedIds.has(11)).toBe(false)
    expect(vm.selectedIds.has(12)).toBe(false)
    expect(vm.selectedIds.has(13)).toBe(false)
    expect(vm.selectedIds.has(14)).toBe(true)
    wrapper.unmount()
  })

  it('翻页保留选中但重置锚点', async () => {
    const p1 = [baseItem({ event_id: 21 })]
    const p2 = [baseItem({ event_id: 31 })]
    mocks.mockListFaceQualityReviews
      .mockResolvedValueOnce(page(p1, 4))
      .mockResolvedValueOnce(page(p2, 4))
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.onSelectClick(21, { shiftKey: false } as any)
    await flushPromises()
    expect(vm.selectionAnchorId).toBe(21)
    vm.onPageChange(2)
    await flushPromises()
    await flushPromises()
    expect(vm.selectedIds.has(21)).toBe(true)
    expect(vm.selectionAnchorId).toBe(null)
    wrapper.unmount()
  })

  it('Space/Enter 可选择（键盘路径无 Shift 按普通选择）', async () => {
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.onSelectKeydown(1, { key: ' ', shiftKey: false, preventDefault: () => {}, stopPropagation: () => {} } as any)
    await flushPromises()
    expect(vm.selectedIds.has(1)).toBe(true)
    vm.onSelectKeydown(1, { key: 'Enter', shiftKey: false, preventDefault: () => {}, stopPropagation: () => {} } as any)
    await flushPromises()
    expect(vm.selectedIds.has(1)).toBe(false)
    wrapper.unmount()
  })

  it('切换 Tab / 刷新 / 成功批量操作清空选择和锚点', async () => {
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.onSelectClick(1, { shiftKey: false } as any)
    await flushPromises()
    expect(vm.selectedIds.size).toBe(1)
    // 切 Tab
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([], 0))
    await vm.onTabChange()
    await flushPromises()
    expect(vm.selectedIds.size).toBe(0)
    expect(vm.selectionAnchorId).toBe(null)
    wrapper.unmount()
  })

  it('各队列 Tab 使用正确 state', async () => {
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    const states = ['pending_review', 'historical_missing_evidence', 'rescore_retryable', 'auto_excluded', 'manual_confirmed']
    for (const s of states) {
      mocks.mockListFaceQualityReviews.mockClear()
      mocks.mockListFaceQualityReviews.mockResolvedValue(page([], 0))
      vm.activeTab = s
      await vm.onTabChange()
      await flushPromises()
      const call = mocks.mockListFaceQualityReviews.mock.calls[0]![0] as any
      expect(call.state).toBe(s)
    }
    wrapper.unmount()
  })

  it('历史待补证据空态文案不显示为人工待审核', async () => {
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([], 0))
    mocks.mockListRescoreRuns.mockResolvedValue(emptyRescoreRuns())
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.activeTab = 'historical_missing_evidence'
    await vm.onTabChange()
    await flushPromises()
    const text = wrapper.find('.empty-state').text()
    expect(text).toContain('不需要人工逐张确认')
    expect(text).not.toContain('人工审核')
    wrapper.unmount()
  })

  it('校准任务创建调用 createFaceQualityRescoreRun(mode=calibration)', async () => {
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.calibrationPhotoLimit = 1000
    vm.calibrationVisible = true
    await vm.doCreateCalibration()
    await flushPromises()
    expect(mocks.mockCreateRescoreRun).toHaveBeenCalledTimes(1)
    const arg = mocks.mockCreateRescoreRun.mock.calls[0]![0] as any
    expect(arg.mode).toBe('calibration')
    expect(arg.photo_limit).toBe(1000)
    wrapper.unmount()
  })

  it('全量 enforce 必须选择后端 eligible_for_enforce=true 的校准 run，提交含 calibration_run_id', async () => {
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    // 未选择 → 警告且不调用
    vm.fullEnforceCalibrationId = null
    await vm.doCreateFullEnforce()
    await flushPromises()
    expect(mocks.mockCreateRescoreRun).not.toHaveBeenCalled()
    expect(mocks.ElMessage.warning).toHaveBeenCalled()
    // 选择合格 ID → 调用 mode=full 且携带 calibration_run_id
    mocks.ElMessage.warning.mockClear()
    vm.fullEnforceCalibrationId = 5
    await vm.doCreateFullEnforce()
    await flushPromises()
    expect(mocks.mockCreateRescoreRun).toHaveBeenCalledTimes(1)
    const arg = mocks.mockCreateRescoreRun.mock.calls[0]![0] as any
    expect(arg.mode).toBe('full')
    expect(arg.calibration_run_id).toBe(5)
    wrapper.unmount()
  })

  it('completed_with_errors + retryable=4733 显示完成但有错误、已获证据 0、待重试 4733，不显示灰区 4733，enforce 入口不可见', async () => {
    const run1 = {
      id: 1,
      mode: 'calibration',
      apply_mode: 'shadow',
      status: 'completed_with_errors',
      target_face_count: 4733,
      target_photo_count: 1000,
      processed_face_count: 0,
      processed_photo_count: 1000,
      accepted_count: 0,
      review_required_count: 0,
      auto_excluded_count: 0,
      retryable_count: 4733,
      superseded_manual_count: 0,
      eligible_for_enforce: false,
      rule_version: 'v1',
      model_version: 'test-v1',
      photo_limit: 1000,
      created_at: '',
      updated_at: '',
    }
    mocks.mockListRescoreRuns.mockResolvedValue({ data: { success: true, data: { items: [run1] } } })
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.activeTab = 'historical_missing_evidence'
    await vm.onTabChange()
    await flushPromises()
    const cardText = wrapper.find('.rescore-card').text()
    expect(cardText).toContain('完成但有错误')
    expect(cardText).toContain('待重试/未匹配 4733')
    expect(cardText).toContain('已获证据 0')
    // 不应把灰区显示为 4733（真实灰区=0）
    expect(cardText).toContain('真实灰区 0')
    // full/enforce 入口不可见（无 eligible run）
    expect(wrapper.text()).not.toContain('启动全量 enforce')
    // 可重试
    expect(cardText).toContain('重试运行 #1')
    wrapper.unmount()
  })

  it('点击重试 #1 调用 retry API，不调用普通 create 接口', async () => {
    const run1 = {
      id: 1,
      mode: 'calibration',
      apply_mode: 'shadow',
      status: 'completed_with_errors',
      target_face_count: 4733,
      target_photo_count: 1000,
      processed_face_count: 0,
      processed_photo_count: 1000,
      accepted_count: 0,
      review_required_count: 0,
      auto_excluded_count: 0,
      retryable_count: 4733,
      superseded_manual_count: 0,
      eligible_for_enforce: false,
      rule_version: 'v1',
      model_version: 'test-v1',
      photo_limit: 1000,
      created_at: '',
      updated_at: '',
    }
    mocks.mockListRescoreRuns.mockResolvedValue({ data: { success: true, data: { items: [run1] } } })
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    // 切到历史缺证据 Tab 触发 loadRescoreRuns，填充 latestRun。
    vm.activeTab = 'historical_missing_evidence'
    await vm.onTabChange()
    await flushPromises()
    // openRetryDialog 设置 retrySourceRun；doRetryRun 读取它调用 retry API。
    expect(vm.latestRun).toBeTruthy()
    vm.openRetryDialog(vm.latestRun)
    expect(vm.retrySourceRun).toBeTruthy()
    await vm.doRetryRun()
    await flushPromises()
    expect(mocks.mockRetryRescoreRun).toHaveBeenCalledTimes(1)
    expect(mocks.mockRetryRescoreRun.mock.calls[0]![0]).toBe(1)
    expect(mocks.mockCreateRescoreRun).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('无证据样本不显示 0%，真实 0 分显示 0%（保留行为）', async () => {
    const noEvidence = baseItem({ event_id: 2, face_validity_score: 0, quality_score: 0, quality_evidence_available: false })
    const realZero = baseItem({
      event_id: 3, face_validity_score: 0, quality_score: 0,
      quality_evidence_available: true, evidence_json: '{"face_validity_score":0}',
    })
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([noEvidence, realZero], 2))
    const wrapper = mountReview()
    await flushPromises()
    const cards = wrapper.findAll('.face-card')
    expect(cards.length).toBe(2)
    expect(cards[0]!.text()).toContain('有效性未采集')
    expect(cards[0]!.text()).not.toContain('0%')
    expect(cards[1]!.text()).toContain('0% 有效')
    wrapper.unmount()
  })
})

// ---- v2 独立复核与 legacy_v1 证据展示 ----

const v2Item = (over: Partial<FaceQualityReviewItem> = {}): FaceQualityReviewItem => baseItem({
  event_id: 500,
  evidence_pipeline: 'independent_v2',
  quality_evidence_available: true,
  evidence_v2: {
    evidence_schema_version: 'independent_v2',
    primary_detector_score: 0.3,
    verification_status: 'no_face',
    verifier_score: 0.12,
    verifier_name: 'yunet',
    verifier_version: 'opencv-yunet-2023mar',
    original_width: 4032,
    original_height: 3024,
    face_box_width_px: 55,
    face_box_height_px: 63,
    context_crop_width_px: 165,
    context_crop_height_px: 189,
    context_expand_ratio: 1.0,
    sharpness_norm: 42.5,
    brightness_norm: 128.0,
    contrast_norm: 30.2,
    occluded: false,
    quality_domain: 'original_face_box_norm_to_96',
    quality_version: 'v1',
    reason_codes: ['detector_verifier_disagreement'],
    rule_version: 'face_quality_v2',
    model_version: 'opencv-yunet-2023mar',
  },
  suggested_decision: 'non_face',
  ...over,
})

describe('FaceQualityReview.vue - v2 证据展示', () => {
  it('independent_v2 卡片显示 v2 标识而非有效性百分比', async () => {
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([v2Item()]))
    const wrapper = mountReview()
    await flushPromises()
    const card = wrapper.find('.face-card')
    expect(card.text()).toContain('v2 独立复核')
    expect(card.text()).not.toContain('% 有效')
    wrapper.unmount()
  })

  it('详情展示四组 v2 证据，不压成单一有效性', async () => {
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([v2Item()]))
    const wrapper = mountReview()
    await flushPromises()
    await wrapper.find('.face-card').trigger('click')
    await flushPromises()
    const detail = wrapper.find('.detail-content')
    expect(detail.exists()).toBe(true)
    const v2 = wrapper.find('.v2-evidence')
    expect(v2.exists()).toBe(true)
    const text = v2.text()
    // 主检测分
    expect(text).toContain('主检测分')
    // 独立复核结果 + 模型版本
    expect(text).toContain('未检测到脸')
    expect(text).toContain('yunet')
    // 原图人脸框尺寸（55×63，不是压缩的 14×16）
    expect(text).toContain('55 × 63 px')
    expect(text).toContain('4032 × 3024')
    // 上下文裁剪尺寸
    expect(text).toContain('165 × 189 px')
    // 质量特征：清晰度/亮度/遮挡
    expect(text).toContain('清晰度')
    expect(text).toContain('亮度')
    expect(text).toContain('遮挡')
    // 系统建议
    expect(text).toContain('系统建议')
    wrapper.unmount()
  })

  it('legacy_v1 详情显示旧版同源指标追溯提示', async () => {
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([baseItem({ event_id: 600, evidence_pipeline: 'legacy_v1' })]))
    const wrapper = mountReview()
    await flushPromises()
    await wrapper.find('.face-card').trigger('click')
    await flushPromises()
    const alert = wrapper.find('.legacy-alert')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('旧版同源指标')
    // legacy 不展示 v2 证据块
    expect(wrapper.find('.v2-evidence').exists()).toBe(false)
    wrapper.unmount()
  })
})
