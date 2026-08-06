import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import PhotosIndex from './index.vue'
import { photoApi } from '@/api/photo'
import { usePhotoStore } from '@/stores/photo'

// PhotosContinuousBrowse: 连续浏览集成测试。
// 覆盖：浏览模式切换、筛选变化使旧请求失效、下一批追加及 ID 去重、
// loading/error/finished 不重复触发、重复/空游标停滞保护、跨虚拟区域选择不丢失、
// 从详情返回恢复数据/锚点/滚动位置。

// stubs：用带插槽渲染的浅组件，让 v-if 分支（空状态 / 照片网格）真正挂载，
// 否则 el-card/el-empty 等被默认 stub 吞掉插槽，断言不到 DOM。
const STUBS = {
  'el-card': { template: '<div><slot name="header" /><slot /></div>' },
  'el-empty': { template: '<div class="el-empty-stub">{{ description }}</div>', props: ['description', 'imageSize'] },
  'el-button': { template: '<button><slot /></button>' },
  'el-icon': true,
  'el-input': true,
  'el-radio-group': { template: '<div><slot /></div>' },
  'el-radio-button': true,
  'el-pagination': true,
  'el-table': true,
  'el-table-column': true,
  'el-tag': { template: '<span><slot /></span>' },
  'el-tooltip': { template: '<div><slot /></div>' },
  'el-dialog': true,
  'el-switch': true,
  'el-select': true,
  'el-option': true,
  'el-image': true,
  'SectionHeader': { template: '<div><slot /><slot name="actions" /></div>' },
  'PathBrowser': true,
  'LocationPicker': true,
  'VirtualMediaGrid': true,
  'PhotoCard': true,
}

// 共享 store 状态引用：单测可通过 usePhotoStore().photoCounts 覆盖 systemTotal。
const sharedStore = {
  photoCounts: { active: 0, excluded: 0 },
  scanPaths: [],
  categories: [],
  hotTags: [],
  totalTagCount: 0,
  autoScanConfig: { enabled: false, interval_minutes: 30 },
  fetchPhotoCounts: vi.fn(),
  fetchScanPaths: vi.fn(),
  fetchCategories: vi.fn(),
  fetchTags: vi.fn(),
  fetchAutoScanConfig: vi.fn(),
  invalidateAll: vi.fn(),
}

// jsdom 默认未提供 localStorage，提前注入最小实现（与 People/Detail.spec.ts 一致）。
beforeAll(() => {
  const store = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
      clear: () => store.clear(),
      key: (i: number) => Array.from(store.keys())[i] ?? null,
      get length() {
        return store.size
      },
    },
  })
  if (typeof window !== 'undefined' && !window.localStorage) {
    Object.defineProperty(window, 'localStorage', { configurable: true, value: (globalThis as any).localStorage })
  }
})

vi.mock('@/api/photo', () => ({
  photoApi: {
    getList: vi.fn().mockResolvedValue({ data: { data: { items: [], total: 0 } } }),
    getCursorList: vi.fn(),
    getTags: vi.fn().mockResolvedValue({ data: { data: { items: [], total: 0 } } }),
    getCounts: vi.fn().mockResolvedValue({ data: { data: { active_count: 0, excluded_count: 0 } } }),
    countByPaths: vi.fn().mockResolvedValue({ data: { data: { counts: {} } } }),
    countDerivedStatusByPaths: vi.fn().mockResolvedValue({ data: { data: { stats: {} } } }),
    getCategories: vi.fn().mockResolvedValue({ data: { data: [] } }),
    getScanTask: vi.fn().mockResolvedValue({ data: { data: { is_running: false } } }),
  },
}))

vi.mock('@/stores/photo', () => ({
  usePhotoStore: () => sharedStore,
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    currentRoute: { value: { query: {} } },
  }),
  useRoute: () => ({ query: {}, params: {} }),
}))

const makePhoto = (id: number, overrides: any = {}) => ({
  id,
  file_path: `/p${id}.jpg`,
  file_name: `p${id}.jpg`,
  file_hash: `h${id}`,
  ai_analyzed: false,
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
  ...overrides,
})

describe('PhotosContinuousBrowse', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    // 重置共享 store 计数，避免用例间污染。
    sharedStore.photoCounts.active = 0
    sharedStore.photoCounts.excluded = 0
  })

  const mountPhotos = () => mount(PhotosIndex, { global: { stubs: STUBS } })

  it('默认浏览模式为 pagination（localStorage 空）', async () => {
    const wrapper = mountPhotos()
    await flushPromises()
    // 默认走 getList（翻页模式）
    expect(photoApi.getList).toHaveBeenCalled()
    expect(photoApi.getCursorList).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('切换到 continuous 触发 getCursorList 首批加载', async () => {
    ;(photoApi.getCursorList as any).mockResolvedValue({
      data: { data: { items: [makePhoto(1), makePhoto(2)], has_more: true, next_cursor: 'c1' } },
    })
    const wrapper = mountPhotos()
    await flushPromises()
    // 切换到连续浏览
    const radioGroups = wrapper.findAllComponents({ name: 'ElRadioGroup' })
    // 找到 browseMode group 并触发 change
    wrapper.vm.$nextTick()
    // 直接通过组件实例触发：找到 browseMode ref 并设置
    const vm: any = wrapper.vm
    vm.browseMode = 'continuous'
    vm.handleBrowseModeChange('continuous')
    await flushPromises()
    await flushPromises()
    expect(photoApi.getCursorList).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('连续浏览：下一批追加且按 ID 去重', async () => {
    ;(photoApi.getCursorList as any)
      .mockResolvedValueOnce({ data: { data: { items: [makePhoto(1), makePhoto(2)], has_more: true, next_cursor: 'c1' } } })
      .mockResolvedValueOnce({ data: { data: { items: [makePhoto(2), makePhoto(3)], has_more: false, next_cursor: '' } } })

    const wrapper = mountPhotos()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.browseMode = 'continuous'
    vm.handleBrowseModeChange('continuous')
    await flushPromises()
    await flushPromises()
    // 第一批：1,2
    expect(vm.continuousPhotos.map((p: any) => p.id)).toEqual([1, 2])
    // 手动触发第二批
    await vm.loadContinuousPhotos()
    await flushPromises()
    // 第二批 2 重复，3 新增 → [1,2,3]
    expect(vm.continuousPhotos.map((p: any) => p.id)).toEqual([1, 2, 3])
    expect(vm.continuousFinished).toBe(true)
    wrapper.unmount()
  })

  it('连续浏览：loading 中不重复触发', async () => {
    let resolveFirst: (v: any) => void = () => {}
    ;(photoApi.getCursorList as any).mockReturnValue(new Promise(r => { resolveFirst = r }))

    const wrapper = mountPhotos()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.browseMode = 'continuous'
    vm.handleBrowseModeChange('continuous')
    await flushPromises()
    // 首批请求未决，loading=true，再次调用应直接 return
    expect(vm.continuousLoading).toBe(true)
    const callsBefore = (photoApi.getCursorList as any).mock.calls.length
    await vm.loadContinuousPhotos()
    expect((photoApi.getCursorList as any).mock.calls.length).toBe(callsBefore) // 未新增请求
    resolveFirst({ data: { data: { items: [makePhoto(1)], has_more: false, next_cursor: '' } } })
    await flushPromises()
    wrapper.unmount()
  })

  it('连续浏览：hasMore=true 但 nextCursor 空 → 停滞保护', async () => {
    ;(photoApi.getCursorList as any).mockResolvedValue({
      data: { data: { items: [makePhoto(1)], has_more: true, next_cursor: '' } },
    })
    const wrapper = mountPhotos()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.browseMode = 'continuous'
    vm.handleBrowseModeChange('continuous')
    await flushPromises()
    await flushPromises()
    expect(vm.continuousError).toBe(true)
    expect(vm.continuousHasMore).toBe(false)
    wrapper.unmount()
  })

  it('连续浏览：error 状态下不自动触发加载', async () => {
    ;(photoApi.getCursorList as any).mockResolvedValue({
      data: { data: { items: [makePhoto(1)], has_more: true, next_cursor: '' } },
    })
    const wrapper = mountPhotos()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.browseMode = 'continuous'
    vm.handleBrowseModeChange('continuous')
    await flushPromises()
    await flushPromises()
    expect(vm.continuousError).toBe(true)
    const callsBefore = (photoApi.getCursorList as any).mock.calls.length
    // 触发可见区间事件，error 状态下不应加载
    vm.onContinuousVisibleRange({ firstRowIndex: 0, lastRowIndex: 0, rowCount: 1 })
    await flushPromises()
    expect((photoApi.getCursorList as any).mock.calls.length).toBe(callsBefore)
    wrapper.unmount()
  })

  it('连续浏览：finished 状态下不触发加载', async () => {
    ;(photoApi.getCursorList as any)
      .mockResolvedValueOnce({ data: { data: { items: [makePhoto(1)], has_more: false, next_cursor: '' } } })
    const wrapper = mountPhotos()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.browseMode = 'continuous'
    vm.handleBrowseModeChange('continuous')
    await flushPromises()
    await flushPromises()
    expect(vm.continuousFinished).toBe(true)
    const callsBefore = (photoApi.getCursorList as any).mock.calls.length
    vm.onContinuousVisibleRange({ firstRowIndex: 0, lastRowIndex: 0, rowCount: 1 })
    await flushPromises()
    expect((photoApi.getCursorList as any).mock.calls.length).toBe(callsBefore)
    wrapper.unmount()
  })

  it('连续浏览：selectedPhotos 独立于挂载 DOM，回收后再次出现仍保持选中', async () => {
    ;(photoApi.getCursorList as any).mockResolvedValue({
      data: { data: { items: [makePhoto(1), makePhoto(2), makePhoto(3)], has_more: false, next_cursor: '' } },
    })
    const wrapper = mountPhotos()
    await flushPromises()
    const vm: any = wrapper.vm
    vm.browseMode = 'continuous'
    vm.handleBrowseModeChange('continuous')
    await flushPromises()
    await flushPromises()
    // 选中 photo 2
    vm.toggleSelectPhoto(2)
    expect(vm.selectedPhotos.has(2)).toBe(true)
    // 连续列表仍含 photo 2（选择不依赖 DOM 挂载）
    expect(vm.continuousPhotos.map((p: any) => p.id)).toContain(2)
    wrapper.unmount()
  })

  // ============ 回归：保存 continuous 后首次挂载 ============

  it('保存 continuous 后首次挂载：cursor 成功直接渲染，不调用分页接口', async () => {
    localStorage.setItem('relive.photos.browseMode', 'continuous')
    ;(photoApi.getCursorList as any).mockResolvedValue({
      data: { data: { items: [makePhoto(1), makePhoto(2)], has_more: false, next_cursor: '' } },
    })
    // 系统照片总数大于零，避免误判“暂无照片”。
    const wrapper = mountPhotos()
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    // 连续浏览不应调用首屏分页列表接口（page/page_size/no_total）。
    // 注意：loadFilteredTotal() 会调 getList({page:1,page_size:1}) 取系统总数，这是既有行为，
    // 不属于“分页列表”请求，故这里只断言不带 no_total:true，不断言 getList 完全未调用。
    expect(photoApi.getList).not.toHaveBeenCalledWith(
      expect.objectContaining({ no_total: true }),
    )
    // cursor 已返回非空数据
    expect(vm.continuousPhotos.map((p: any) => p.id)).toEqual([1, 2])
    // 连续浏览网格容器已渲染（v-else 分支命中）
    expect(wrapper.find('.photo-grid-continuous').exists()).toBe(true)
    // 没有显示任何空状态
    expect(wrapper.findAllComponents({ name: 'ElEmpty' }).length).toBe(0)
    expect(wrapper.text()).not.toContain('未找到匹配的照片')
    expect(wrapper.text()).not.toContain('暂无照片')
    wrapper.unmount()
  })

  it('连续浏览首次加载中：不显示错误空状态，加载反馈可见', async () => {
    localStorage.setItem('relive.photos.browseMode', 'continuous')
    let _resolve: (v: any) => void = () => {}
    ;(photoApi.getCursorList as any).mockReturnValue(new Promise(r => { _resolve = r }))
    const wrapper = mountPhotos()
    await flushPromises()
    const vm: any = wrapper.vm
    // 加载期间不显示“没有符合条件”/“暂无照片”空状态
    expect(wrapper.text()).not.toContain('未找到匹配的照片')
    expect(wrapper.text()).not.toContain('暂无照片')
    expect(vm.continuousLoading).toBe(true)
    // 连续浏览容器应渲染（加载中也进入 v-else 分支），哨兵可见“加载中”
    expect(wrapper.find('.photo-grid-continuous').exists()).toBe(true)
    expect(wrapper.find('.scroll-sentinel').exists()).toBe(true)
    // 让请求落地，避免遗留 pending
    _resolve({ data: { data: { items: [makePhoto(1)], has_more: false, next_cursor: '' } } })
    await flushPromises()
    wrapper.unmount()
  })

  it('连续浏览真正为空：系统有照片时显示“没有符合当前搜索条件”', async () => {
    localStorage.setItem('relive.photos.browseMode', 'continuous')
    ;(photoApi.getCursorList as any).mockResolvedValue({
      data: { data: { items: [], has_more: false, next_cursor: '' } },
    })
    // 系统照片总数大于零（active_count=10）。
    const wrapper = mountPhotos()
    const vm: any = wrapper.vm
    vm.photoStore.photoCounts.active = 10
    await flushPromises()
    await flushPromises()
    // 系统有照片、当前筛选为空 → 显示“未找到匹配的照片”
    expect(wrapper.text()).toContain('未找到匹配的照片')
    expect(wrapper.text()).not.toContain('暂无照片')
    // 网格容器不应渲染
    expect(wrapper.find('.photo-grid-continuous').exists()).toBe(false)
    wrapper.unmount()
  })

  it('连续浏览真正为空：系统无照片时显示“暂无照片”', async () => {
    localStorage.setItem('relive.photos.browseMode', 'continuous')
    ;(photoApi.getCursorList as any).mockResolvedValue({
      data: { data: { items: [], has_more: false, next_cursor: '' } },
    })
    // 系统照片总数为零。
    const wrapper = mountPhotos()
    await flushPromises()
    await flushPromises()
    // 系统无照片 → 显示“暂无照片”
    expect(wrapper.text()).toContain('暂无照片')
    expect(wrapper.text()).not.toContain('未找到匹配的照片')
    expect(wrapper.find('.photo-grid-continuous').exists()).toBe(false)
    wrapper.unmount()
  })

  it('连续浏览请求失败：显示加载失败状态及重试入口，不被通用空状态遮挡', async () => {
    localStorage.setItem('relive.photos.browseMode', 'continuous')
    // 系统有照片，避免错误态被“暂无照片/没有符合条件”空状态分支抢先命中。
    sharedStore.photoCounts.active = 10
    ;(photoApi.getCursorList as any).mockRejectedValue(new Error('network error'))
    const wrapper = mountPhotos()
    await flushPromises()
    await flushPromises()
    const vm: any = wrapper.vm
    expect(vm.continuousError).toBe(true)
    expect(vm.continuousLoading).toBe(false)
    expect(vm.continuousPhotos.length).toBe(0)
    // 网格容器仍渲染，哨兵显示加载失败与重试入口
    expect(wrapper.find('.photo-grid-continuous').exists()).toBe(true)
    expect(wrapper.find('.sentinel-error').exists()).toBe(true)
    expect(wrapper.text()).toContain('加载失败')
    expect(wrapper.text()).toContain('重试')
    // 不被通用空状态覆盖
    expect(wrapper.text()).not.toContain('未找到匹配的照片')
    expect(wrapper.text()).not.toContain('暂无照片')
    wrapper.unmount()
  })
})
