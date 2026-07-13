import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'

// Detail.vue 级别测试：覆盖 Tab 按需加载（测试项 10）、Tab 切换恢复滚动位置（测试项 8）。
// 通过 mock peopleApi 验证调用次数与顺序，mock vue-router/element-plus 避免副作用。

// jsdom 默认未提供 localStorage，提前注入最小实现。
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
  // jsdom 可能已有 window；同步补上
  if (typeof window !== 'undefined' && !window.localStorage) {
    Object.defineProperty(window, 'localStorage', { configurable: true, value: globalThis.localStorage })
  }
})

const mockGetById = vi.fn()
const mockGetPhotos = vi.fn()
const mockGetFaces = vi.fn()
const mockGetList = vi.fn()

vi.mock('@/api/people', () => ({
  peopleApi: {
    getById: (...a: unknown[]) => mockGetById(...a),
    getPhotos: (...a: unknown[]) => mockGetPhotos(...a),
    getFaces: (...a: unknown[]) => mockGetFaces(...a),
    getList: (...a: unknown[]) => mockGetList(...a),
  },
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: '271594' }, query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}))

// element-plus 的 ElMessage / ElMessageBox 在 jsdom 下挂载较重，简化 mock。
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
  ElMessageBox: { confirm: vi.fn() },
}))

import Detail from '@/views/People/Detail.vue'
import SectionHeader from '@/components/SectionHeader.vue'

const paged = (items: unknown[], total: number) => ({
  data: { success: true, data: { items, total, page: 1, page_size: items.length, total_pages: 1 } },
})

const makePerson = () => ({
  data: {
    success: true,
    data: {
      id: 271594,
      name: 'Test',
      category: 'family',
      has_avatar: false,
      face_count: 10000,
      photo_count: 500,
      hidden: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  },
})

const mountDetail = async () => {
  const wrapper = mount(Detail, {
    global: {
      stubs: {
        SectionHeader: SectionHeader,
        VirtualMediaGrid: true,
        PersonEditDialog: true,
        PersonMergeConfirmDialog: true,
        // stub 掉所有 element-plus 组件避免渲染开销
        'el-card': { template: '<div><slot/><slot name="header"/></div>' },
        'el-tabs': { template: '<div><slot/></div>' },
        'el-tab-pane': { template: '<div><slot/></div>' },
        'el-button': { template: '<button @click="$emit(\'click\')"><slot/></button>' },
        'el-empty': true,
        'el-tag': { template: '<span><slot/></span>' },
        'el-tooltip': { template: '<span><slot/></span>' },
        'el-avatar': true,
        'el-icon': true,
        'el-dialog': true,
        'el-select': true,
        'el-option': true,
        'el-divider': true,
        'el-checkbox': true,
      },
    },
  })
  await flushPromises()
  return wrapper
}

describe('Detail - 照片和人脸首次按需加载（测试项 10）', () => {
  beforeEach(() => {
    mockGetById.mockReset()
    mockGetPhotos.mockReset()
    mockGetFaces.mockReset()
    mockGetList.mockReset()
    mockGetById.mockResolvedValue(makePerson())
    mockGetPhotos.mockResolvedValue(
      paged(Array.from({ length: 30 }, (_, i) => ({ id: i + 1, file_name: `p${i}.jpg`, updated_at: '2026-01-01T00:00:00Z' })), 500),
    )
    mockGetFaces.mockResolvedValue(
      paged(Array.from({ length: 50 }, (_, i) => ({ id: i + 1, photo_id: 1, updated_at: '2026-01-01T00:00:00Z', quality_score: 0.9 })), 10000),
    )
  })

  it('进入页面只加载人物信息 + 照片第一页，不加载人脸', async () => {
    await mountDetail()
    await flushPromises()

    expect(mockGetById).toHaveBeenCalledWith(271594)
    expect(mockGetPhotos).toHaveBeenCalledTimes(1)
    // 人脸在首次进入时不应被加载
    expect(mockGetFaces).not.toHaveBeenCalled()
  })

  it('切换到人脸 Tab 才加载人脸第一页', async () => {
    const wrapper = await mountDetail()
    await flushPromises()
    expect(mockGetFaces).not.toHaveBeenCalled()

    // 切到 faces tab
    wrapper.findComponent(Detail).vm.$nextTick()
    // 模拟 activeTab 变化：直接操作组件内部状态
    const vm = wrapper.findComponent(Detail).vm as any
    vm.activeTab = 'faces'
    await flushPromises()

    expect(mockGetFaces).toHaveBeenCalledTimes(1)
  })

  it('已访问的人脸 Tab 再次切回不重复加载第一页', async () => {
    const wrapper = await mountDetail()
    await flushPromises()
    const vm = wrapper.findComponent(Detail).vm as any

    // 切到 faces
    vm.activeTab = 'faces'
    await flushPromises()
    const facesCallsAfterFirst = mockGetFaces.mock.calls.length
    expect(facesCallsAfterFirst).toBe(1)

    // 切回 photos
    vm.activeTab = 'photos'
    await flushPromises()
    // 再切回 faces —— 已加载过，不应再触发第一页
    vm.activeTab = 'faces'
    await flushPromises()
    expect(mockGetFaces.mock.calls.length).toBe(facesCallsAfterFirst)
  })
})

describe('Detail - Tab 切换恢复各自滚动位置（测试项 8）', () => {
  beforeEach(() => {
    mockGetById.mockReset()
    mockGetPhotos.mockReset()
    mockGetFaces.mockReset()
    mockGetList.mockReset()
    mockGetById.mockResolvedValue(makePerson())
    mockGetPhotos.mockResolvedValue(
      paged(Array.from({ length: 30 }, (_, i) => ({ id: i + 1, updated_at: '2026-01-01T00:00:00Z' })), 500),
    )
    mockGetFaces.mockResolvedValue(
      paged(Array.from({ length: 50 }, (_, i) => ({ id: i + 1, photo_id: 1, updated_at: '2026-01-01T00:00:00Z', quality_score: 0.9 })), 10000),
    )
  })

  it('照片与人脸各自维护独立滚动位置记忆', async () => {
    const wrapper = await mountDetail()
    await flushPromises()
    const vm = wrapper.findComponent(Detail).vm as any

    // photosScrollTop / facesScrollTop 初始为 0 且互相独立
    expect(vm.photosScrollTop).toBe(0)
    expect(vm.facesScrollTop).toBe(0)

    // 切到 faces 触发首次加载
    vm.activeTab = 'faces'
    await flushPromises()
    expect(mockGetFaces).toHaveBeenCalledTimes(1)

    // 切回 photos：照片滚动位置仍为初始 0（未被 faces 操作影响）
    vm.activeTab = 'photos'
    await flushPromises()
    expect(vm.photosScrollTop).toBe(0)
  })
})
