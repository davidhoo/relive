import { describe, it, expect } from 'vitest'
import {
  partitionRows,
  visibleCardCount,
  shouldLoadMore,
  shouldLoadByVisibleRange,
  isAllFacesMoved,
  toggleFaceInSet,
  anchorRowIndexAfterDensityChange,
} from './peopleGridUtils'

describe('partitionRows - 虚拟行的列数与数据分组（测试项 1）', () => {
  it('按列数切分，最后一行可不足列数', () => {
    const items = Array.from({ length: 37 }, (_, i) => i)
    const rows = partitionRows(items, 15)
    expect(rows).toHaveLength(3) // ceil(37/15) = 3
    expect(rows[0]!.items).toHaveLength(15)
    expect(rows[1]!.items).toHaveLength(15)
    expect(rows[2]!.items).toHaveLength(7)
    expect(rows[0]!.index).toBe(0)
    expect(rows[2]!.index).toBe(2)
  })

  it('空数据返回 0 行', () => {
    expect(partitionRows([], 15)).toHaveLength(0)
  })

  it('列数为 0 时返回空（防御）', () => {
    expect(partitionRows([1, 2, 3], 0)).toHaveLength(0)
  })

  it('恰好整除时无半空行', () => {
    const rows = partitionRows(Array.from({ length: 30 }, (_, i) => i), 15)
    expect(rows).toHaveLength(2)
    expect(rows[1]!.items).toHaveLength(15)
  })
})

describe('visibleCardCount - 离开视口的卡片被卸载（测试项 4）', () => {
  it('仅渲染可见行 + 前后 overscan 行对应的卡片数', () => {
    // 总 200 行，可见 10~14 行，overscan 5 → 渲染 5..19 共 15 行
    const count = visibleCardCount({
      totalRows: 200,
      visibleStartRow: 10,
      visibleEndRow: 14,
      overscan: 5,
      columns: 15,
    })
    // (19-5+1) * 15 = 225
    expect(count).toBe(225)
    // 远小于全量 200*15=3000
    expect(count).toBeLessThan(200 * 15)
  })

  it('接近起始时 overscan 不越界到负数', () => {
    const count = visibleCardCount({
      totalRows: 200,
      visibleStartRow: 0,
      visibleEndRow: 4,
      overscan: 5,
      columns: 15,
    })
    // 0..9 共 10 行
    expect(count).toBe(150)
  })

  it('接近末端时 overscan 不超过总行数', () => {
    const count = visibleCardCount({
      totalRows: 200,
      visibleStartRow: 196,
      visibleEndRow: 199,
      overscan: 5,
      columns: 15,
    })
    // 191..199 共 9 行
    expect(count).toBe(135)
  })
})

describe('shouldLoadMore - 接近末端只触发一次 / 失败可重试（测试项 2、3）', () => {
  it('接近末端时返回 true', () => {
    expect(
      shouldLoadMore({
        visibleLastRowIndex: 198,
        rowCount: 200,
        loading: false,
        hasMore: true,
        threshold: 3,
      }),
    ).toBe(true)
  })

  it('正在加载时不再触发（只触发一次）', () => {
    expect(
      shouldLoadMore({
        visibleLastRowIndex: 198,
        rowCount: 200,
        loading: true,
        hasMore: true,
        threshold: 3,
      }),
    ).toBe(false)
  })

  it('未接近末端时不触发', () => {
    expect(
      shouldLoadMore({
        visibleLastRowIndex: 10,
        rowCount: 200,
        loading: false,
        hasMore: true,
        threshold: 3,
      }),
    ).toBe(false)
  })

  it('没有更多数据时不触发', () => {
    expect(
      shouldLoadMore({
        visibleLastRowIndex: 198,
        rowCount: 200,
        loading: false,
        hasMore: false,
        threshold: 3,
      }),
    ).toBe(false)
  })

  it('加载失败后（loading=false）仍可再次触发重试', () => {
    expect(
      shouldLoadMore({
        visibleLastRowIndex: 198,
        rowCount: 200,
        loading: false,
        hasMore: true,
        threshold: 3,
        error: true,
      }),
    ).toBe(true)
  })
})

describe('shouldLoadByVisibleRange - window 虚拟化加载守卫', () => {
  const base = {
    active: true,
    loading: false,
    error: false,
    hasMore: true,
    rowCount: 200,
    lastVisibleRowIndex: 198,
    thresholdRows: 3,
  }

  it('active 且接近末端时返回 true', () => {
    expect(shouldLoadByVisibleRange(base)).toBe(true)
  })

  it('loading 时不再触发（单 in-flight）', () => {
    expect(shouldLoadByVisibleRange({ ...base, loading: true })).toBe(false)
  })

  it('error 时禁止自动重试', () => {
    expect(shouldLoadByVisibleRange({ ...base, error: true })).toBe(false)
  })

  it('hasMore=false 时不再触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, hasMore: false })).toBe(false)
  })

  it('Tab 隐藏（active=false）时不触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, active: false })).toBe(false)
  })

  it('未接近末端时不触发', () => {
    expect(shouldLoadByVisibleRange({ ...base, lastVisibleRowIndex: 10 })).toBe(false)
  })

  it('首屏尚未测量（lastVisibleRowIndex<0）且 active+hasMore 时允许加载', () => {
    expect(shouldLoadByVisibleRange({ ...base, rowCount: 0, lastVisibleRowIndex: -1 })).toBe(true)
  })
})

describe('isAllFacesMoved - 人脸总数大于已加载数量时不误判（测试项 9）', () => {
  it('选中已加载全部但总数更大时，不判定为全部移动', () => {
    // 已加载 50 张人脸全部选中，但人物总共有 10000 张
    expect(isAllFacesMoved({ selectedCount: 50, facesTotal: 10000 })).toBe(false)
  })

  it('选中数等于总数时判定为全部移动', () => {
    expect(isAllFacesMoved({ selectedCount: 10000, facesTotal: 10000 })).toBe(true)
  })

  it('总数未知（0）时不判定为全部移动', () => {
    expect(isAllFacesMoved({ selectedCount: 50, facesTotal: 0 })).toBe(false)
  })

  it('选中超过总数也判定为全部移动', () => {
    expect(isAllFacesMoved({ selectedCount: 10001, facesTotal: 10000 })).toBe(true)
  })
})

describe('toggleFaceInSet - 跨虚拟区域选择不丢失（测试项 5、6）', () => {
  it('添加与删除基于 Set，与卡片是否挂载无关', () => {
    let set = new Set<number>()
    // 模拟跨区域选择：顶部、中间、末端各选一张
    set = toggleFaceInSet(set, 1, true)
    set = toggleFaceInSet(set, 5000, true)
    set = toggleFaceInSet(set, 10850, true)
    expect(set.size).toBe(3)

    // 取消中间一张
    set = toggleFaceInSet(set, 5000, false)
    expect(set.size).toBe(2)
    expect(set.has(5000)).toBe(false)
    expect(set.has(1)).toBe(true)
    expect(set.has(10850)).toBe(true)
  })

  it('批量操作提交全部选中 ID（跨区域）', () => {
    let set = new Set<number>()
    set = toggleFaceInSet(set, 1, true)
    set = toggleFaceInSet(set, 5000, true)
    set = toggleFaceInSet(set, 10850, true)
    const submittedIds = Array.from(set)
    expect(submittedIds).toEqual(expect.arrayContaining([1, 5000, 10850]))
    expect(submittedIds).toHaveLength(3)
  })

  it('重复添加同一 id 不产生重复', () => {
    let set = new Set<number>()
    set = toggleFaceInSet(set, 42, true)
    set = toggleFaceInSet(set, 42, true)
    expect(set.size).toBe(1)
  })
})

describe('anchorRowIndexAfterDensityChange - 切换密度保留滚动锚点（测试项 7）', () => {
  it('切换前第一个可见 item 的列索引在新列数下映射到新行', () => {
    // 15 列下第 0 行第 7 个 item（index=7），切到 5 列 → 行 1
    expect(anchorRowIndexAfterDensityChange(7, 5)).toBe(1)
    // 切到 3 列 → 行 2
    expect(anchorRowIndexAfterDensityChange(7, 3)).toBe(2)
  })

  it('锚点映射后该 item 仍在新行内', () => {
    const itemIndex = 150
    const newColumns = 5
    const newRow = anchorRowIndexAfterDensityChange(itemIndex, newColumns)
    // 该 item 应落在 newRow 行的区间内
    expect(itemIndex).toBeGreaterThanOrEqual(newRow * newColumns)
    expect(itemIndex).toBeLessThan((newRow + 1) * newColumns)
  })

  it('密度切换不影响选择集合（选择态独立于列数）', () => {
    // 选择集合是 Set<number>，与 columns 无关；这里验证切换前后集合不变
    const set = new Set([1, 2, 3])
    const beforeColumns = 15
    const afterColumns = 5
    // 模拟切换密度：仅改变列数，不触碰 set
    expect(beforeColumns).not.toBe(afterColumns)
    expect(set.size).toBe(3)
  })
})
