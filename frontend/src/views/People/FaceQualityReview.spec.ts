import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import FaceQualityReview from './FaceQualityReview.vue'
import type { FaceQualityReviewItem } from '@/types/people'

// 人脸质检审核页三项修复回归：
// 1. 详情照片 URL 走受保护的 /photos/:id/thumbnail?v=<event_id>，不再拼接 ../ 相对路径；
//    加载失败显示“照片缩略图不可用”与“查看照片详情”入口。
// 2. 本页全选/反选/清空选择，三态复选框，跨页累积；Tab/筛选/刷新/批量动作清空选择。
// 3. 按 quality_evidence_available 区分“真实 0 分”与“未采集”，无证据不显示 0%。

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
  }
})

vi.mock('@/api/people', () => ({
  peopleApi: {
    getFaceQualityStats: (...a: unknown[]) => mocks.mockGetFaceQualityStats(...a),
    listFaceQualityReviews: (...a: unknown[]) => mocks.mockListFaceQualityReviews(...a),
    applyFaceQualityDecision: (...a: unknown[]) => mocks.mockApplyFaceQualityDecision(...a),
    restoreAutoFaceQuality: (...a: unknown[]) => mocks.mockRestoreAutoFaceQuality(...a),
  },
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {}, params: {} }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  // 全局 router-link 桩：审核页详情失败态用到 <router-link>。
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

// Element Plus 桩：el-image 用原生 <img> 以便断言 src；
// 通过 props 暴露 error 状态，便于触发失败态插槽。
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
  'el-input-number': { template: '<div/>' },
  'el-image': {
    name: 'ElImage',
    props: ['src', 'fit'],
    data(): { errored: boolean } {
      return { errored: false }
    },
    template:
      '<div class="el-image"><img v-if="!errored" class="el-image__img" :src="src"/><div v-else class="el-image__error"><slot name="error"/></div></div>',
    // 暴露方法让测试主动触发失败态
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

const page = (items: FaceQualityReviewItem[], total = items.length) => ({
  data: {
    success: true,
    data: { items, total, page: 1, page_size: 24, total_pages: Math.max(1, Math.ceil(total / 24)) },
  },
})

const stats = () => ({
  data: {
    success: true,
    data: {
      pending_review: 1,
      auto_excluded: 0,
      manual_confirmed: 0,
      total: 1,
      by_reason: {},
      by_rule_version: { v1: 1 },
    },
  },
})

const mountReview = () =>
  mount(FaceQualityReview, {
    global: {
      stubs,
      // router-link 在组件模板里使用；vue-router mock 已提供 RouterLink，
      // 但模板写的是 <router-link>，需通过 components 注册对应桩。
      components: { RouterLink: (stubs as any).RouterLink ?? { template: '<a class="router-link"><slot/></a>' } },
    },
  })

describe('FaceQualityReview.vue - 三项修复', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.mockGetFaceQualityStats.mockResolvedValue(stats())
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([baseItem()]))
    mocks.mockApplyFaceQualityDecision.mockResolvedValue({ data: { success: true, data: { processed: 1 } } })
    mocks.mockRestoreAutoFaceQuality.mockResolvedValue({ data: { success: true, data: { restored: 0, rule_version: 'v1' } } })
  })

  it('详情照片 URL 为 /api/v1/photos/<photo_id>/thumbnail?v=<event_id>，不含 ../ 或本地路径', async () => {
    const wrapper = mountReview()
    await flushPromises()
    // 打开详情抽屉
    await wrapper.find('.face-card').trigger('click')
    await flushPromises()
    const img = wrapper.find('.detail-photo-wrap .el-image__img')
    expect(img.exists()).toBe(true)
    const src = img.attributes('src') || ''
    expect(src).toBe('/api/v1/photos/10/thumbnail?v=1')
    expect(src).not.toContain('../')
    expect(src).not.toContain('photo_thumbnail')
    expect(src).not.toContain('.jpg')
    wrapper.unmount()
  })

    it('照片缩略图加载失败时显示“照片缩略图不可用”与照片详情链接', async () => {
    const wrapper = mountReview()
    await flushPromises()
    await wrapper.find('.face-card').trigger('click')
    await flushPromises()
    const image = wrapper.findComponent({ name: 'ElImage' })
    // 触发错误态：el-image 桩切换到 error 插槽
    ;(image.vm as any).triggerError()
    await flushPromises()
    expect(wrapper.text()).toContain('照片缩略图不可用')
    expect(wrapper.text()).toContain('查看照片详情')
    const link = wrapper.find('.photo-detail-link')
    expect(link.exists()).toBe(true)
    // RouterLink 桩用 :to 绑定 href；断言链接目标指向 /photos/10
    expect(link.attributes('href') || link.attributes('to')).toBe('/photos/10')
    wrapper.unmount()
  })

  it('无证据样本显示“有效性未采集”/“未采集（历史人脸）”，不显示 0%；真实 0 分有证据显示 0%', async () => {
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
    // 卡片底部：无证据 → “有效性未采集”，不应出现 0%
    expect(cards[0]!.text()).toContain('有效性未采集')
    expect(cards[0]!.text()).not.toContain('0%')
    // 真实 0 分有证据 → 0%
    expect(cards[1]!.text()).toContain('0% 有效')

    // 打开无证据样本详情
    await cards[0]!.trigger('click')
    await flushPromises()
    const detailText = wrapper.find('.el-drawer').text()
    expect(detailText).toContain('未采集（历史人脸）')
    expect(detailText).toContain('质量分')
    // 证据区块与原因码区块在无证据时不渲染
    expect(wrapper.find('.evidence-json').exists()).toBe(false)
    wrapper.unmount()
  })

  it('全选本页选中全部 items；部分取消后全选框为半选；反选只反转本页；清空清除全部', async () => {
    const items = [baseItem({ event_id: 11 }), baseItem({ event_id: 12 }), baseItem({ event_id: 13 })]
    mocks.mockListFaceQualityReviews.mockResolvedValue(page(items, 3))
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm

    // 全选本页（直接调方法，避免 checkbox 桩 click 与 toggleSelectAllPage 的 checked 语义歧义）
    vm.toggleSelectAllPage(true)
    await flushPromises()
    expect(vm.selectedIds.size).toBe(3)
    expect(vm.selectedIds.has(11)).toBe(true)
    expect(vm.selectedIds.has(12)).toBe(true)
    expect(vm.selectedIds.has(13)).toBe(true)
    // 全选后三态：全选 true、半选 false
    expect(vm.pageSelectAll).toBe(true)
    expect(vm.pageSelectIndeterminate).toBe(false)

    // 取消其中一个 → 半选；此时全选本页复选框仍可见（与 batch-bar 同一工具栏，不互斥）
    vm.toggleSelect(12)
    await flushPromises()
    expect(vm.pageSelectAll).toBe(false)
    expect(vm.pageSelectIndeterminate).toBe(true)
    expect(vm.selectedIds.has(12)).toBe(false)
    expect(vm.selectedIds.size).toBe(2)
    // 半选态下全选本页控件仍在 DOM 中可操作（不因有选中项而隐藏）
    expect(wrapper.find('.page-select .el-checkbox').exists()).toBe(true)

    // 通过真实 @change 事件契约补齐全选（验证 checkbox change → toggleSelectAllPage(true)）
    await wrapper.find('.page-select .el-checkbox').trigger('click')
    await flushPromises()
    expect(vm.selectedIds.has(12)).toBe(true)
    expect(vm.pageSelectAll).toBe(true)

    // 反选本页：三张全部反转
    vm.invertSelectPage()
    await flushPromises()
    expect(vm.selectedIds.has(11)).toBe(false)
    expect(vm.selectedIds.has(12)).toBe(false)
    expect(vm.selectedIds.has(13)).toBe(false)

    // 清空选择
    vm.clearSelection()
    await flushPromises()
    expect(vm.selectedIds.size).toBe(0)
    wrapper.unmount()
  })

  it('翻页保留上一页选择；Tab/刷新/成功批量动作清空选择；提交 ID 含跨页累计且无重复', async () => {
    const page1 = [baseItem({ event_id: 21 }), baseItem({ event_id: 22 })]
    const page2 = [baseItem({ event_id: 31 }), baseItem({ event_id: 32 })]
    mocks.mockListFaceQualityReviews
      .mockResolvedValueOnce(page(page1, 4))
      .mockResolvedValueOnce(page(page2, 4))

    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm

    // 选中第 1 页一项，翻页保留
    vm.toggleSelect(21)
    await flushPromises()
    expect(vm.selectedIds.has(21)).toBe(true)

    // 翻页：onPageChange 不清空选择
    vm.onPageChange(2)
    await flushPromises()
    await flushPromises()
    expect(vm.selectedIds.has(21)).toBe(true) // 上一页选择保留
    expect(vm.page).toBe(2)

    // 第 2 页再选一项，跨页累积
    vm.toggleSelect(31)
    await flushPromises()
    expect(vm.selectedIds.size).toBe(2)

    // 批量提交：applyFaceQualityDecision 收到跨页累计且无重复的 ID
    mocks.mockListFaceQualityReviews.mockResolvedValueOnce(page([], 0))
    await vm.batchAction('mark_non_face')
    await flushPromises()
    expect(mocks.mockApplyFaceQualityDecision).toHaveBeenCalledTimes(1)
    const submitted = mocks.mockApplyFaceQualityDecision.mock.calls[0]![0] as number[]
    expect(submitted.sort((a, b) => a - b)).toEqual([21, 31])
    // 成功后清空选择
    expect(vm.selectedIds.size).toBe(0)
    wrapper.unmount()
  })

  it('切换 Tab 清空选择', async () => {
    const items = [baseItem({ event_id: 41 }), baseItem({ event_id: 42 })]
    mocks.mockListFaceQualityReviews.mockResolvedValue(page(items, 2))
    const wrapper = mountReview()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.toggleSelect(41)
    await flushPromises()
    expect(vm.selectedIds.size).toBe(1)
    mocks.mockListFaceQualityReviews.mockResolvedValue(page([], 0))
    await vm.onTabChange()
    await flushPromises()
    expect(vm.selectedIds.size).toBe(0)
    wrapper.unmount()
  })
})
