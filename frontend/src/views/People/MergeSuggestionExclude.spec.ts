import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'

// People/index.vue 合并建议审核流程回归测试。
// 聚焦“剔除所选”成功后的静默详情刷新：
// - 部分剔除：详情 200，弹窗保持打开，剩余候选刷新；
// - 全部剔除：详情 404，当前建议清空，弹窗关闭，不弹 ElMessage.error；
// - 真实失败（500/网络错误）：仍调用错误提示。

// jsdom 默认无 localStorage，注入最小实现。
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
    Object.defineProperty(window, 'localStorage', { configurable: true, value: globalThis.localStorage })
  }
})

// vi.hoisted 确保 mock 对象在 vi.mock 工厂（会被提升到文件顶部）执行时已就绪。
const mocks = vi.hoisted(() => {
  return {
    ElMessage: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
    ElMessageBox: { confirm: vi.fn() },
    mockListMergeSuggestions: vi.fn(),
    mockGetMergeSuggestion: vi.fn(),
    mockExcludeMergeSuggestionCandidates: vi.fn(),
    mockGetList: vi.fn(),
    mockGetTask: vi.fn(),
    mockGetStats: vi.fn(),
    mockGetBackgroundLogs: vi.fn(),
    mockGetMergeSuggestionTask: vi.fn(),
    mockGetMergeSuggestionStats: vi.fn(),
    mockGetMergeSuggestionLogs: vi.fn(),
    mockGetBackgroundStatus: vi.fn(),
  }
})

vi.mock('@/api/people', () => ({
  peopleApi: {
    listMergeSuggestions: (...a: unknown[]) => mocks.mockListMergeSuggestions(...a),
    getMergeSuggestion: (...a: unknown[]) => mocks.mockGetMergeSuggestion(...a),
    excludeMergeSuggestionCandidates: (...a: unknown[]) => mocks.mockExcludeMergeSuggestionCandidates(...a),
    getList: (...a: unknown[]) => mocks.mockGetList(...a),
    getTask: (...a: unknown[]) => mocks.mockGetTask(...a),
    getStats: (...a: unknown[]) => mocks.mockGetStats(...a),
    getBackgroundLogs: (...a: unknown[]) => mocks.mockGetBackgroundLogs(...a),
    getMergeSuggestionTask: (...a: unknown[]) => mocks.mockGetMergeSuggestionTask(...a),
    getMergeSuggestionStats: (...a: unknown[]) => mocks.mockGetMergeSuggestionStats(...a),
    getMergeSuggestionLogs: (...a: unknown[]) => mocks.mockGetMergeSuggestionLogs(...a),
  },
}))

vi.mock('@/api/background', () => ({
  backgroundApi: {
    getStatus: (...a: unknown[]) => mocks.mockGetBackgroundStatus(...a),
  },
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: {}, query: {} }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}))

vi.mock('element-plus', () => ({
  ElMessage: mocks.ElMessage,
  ElMessageBox: mocks.ElMessageBox,
}))

const {
  ElMessage,
  mockListMergeSuggestions,
  mockGetMergeSuggestion,
  mockExcludeMergeSuggestionCandidates,
  mockGetList,
  mockGetTask,
  mockGetStats,
  mockGetBackgroundLogs,
  mockGetMergeSuggestionTask,
  mockGetMergeSuggestionStats,
  mockGetMergeSuggestionLogs,
  mockGetBackgroundStatus,
} = mocks

import PeopleIndex from '@/views/People/index.vue'

const ok = (data: unknown) => ({ status: 200, statusText: 'OK', data: { success: true, data }, headers: {}, config: {} })
const emptyPaged = () => ok({ items: [], total: 0, page: 1, page_size: 12, total_pages: 0 })
const emptyPeople = () => ok({ items: [], total: 0, page: 1, page_size: 20, total_pages: 0 })

// 构造一条合并建议，items 为候选列表。
const makeSuggestion = (id: number, candidateIds: number[]) => ({
  id,
  target_person_id: 100,
  target_category_snapshot: 'stranger',
  status: 'pending',
  candidate_count: candidateIds.length,
  top_similarity: 0.9,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  target_person: {
    id: 100,
    name: '目标人物',
    category: 'stranger',
    has_avatar: false,
    face_count: 1,
    photo_count: 1,
    hidden: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  },
  items: candidateIds.map(cid => ({
    candidate_person_id: cid,
    similarity_score: 0.85,
    rank: cid,
    status: 'pending',
    match_source: 'legacy' as const,
    candidate_person: {
      id: cid,
      name: `候选${cid}`,
      category: 'stranger' as const,
      has_avatar: false,
      face_count: 1,
      photo_count: 1,
      hidden: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })),
})

// 模拟后端 404 响应：getMergeSuggestion 在 acceptNotFound 下应放行为 resolved（status=404）。
// 注意：真实拦截器对 404 会 reject；这里 mock 直接返回 404 resolved，模拟 validateStatus 放行后的效果。
const notFound = () => ({ status: 404, statusText: 'Not Found', data: { success: false, error: { code: 'NOT_FOUND', message: 'Merge suggestion not found' } }, headers: {}, config: {} })

// 模拟 500 响应：validateStatus 不放行 500，axios 会 reject，进入 catch。
// 直接 reject 一个带 response 的 AxiosError 形状对象。
const serverError = () => Promise.reject({
  response: { status: 500, data: { error: { message: '服务器内部错误' } } },
  message: 'Request failed with status 500',
})

const networkError = () => Promise.reject({ request: {}, message: 'Network Error' })

const baseStubs = {
  SectionHeader: { template: '<div class="section-header"><slot name="actions" /></div>' },
  MergeSuggestionReviewDialog: {
    // 透传关键 props/emits，使父组件状态可被测试断言；渲染一个触发 exclude 的按钮。
    props: ['modelValue', 'suggestion', 'loading', 'submitting'],
    emits: ['update:modelValue', 'exclude', 'apply'],
    template: `
      <div class="review-dialog-stub">
        <button class="stub-exclude" :disabled="submitting" @click="$emit('exclude', (suggestion?.items || []).map(i => i.candidate_person_id))">stub-exclude</button>
        <button class="stub-close" @click="$emit('update:modelValue', false)">stub-close</button>
      </div>
    `,
  },
  PersonCard: true,
  PersonEditDialog: true,
  PersonMergeConfirmDialog: true,
  'el-tabs': { template: '<div><slot/></div>' },
  'el-tab-pane': { template: '<div><slot/></div>' },
  'el-card': { template: '<div><slot/><slot name="header"/></div>' },
  'el-button': { template: '<button @click="$emit(\'click\')"><slot/></button>' },
  'el-input': true,
  'el-select': true,
  'el-option': true,
  'el-radio-group': { template: '<div><slot/></div>' },
  'el-radio-button': { template: '<span><slot/></span>' },
  'el-checkbox': { template: '<span><slot/></span>' },
  'el-avatar': true,
  'el-icon': true,
  'el-empty': true,
  'el-pagination': true,
  'el-progress': true,
}

const mountPeople = async () => {
  const wrapper = mount(PeopleIndex, { global: { stubs: baseStubs } })
  await flushPromises()
  return wrapper
}

const setupInitialLoad = (suggestion: ReturnType<typeof makeSuggestion>) => {
  mockListMergeSuggestions.mockResolvedValue(ok({ items: [suggestion], total: 1, page: 1, page_size: 12, total_pages: 1 }))
  mockGetMergeSuggestion.mockResolvedValue(ok(suggestion))
  mockGetList.mockResolvedValue(emptyPeople())
  mockGetTask.mockResolvedValue(ok(null))
  mockGetStats.mockResolvedValue(ok({
    total: 0, pending: 0, queued: 0, processing: 0, completed: 0, failed: 0, cancelled: 0,
    pending_faces_total: 0, pending_faces_never_clustered: 0, pending_faces_retried: 0,
    total_faces: 0, detected_photos: 0, pending_photos: 0,
  }))
  mockGetBackgroundLogs.mockResolvedValue(ok({ lines: [] }))
  mockGetMergeSuggestionTask.mockResolvedValue(ok(null))
  mockGetMergeSuggestionStats.mockResolvedValue(ok({
    total: 0, pending: 0, applied: 0, dismissed: 0, obsolete: 0,
    pending_items: 0, excluded_items: 0, merged_items: 0,
  }))
  mockGetMergeSuggestionLogs.mockResolvedValue(ok({ lines: [] }))
  mockGetBackgroundStatus.mockResolvedValue(ok(null))
}

describe('People 合并建议 - 剔除审核流程', () => {
  beforeEach(() => {
    ElMessage.success.mockClear()
    ElMessage.error.mockClear()
    mockListMergeSuggestions.mockReset()
    mockGetMergeSuggestion.mockReset()
    mockExcludeMergeSuggestionCandidates.mockReset()
    mockGetList.mockReset()
  })

  it('剔除部分候选后，使用 200 数据更新详情，弹窗保持打开', async () => {
    const suggestion = makeSuggestion(7, [201, 202, 203])
    setupInitialLoad(suggestion)

    const wrapper = await mountPeople()
    const vm = wrapper.findComponent(PeopleIndex).vm as any

    // 打开审核弹窗
    await vm.openMergeSuggestionReview(7)
    await flushPromises()
    expect(vm.currentMergeSuggestion?.id).toBe(7)
    expect(vm.mergeSuggestionDialogVisible).toBe(true)

    // exclude 成功；后续静默详情刷新返回剩余候选（去掉 201 后剩 2 个）
    const remaining = makeSuggestion(7, [202, 203])
    mockExcludeMergeSuggestionCandidates.mockResolvedValue(ok(null))
    mockListMergeSuggestions.mockResolvedValue(ok({ items: [remaining], total: 1, page: 1, page_size: 12, total_pages: 1 }))
    mockGetMergeSuggestion.mockResolvedValue(ok(remaining))

    // 触发“剔除所选”
    await vm.handleExcludeMergeSuggestion([201])
    await flushPromises()

    expect(mockExcludeMergeSuggestionCandidates).toHaveBeenCalledWith(7, [201])
    expect(ElMessage.success).toHaveBeenCalledWith('已剔除所选候选人物')
    // 成功提示只出现一次，且无错误提示
    expect(ElMessage.success).toHaveBeenCalledTimes(1)
    expect(ElMessage.error).not.toHaveBeenCalled()
    // 剩余候选更新，弹窗保持打开
    expect(vm.currentMergeSuggestion?.items?.length).toBe(2)
    expect(vm.mergeSuggestionDialogVisible).toBe(true)
    // 详情请求在 silent 下应启用 acceptNotFound
    expect(mockGetMergeSuggestion).toHaveBeenCalledWith(7, { acceptNotFound: true })
  })

  it('剔除最后一个候选后，详情返回 404，当前建议清空，弹窗关闭', async () => {
    const suggestion = makeSuggestion(8, [301])
    setupInitialLoad(suggestion)

    const wrapper = await mountPeople()
    const vm = wrapper.findComponent(PeopleIndex).vm as any

    await vm.openMergeSuggestionReview(8)
    await flushPromises()
    expect(vm.mergeSuggestionDialogVisible).toBe(true)

    // exclude 成功；静默详情刷新返回 404（建议已处理完毕）；外部列表也无该建议
    mockExcludeMergeSuggestionCandidates.mockResolvedValue(ok(null))
    mockListMergeSuggestions.mockResolvedValue(emptyPaged())
    mockGetMergeSuggestion.mockResolvedValue(notFound())

    await vm.handleExcludeMergeSuggestion([301])
    await flushPromises()

    expect(ElMessage.success).toHaveBeenCalledWith('已剔除所选候选人物')
    expect(ElMessage.success).toHaveBeenCalledTimes(1)
    // 404 不应触发错误提示
    expect(ElMessage.error).not.toHaveBeenCalled()
    // 当前建议状态被清空
    expect(vm.currentMergeSuggestion).toBeNull()
    expect(vm.currentMergeSuggestionId).toBeNull()
    // 弹窗自动关闭
    expect(vm.mergeSuggestionDialogVisible).toBe(false)
  })

  it('500 或网络错误仍然调用错误提示，且不显示成功提示', async () => {
    const suggestion = makeSuggestion(9, [401, 402])
    setupInitialLoad(suggestion)

    const wrapper = await mountPeople()
    const vm = wrapper.findComponent(PeopleIndex).vm as any

    await vm.openMergeSuggestionReview(9)
    await flushPromises()

    // exclude 接口本身失败（500）
    mockExcludeMergeSuggestionCandidates.mockImplementation(serverError)
    mockGetMergeSuggestion.mockResolvedValue(ok(suggestion))

    await vm.handleExcludeMergeSuggestion([401])
    await flushPromises()

    // 剔除接口失败：不显示成功提示，显示真实失败原因
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(ElMessage.error).toHaveBeenCalledWith('服务器内部错误')

    // 第二轮：exclude 成功，但静默详情刷新遇到网络错误
    ElMessage.error.mockClear()
    ElMessage.success.mockClear()
    mockExcludeMergeSuggestionCandidates.mockResolvedValue(ok(null))
    mockListMergeSuggestions.mockResolvedValue(emptyPaged())
    mockGetMergeSuggestion.mockImplementation(networkError)

    await vm.handleExcludeMergeSuggestion([401, 402])
    await flushPromises()

    // 成功提示出现一次（exclude 本身成功）
    expect(ElMessage.success).toHaveBeenCalledWith('已剔除所选候选人物')
    // silent 模式下网络错误仍需提示用户真实失败原因（任务书：非预期错误不得静默吞掉）
    expect(ElMessage.error).toHaveBeenCalledWith('Network Error')
    // silent 下不清空已有状态，弹窗与候选不被误判为处理完成
    expect(vm.currentMergeSuggestion).not.toBeNull()
    expect(vm.currentMergeSuggestionId).toBe(9)
    expect(vm.mergeSuggestionDialogVisible).toBe(true)
  })
})
