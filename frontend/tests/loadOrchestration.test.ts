import test from 'node:test'
import assert from 'node:assert/strict'

import {
  buildFirstScreenListParams,
  shouldLoadDerivedStatusOnMount,
  shouldLoadDerivedStatusAfterListResolved,
  isStaleRequest,
  nextReqId,
} from '../src/views/Photos/loadOrchestration.ts'

test('首屏照片列表请求携带 no_total=true', () => {
  const params = buildFirstScreenListParams(1, 20, { search: 'cat', category: '动物' })
  assert.equal(params.no_total, true)
  assert.equal(params.page, 1)
  assert.equal(params.page_size, 20)
  assert.equal(params.search, 'cat')
  assert.equal(params.category, '动物')
})

test('首屏列表请求过滤空筛选条件', () => {
  const params = buildFirstScreenListParams(2, 50, { search: '', tag: undefined, status: 'excluded' })
  assert.equal(params.no_total, true)
  assert.equal(params.page, 2)
  assert.equal(params.page_size, 50)
  assert.equal(params.status, 'excluded')
  assert.equal('search' in params, false, '空字符串筛选不应出现在参数中')
  assert.equal('tag' in params, false, 'undefined 筛选不应出现在参数中')
})

test('扫描路径折叠时，挂载阶段不加载派生状态', () => {
  assert.equal(shouldLoadDerivedStatusOnMount(true), false)
})

test('扫描路径展开时，挂载阶段才加载派生状态', () => {
  assert.equal(shouldLoadDerivedStatusOnMount(false), true)
})

test('扫描路径折叠时，列表返回后不加载派生状态', () => {
  assert.equal(shouldLoadDerivedStatusAfterListResolved(true), false)
})

test('扫描路径展开时，列表返回后才加载派生状态（延后）', () => {
  assert.equal(shouldLoadDerivedStatusAfterListResolved(false), true)
})

test('快速切换筛选时，旧请求结果被判定为过期', () => {
  // 请求 A 序号=1 发出后，又发出了更新请求 B（序号=2）
  const currentReqId = nextReqId(1) // => 2
  // A 的结果返回时 localReqId=1 !== currentReqId=2 → 过期，丢弃
  assert.equal(isStaleRequest(1, currentReqId), true)
})

test('最新请求的结果不被判定为过期', () => {
  const currentReqId = nextReqId(1) // => 2
  // B 的结果返回时 localReqId=2 === currentReqId=2 → 有效
  assert.equal(isStaleRequest(2, currentReqId), false)
})

test('nextReqId 单调递增', () => {
  assert.equal(nextReqId(0), 1)
  assert.equal(nextReqId(5), 6)
  assert.ok(nextReqId(5) > 5)
})
