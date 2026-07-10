import test from 'node:test'
import assert from 'node:assert/strict'

import { shouldLoadScanPathDerivedStatus } from '../src/views/Photos/scanPathStatsHelpers.ts'

test('扫描路径折叠时不加载派生统计', () => {
  assert.equal(shouldLoadScanPathDerivedStatus(true, false), false)
})

test('扫描路径首次展开时加载派生统计', () => {
  assert.equal(shouldLoadScanPathDerivedStatus(false, false), true)
})

test('派生统计已加载后不重复加载', () => {
  assert.equal(shouldLoadScanPathDerivedStatus(false, true), false)
})
