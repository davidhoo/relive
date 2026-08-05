import { describe, it, expect } from 'vitest'
import {
  readBrowseMode,
  shouldLoadByVisibleRange,
  appendDedup,
  isCursorStalled,
  columnsForViewport,
  BROWSE_MODE_STORAGE_KEY,
} from './photoGridUtils'

describe('photoGridUtils - readBrowseMode', () => {
  it('合法值 continuous 返回 continuous', () => {
    expect(readBrowseMode('continuous')).toBe('continuous')
  })
  it('合法值 pagination 返回 pagination', () => {
    expect(readBrowseMode('pagination')).toBe('pagination')
  })
  it('null 降级为 pagination', () => {
    expect(readBrowseMode(null)).toBe('pagination')
  })
  it('非法值降级为 pagination', () => {
    expect(readBrowseMode('invalid')).toBe('pagination')
  })
  it('空字符串降级为 pagination', () => {
    expect(readBrowseMode('')).toBe('pagination')
  })
  it('存储键常量为 relive.photos.browseMode', () => {
    expect(BROWSE_MODE_STORAGE_KEY).toBe('relive.photos.browseMode')
  })
})

describe('photoGridUtils - shouldLoadByVisibleRange', () => {
  const base = {
    active: true,
    loading: false,
    error: false,
    hasMore: true,
    rowCount: 100,
    lastVisibleRowIndex: 95,
    thresholdRows: 3,
  }
  it('接近末尾（差 3 行）触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, lastVisibleRowIndex: 97, rowCount: 100 })).toBe(true)
  })
  it('距末端 5 行不触发（>threshold）', () => {
    expect(shouldLoadByVisibleRange({ ...base, lastVisibleRowIndex: 95, rowCount: 100 })).toBe(false)
  })
  it('loading 中不触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, loading: true })).toBe(false)
  })
  it('error 状态不触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, error: true })).toBe(false)
  })
  it('hasMore=false 不触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, hasMore: false })).toBe(false)
  })
  it('非活动 tab 不触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, active: false })).toBe(false)
  })
  it('首屏 rowCount<=0 触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, rowCount: 0, lastVisibleRowIndex: 0 })).toBe(true)
  })
  it('有数据但未测量（lastVisibleRowIndex<0）不触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, lastVisibleRowIndex: -1 })).toBe(false)
  })
})

describe('photoGridUtils - appendDedup', () => {
  it('新批次全为新项时全部追加', () => {
    const existing = [{ id: 1 }, { id: 2 }]
    const batch = [{ id: 3 }, { id: 4 }]
    const r = appendDedup(existing, batch)
    expect(r.items.map(p => p.id)).toEqual([1, 2, 3, 4])
    expect(r.fresh).toBe(2)
  })
  it('重复 ID 去重，仅追加新项', () => {
    const existing = [{ id: 1 }, { id: 2 }]
    const batch = [{ id: 2 }, { id: 3 }]
    const r = appendDedup(existing, batch)
    expect(r.items.map(p => p.id)).toEqual([1, 2, 3])
    expect(r.fresh).toBe(1)
  })
  it('全重复时 fresh=0', () => {
    const existing = [{ id: 1 }]
    const batch = [{ id: 1 }]
    const r = appendDedup(existing, batch)
    expect(r.items.map(p => p.id)).toEqual([1])
    expect(r.fresh).toBe(0)
  })
  it('保留原类型', () => {
    const existing = [{ id: 1, name: 'a' }]
    const batch = [{ id: 2, name: 'b' }]
    const r = appendDedup(existing, batch)
    expect(r.items[1]!.name).toBe('b')
  })
})

describe('photoGridUtils - isCursorStalled', () => {
  it('hasMore=false 不算停滞', () => {
    expect(isCursorStalled({ hasMore: false, nextCursor: '', requestCursor: '', consumedCursors: new Set() })).toBe(false)
  })
  it('hasMore=true 但 nextCursor 空 → 停滞', () => {
    expect(isCursorStalled({ hasMore: true, nextCursor: '', requestCursor: '', consumedCursors: new Set() })).toBe(true)
  })
  it('nextCursor 等于请求 cursor → 停滞', () => {
    expect(isCursorStalled({ hasMore: true, nextCursor: 'x', requestCursor: 'x', consumedCursors: new Set() })).toBe(true)
  })
  it('nextCursor 已被消费过 → 停滞', () => {
    expect(isCursorStalled({ hasMore: true, nextCursor: 'y', requestCursor: 'x', consumedCursors: new Set(['y']) })).toBe(true)
  })
  it('正常推进 → 不停滞', () => {
    expect(isCursorStalled({ hasMore: true, nextCursor: 'y', requestCursor: 'x', consumedCursors: new Set() })).toBe(false)
  })
})

describe('photoGridUtils - columnsForViewport', () => {
  it('默认 10 列', () => {
    expect(columnsForViewport(1920)).toBe(10)
  })
  it('≤1400 → 8 列', () => {
    expect(columnsForViewport(1400)).toBe(8)
    expect(columnsForViewport(1300)).toBe(8)
  })
  it('≤1200 → 6 列', () => {
    expect(columnsForViewport(1200)).toBe(6)
  })
  it('≤992 → 5 列', () => {
    expect(columnsForViewport(992)).toBe(5)
  })
  it('≤480 → 2 列', () => {
    expect(columnsForViewport(480)).toBe(2)
    expect(columnsForViewport(375)).toBe(2)
  })
})
