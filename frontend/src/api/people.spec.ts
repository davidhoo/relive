import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { AxiosRequestConfig } from 'axios'

// people.ts 单元测试：聚焦 getMergeSuggestion 的“接受 404”选项，
// 验证 validateStatus 配置在不同状态码下的行为，不依赖真实网络。

// 通过 spy http.get 捕获实际传入的 axios config，断言 validateStatus 行为。
let capturedConfig: AxiosRequestConfig | undefined
const httpGetSpy = vi.fn((url: string, config?: AxiosRequestConfig) => {
  capturedConfig = config
  // 返回一个最小 AxiosResponse 形状，调用方一般不在此处消费 data
  return Promise.resolve({ status: 200, statusText: 'OK', data: {}, headers: {}, config: config || {} })
})

vi.mock('@/utils/request', () => ({
  default: {
    get: (url: string, config?: AxiosRequestConfig) => httpGetSpy(url, config),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}))

import { peopleApi } from '@/api/people'

const validateStatusOf = (): ((status: number) => boolean) | undefined => {
  return capturedConfig?.validateStatus as ((status: number) => boolean) | undefined
}

describe('peopleApi.getMergeSuggestion - 接受 404 选项', () => {
  beforeEach(() => {
    httpGetSpy.mockClear()
    capturedConfig = undefined
  })

  it('默认详情请求不接受 404（不传 options）', async () => {
    await peopleApi.getMergeSuggestion(42)
    // 未传 options 时不应注入自定义 validateStatus，沿用 axios 默认（>= 200 && < 300）
    expect(validateStatusOf()).toBeUndefined()
    expect(httpGetSpy).toHaveBeenCalledWith('/people/merge-suggestions/42', undefined)
  })

  it('开启“接受未找到”后，validateStatus(404) 返回 true', async () => {
    await peopleApi.getMergeSuggestion(42, { acceptNotFound: true })
    const fn = validateStatusOf()
    expect(fn).toBeTypeOf('function')
    expect(fn!(404)).toBe(true)
  })

  it('开启该选项后，validateStatus(500) 仍返回 false', async () => {
    await peopleApi.getMergeSuggestion(42, { acceptNotFound: true })
    const fn = validateStatusOf()
    expect(fn!(500)).toBe(false)
    // 其他常见 4xx 也不应被接受
    expect(fn!(400)).toBe(false)
    expect(fn!(403)).toBe(false)
  })

  it('开启该选项后，正常的 2xx 响应始终被接受', async () => {
    await peopleApi.getMergeSuggestion(42, { acceptNotFound: true })
    const fn = validateStatusOf()
    expect(fn!(200)).toBe(true)
    expect(fn!(204)).toBe(true)
    // 边界：300 重定向不在接受范围（与 axios 默认一致，仅放行 2xx + 404）
    expect(fn!(300)).toBe(false)
  })
})
