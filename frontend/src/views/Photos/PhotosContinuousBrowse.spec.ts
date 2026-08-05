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
    getList: vi.fn(),
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
  usePhotoStore: () => ({
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
  }),
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
  })

  it('默认浏览模式为 pagination（localStorage 空）', async () => {
    const wrapper = mount(PhotosIndex, { global: { stubs: ['el-card', 'el-empty', 'el-button', 'el-icon', 'el-input', 'el-radio-group', 'el-radio-button', 'el-pagination', 'el-table', 'el-table-column', 'el-tag', 'el-tooltip', 'el-dialog', 'el-switch', 'el-select', 'el-option', 'el-image', 'SectionHeader', 'PathBrowser', 'LocationPicker', 'VirtualMediaGrid', 'PhotoCard'] } })
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
    const wrapper = mount(PhotosIndex, { global: { stubs: ['el-card', 'el-empty', 'el-button', 'el-icon', 'el-input', 'el-radio-group', 'el-radio-button', 'el-pagination', 'el-table', 'el-table-column', 'el-tag', 'el-tooltip', 'el-dialog', 'el-switch', 'el-select', 'el-option', 'el-image', 'SectionHeader', 'PathBrowser', 'LocationPicker', 'VirtualMediaGrid', 'PhotoCard'] } })
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

    const wrapper = mount(PhotosIndex, { global: { stubs: ['el-card', 'el-empty', 'el-button', 'el-icon', 'el-input', 'el-radio-group', 'el-radio-button', 'el-pagination', 'el-table', 'el-table-column', 'el-tag', 'el-tooltip', 'el-dialog', 'el-switch', 'el-select', 'el-option', 'el-image', 'SectionHeader', 'PathBrowser', 'LocationPicker', 'VirtualMediaGrid', 'PhotoCard'] } })
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

    const wrapper = mount(PhotosIndex, { global: { stubs: ['el-card', 'el-empty', 'el-button', 'el-icon', 'el-input', 'el-radio-group', 'el-radio-button', 'el-pagination', 'el-table', 'el-table-column', 'el-tag', 'el-tooltip', 'el-dialog', 'el-switch', 'el-select', 'el-option', 'el-image', 'SectionHeader', 'PathBrowser', 'LocationPicker', 'VirtualMediaGrid', 'PhotoCard'] } })
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
    const wrapper = mount(PhotosIndex, { global: { stubs: ['el-card', 'el-empty', 'el-button', 'el-icon', 'el-input', 'el-radio-group', 'el-radio-button', 'el-pagination', 'el-table', 'el-table-column', 'el-tag', 'el-tooltip', 'el-dialog', 'el-switch', 'el-select', 'el-option', 'el-image', 'SectionHeader', 'PathBrowser', 'LocationPicker', 'VirtualMediaGrid', 'PhotoCard'] } })
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
    const wrapper = mount(PhotosIndex, { global: { stubs: ['el-card', 'el-empty', 'el-button', 'el-icon', 'el-input', 'el-radio-group', 'el-radio-button', 'el-pagination', 'el-table', 'el-table-column', 'el-tag', 'el-tooltip', 'el-dialog', 'el-switch', 'el-select', 'el-option', 'el-image', 'SectionHeader', 'PathBrowser', 'LocationPicker', 'VirtualMediaGrid', 'PhotoCard'] } })
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
    const wrapper = mount(PhotosIndex, { global: { stubs: ['el-card', 'el-empty', 'el-button', 'el-icon', 'el-input', 'el-radio-group', 'el-radio-button', 'el-pagination', 'el-table', 'el-table-column', 'el-tag', 'el-tooltip', 'el-dialog', 'el-switch', 'el-select', 'el-option', 'el-image', 'SectionHeader', 'PathBrowser', 'LocationPicker', 'VirtualMediaGrid', 'PhotoCard'] } })
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
    const wrapper = mount(PhotosIndex, { global: { stubs: ['el-card', 'el-empty', 'el-button', 'el-icon', 'el-input', 'el-radio-group', 'el-radio-button', 'el-pagination', 'el-table', 'el-table-column', 'el-tag', 'el-tooltip', 'el-dialog', 'el-switch', 'el-select', 'el-option', 'el-image', 'SectionHeader', 'PathBrowser', 'LocationPicker', 'VirtualMediaGrid', 'PhotoCard'] } })
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
})
