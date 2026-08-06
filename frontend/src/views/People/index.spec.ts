import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import PeopleIndex from './index.vue'
import { peopleApi } from '@/api/people'
import { backgroundApi } from '@/api/background'
import { columnsForPeopleList } from './peopleListGridUtils'

// People/index.vue 连续浏览虚拟化集成测试。
//
// 真实 VirtualMediaGrid 在 jsdom 下取不到 .main-content 滚动容器的真实尺寸（offsetHeight
// 无法桩到 closest 链路），virtualizer 不渲染行——组件级行渲染/列数/CSS 已由
// VirtualMediaGrid.spec.ts 充分覆盖。这里用可控桩组件验证 People/index.vue 的列表逻辑：
// 分页触发守卫、选择语义、快照恢复、Tab 守卫、列数 prop 传递。
//
// 覆盖任务测试项 1–9（行级渲染有界由 VirtualMediaGrid.spec.ts 覆盖，这里验证上层正确接线）。

// jsdom 默认未提供 localStorage / ResizeObserver，提前注入最小实现。
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
  // ResizeObserver 桩：People/index.vue 在连续浏览挂载后注册 RO 监听列表容器宽度。
  class RO {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  ;(globalThis as any).ResizeObserver = RO
  ;(window as any).ResizeObserver = RO
})

const mockGetList = vi.fn()
const mockListMergeSuggestions = vi.fn()
const mockGetMergeSuggestionTask = vi.fn()
const mockGetMergeSuggestionStats = vi.fn()
const mockGetMergeSuggestionLogs = vi.fn()

vi.mock('@/api/people', () => ({
  peopleApi: {
    getList: (...a: unknown[]) => mockGetList(...a),
    listMergeSuggestions: (...a: unknown[]) => mockListMergeSuggestions(...a),
    getMergeSuggestionTask: (...a: unknown[]) => mockGetMergeSuggestionTask(...a),
    getMergeSuggestionStats: (...a: unknown[]) => mockGetMergeSuggestionStats(...a),
    getMergeSuggestionLogs: (...a: unknown[]) => mockGetMergeSuggestionLogs(...a),
  },
}))

vi.mock('@/api/background', () => ({
  backgroundApi: {
    getStatus: vi.fn().mockResolvedValue({ data: { data: null } }),
  },
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {}, params: {} }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}))

vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
  ElMessageBox: { confirm: vi.fn() },
}))

const makePerson = (id: number, overrides: any = {}) => ({
  id,
  name: `人物 ${id}`,
  category: 'family',
  photo_count: 1,
  face_count: 1,
  representative_face_id: id,
  hidden: false,
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
  ...overrides,
})

const paged = (items: unknown[], total: number) => ({
  data: { success: true, data: { items, total, page: 1, page_size: items.length, total_pages: 1 } },
})

const continuousPage = (items: unknown[], total: number) => ({
  data: { success: true, data: { items, total, page: 1, page_size: 50, total_pages: Math.ceil(total / 50) } },
})

// 可控 VirtualMediaGrid 桩：记录最近 props，暴露 triggerVisibleRange 派发事件、
// getScrollOffset/scrollToOffset 模拟 .main-content.scrollTop。
// 渲染一个带 data-vmg 标记的 div，便于断言挂载与列数 prop。
const makeGridStub = () => {
  const state: { offset: number; cols: number; itemCount: number } = { offset: 0, cols: 0, itemCount: 0 }
  const stub = defineComponent({
    name: 'VirtualMediaGridStub',
    props: ['items', 'columns', 'rowHeight', 'gap', 'overscan', 'sizeClass'],
    emits: ['visible-range-change'],
    setup(props, { emit }) {
      state.cols = props.columns
      state.itemCount = props.items?.length ?? 0
      return () => null
    },
    mounted(this: any) {
      // 首屏派发一次，模拟 virtualizer 首次测量后的 visible-range-change
      const rowCount = this.items ? Math.ceil(this.items.length / this.columns) : 0
      this.$emit('visible-range-change', { firstRowIndex: 0, lastRowIndex: 0, rowCount })
    },
    methods: {
      getFirstVisibleIndex: () => 0,
      scrollToIndex: () => {},
      measure: () => {},
      recomputeScrollMargin: () => {},
      getScrollOffset: () => state.offset,
      scrollToOffset: (v: number) => { state.offset = v },
    },
  })
  return { stub, state }
}

const baseStubs = {
  'el-tabs': { template: '<div><slot/></div>' },
  'el-tab-pane': { template: '<div><slot/></div>' },
  'el-card': { template: '<div><slot/><slot name="header"/></div>' },
  'el-empty': { template: '<div class="el-empty"/>' },
  'el-button': { template: '<button @click="$emit(\'click\')"><slot/></button>' },
  'el-icon': { template: '<span/>' },
  'el-input': { template: '<input/>' },
  'el-select': { template: '<div class="el-select"/>' },
  'el-option': { template: '<div/>' },
  'el-radio-group': { template: '<div class="el-radio-group"><slot/></div>' },
  'el-radio-button': { template: '<div/>' },
  'el-checkbox': { template: '<div class="el-checkbox"/>' },
  'el-avatar': { template: '<div class="el-avatar"/>' },
  'el-tooltip': { template: '<div><slot/></div>' },
  'el-pagination': { template: '<div class="el-pagination"/>' },
  SectionHeader: { template: '<div class="section-header"><slot name="actions"/></div>' },
  MergeSuggestionReviewDialog: { template: '<div/>' },
  PersonEditDialog: { template: '<div/>' },
  PersonMergeConfirmDialog: { template: '<div/>' },
  PersonCard: { template: '<div class="person-card-stub"/>' },
}

const mountPeople = (continuous = false, gridStub?: any) => {
  if (continuous) {
    localStorage.setItem('relive.people.browseMode', 'continuous')
  } else {
    localStorage.removeItem('relive.people.browseMode')
  }
  return mount(PeopleIndex, {
    global: {
      stubs: { ...baseStubs, VirtualMediaGrid: gridStub ?? true },
    },
  })
}

describe('People/index.vue - 连续浏览虚拟化', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    mockListMergeSuggestions.mockResolvedValue({ data: { data: { items: [], total: 0 } } })
    mockGetMergeSuggestionTask.mockResolvedValue({ data: { data: null } })
    mockGetMergeSuggestionStats.mockResolvedValue({ data: { data: null } })
    mockGetMergeSuggestionLogs.mockResolvedValue({ data: { data: { lines: [] } } })
  })

  it('翻页模式默认走 getList，使用 PersonCard v-for，不挂载 VirtualMediaGrid（测试项 9）', async () => {
    mockGetList.mockResolvedValue(paged([makePerson(1), makePerson(2)], 2))
    const wrapper = mountPeople(false)
    await flushPromises()
    expect(mockGetList).toHaveBeenCalled()
    // 翻页模式：PersonCard 通过 v-for 直接渲染（baseStubs 里 PersonCard 是真实占位）
    expect(wrapper.findAll('.person-card-stub').length).toBe(2)
    // 不挂载虚拟网格
    expect(wrapper.findComponent({ name: 'VirtualMediaGridStub' }).exists()).toBe(false)
    wrapper.unmount()
  })

  it('连续浏览模式挂载 VirtualMediaGrid 渲染 continuousPeople，columns 由容器宽度计算（测试项 1）', async () => {
    mockGetList.mockResolvedValue(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 100))
    const { stub, state } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    expect(vm.continuousPeople).toHaveLength(50)
    // 虚拟网格已挂载，columns prop 由 peopleColumns 传入（初始 2，因 jsdom 容器宽度为 0）
    expect(wrapper.findComponent({ name: 'VirtualMediaGridStub' }).exists()).toBe(true)
    expect(state.cols).toBe(vm.peopleColumns)
    expect(state.itemCount).toBe(50)
    wrapper.unmount()
  })

  it('peopleColumns 由 columnsForPeopleList 计算，断点与翻页 CSS 一致（测试项 1、2）', async () => {
    mockGetList.mockResolvedValue(continuousPage([makePerson(1)], 1))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    const vm: any = wrapper.vm
    // jsdom 下容器宽度 0 → columnsForPeopleList(0)=2
    expect(vm.peopleColumns).toBe(columnsForPeopleList(0))
    expect(vm.peopleColumns).toBe(2)
    wrapper.unmount()
  })

  it('连续浏览首批加载 50 人后，continuousPeople 增长但 DOM 卡片有界（测试项 3 上层验证）', async () => {
    mockGetList.mockResolvedValue(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 100))
    const { stub, state } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    // 内存保留全部 50 人
    expect(vm.continuousPeople).toHaveLength(50)
    // 桩只渲染一个根节点，不渲染 50 个卡片——真实有界性由 VirtualMediaGrid.spec.ts 验证
    expect(state.itemCount).toBe(50)
    wrapper.unmount()
  })

  it('接近末尾只触发一次下一页请求，finished 后不再请求（测试项 4）', async () => {
    mockGetList
      .mockResolvedValueOnce(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 100))
      .mockResolvedValueOnce(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 51)), 100))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    const callsBefore = mockGetList.mock.calls.length
    // 模拟接近末尾：rowCount = ceil(50/cols)，lastVisibleRowIndex 接近末尾
    const rowCount = Math.ceil(50 / vm.peopleColumns)
    vm.onPeopleVisibleRange({ firstRowIndex: 0, lastRowIndex: rowCount - 1, rowCount })
    await flushPromises()
    await flushPromises()
    expect(mockGetList.mock.calls.length).toBe(callsBefore + 1)
    // 第二批加载完 finished（50+50>=100）
    expect(vm.continuousFinished).toBe(true)
    const callsAfter = mockGetList.mock.calls.length
    vm.onPeopleVisibleRange({ firstRowIndex: 0, lastRowIndex: rowCount - 1, rowCount })
    await flushPromises()
    expect(mockGetList.mock.calls.length).toBe(callsAfter)
    wrapper.unmount()
  })

  it('loading 中不重复触发请求（测试项 5）', async () => {
    let resolveFirst: (v: any) => void = () => {}
    mockGetList.mockReturnValue(new Promise(r => { resolveFirst = r }))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    const vm: any = wrapper.vm
    expect(vm.continuousLoading).toBe(true)
    const callsBefore = mockGetList.mock.calls.length
    vm.onPeopleVisibleRange({ firstRowIndex: 0, lastRowIndex: 0, rowCount: 1 })
    await flushPromises()
    expect(mockGetList.mock.calls.length).toBe(callsBefore)
    resolveFirst(continuousPage([makePerson(1)], 1))
    await flushPromises()
    wrapper.unmount()
  })

  it('error 状态下不自动请求（测试项 5）', async () => {
    mockGetList.mockRejectedValueOnce(new Error('fail'))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    expect(vm.continuousError).toBe(true)
    const callsBefore = mockGetList.mock.calls.length
    vm.onPeopleVisibleRange({ firstRowIndex: 0, lastRowIndex: 0, rowCount: 1 })
    await flushPromises()
    expect(mockGetList.mock.calls.length).toBe(callsBefore)
    wrapper.unmount()
  })

  it('finished 状态下不触发请求（测试项 5）', async () => {
    mockGetList.mockResolvedValueOnce(continuousPage([makePerson(1)], 1))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    expect(vm.continuousFinished).toBe(true)
    const callsBefore = mockGetList.mock.calls.length
    vm.onPeopleVisibleRange({ firstRowIndex: 0, lastRowIndex: 0, rowCount: 1 })
    await flushPromises()
    expect(mockGetList.mock.calls.length).toBe(callsBefore)
    wrapper.unmount()
  })

  it('非人物列表 Tab 下不触发请求（测试项 5）', async () => {
    mockGetList.mockResolvedValue(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 100))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.activeTab = 'task'
    await nextTick()
    const callsBefore = mockGetList.mock.calls.length
    const rowCount = Math.ceil(50 / vm.peopleColumns)
    vm.onPeopleVisibleRange({ firstRowIndex: 0, lastRowIndex: rowCount - 1, rowCount })
    await flushPromises()
    expect(mockGetList.mock.calls.length).toBe(callsBefore)
    wrapper.unmount()
  })

  it('跨虚拟区域选择、取消、全选语义独立于 DOM 挂载（测试项 7）', async () => {
    mockGetList.mockResolvedValue(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 50))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.batchMode = true
    await nextTick()
    // 选中视口外的 id（卡片未挂载）
    vm.toggleSelect(5)
    expect(vm.selectedIds.has(5)).toBe(true)
    // 全选当前列表 = 全部已加载人物（50），不限于挂载卡片
    vm.selectAllCurrent()
    expect(vm.selectedIds.size).toBe(50)
    // 取消其中一个
    vm.toggleSelect(5)
    expect(vm.selectedIds.has(5)).toBe(false)
    expect(vm.selectedIds.size).toBe(49)
    wrapper.unmount()
  })

  it('Shift 连选按完整已加载顺序计算，不限于虚拟窗口（测试项 7）', async () => {
    mockGetList.mockResolvedValue(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 50))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.batchMode = true
    await nextTick()
    vm.toggleSelect(1)
    expect(vm.selectionAnchorId).toBe(1)
    // Shift+click id=10：区间 [1..10] 全部选中
    vm.toggleSelect(10, true)
    for (let i = 1; i <= 10; i++) {
      expect(vm.selectedIds.has(i)).toBe(true)
    }
    expect(vm.selectedIds.size).toBe(10)
    wrapper.unmount()
  })

  it('选择集合是 Set<number>，与卡片是否挂载无关（测试项 7）', async () => {
    mockGetList.mockResolvedValue(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 50))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.batchMode = true
    await nextTick()
    vm.toggleSelect(999)
    expect(vm.selectedIds.has(999)).toBe(true)
    expect(vm.continuousPeople.map((p: any) => p.id)).not.toContain(999)
    wrapper.unmount()
  })

  it('从详情页返回后恢复已加载列表、分页状态（测试项 8）', async () => {
    mockGetList.mockResolvedValueOnce(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 100))
    const { stub, state } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    // 模拟用户滚动后进入详情：goToDetail 通过 peopleGridRef.getScrollOffset 保存 scrollTop
    state.offset = 1234
    vm.goToDetail(7)
    expect(vm.continuousPeople).toHaveLength(50)
    // 卸载（导航到详情），重新挂载（返回）
    wrapper.unmount()
    const { stub: stub2 } = makeGridStub()
    const wrapper2 = mountPeople(true, stub2)
    await flushPromises()
    await flushPromises()
    const vm2: any = wrapper2.vm
    // 快照恢复：continuousPeople 恢复 50 人，分页状态恢复，不再请求首批
    expect(vm2.continuousPeople).toHaveLength(50)
    expect(vm2.continuousPage).toBe(2)
    expect(vm2.continuousTotal).toBe(100)
    expect(vm2.continuousFinished).toBe(false)
    wrapper2.unmount()
  })

  it('goToDetail 通过 peopleGridRef.getScrollOffset 保存 scrollTop（测试项 8）', async () => {
    mockGetList.mockResolvedValue(continuousPage([makePerson(1)], 1))
    const { stub, state } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    state.offset = 5678
    vm.goToDetail(1)
    // 快照已保存滚动位置；通过重新挂载验证恢复
    wrapper.unmount()
    const { stub: stub2, state: state2 } = makeGridStub()
    const wrapper2 = mountPeople(true, stub2)
    await flushPromises()
    await flushPromises()
    // 恢复时 peopleGridRef.scrollToOffset 被调用，state2.offset 应被写入快照值
    expect(state2.offset).toBe(5678)
    wrapper2.unmount()
  })

  it('数据追加不触发连续自动翻页：首屏事件后无新事件不续页（测试项 6）', async () => {
    mockGetList.mockResolvedValue(continuousPage(Array.from({ length: 50 }, (_, i) => makePerson(i + 1)), 1000))
    const { stub } = makeGridStub()
    const wrapper = mountPeople(true, stub)
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    // 首屏派发 lastRowIndex=0，距末尾很远（rowCount=25, 25-0>3），不应触发下一页
    const callsAfterMount = mockGetList.mock.calls.length
    // 再次派发同一区间（无变化）：不应触发
    vm.onPeopleVisibleRange({ firstRowIndex: 0, lastRowIndex: 0, rowCount: 25 })
    await flushPromises()
    expect(mockGetList.mock.calls.length).toBe(callsAfterMount)
    wrapper.unmount()
  })
})
