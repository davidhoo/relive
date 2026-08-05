/**
 * photoGridUtils: 照片管理页连续浏览的纯逻辑辅助函数（无 DOM/组件依赖，便于单测）。
 *
 * 复用人物详情页的 shouldLoadByVisibleRange 语义，但照片管理页有额外的“分页停滞保护”
 * 与“请求代际”判定，这里独立实现，避免与 peopleGridUtils 耦合。
 */

/** 浏览模式。pagination=传统翻页，continuous=连续浏览。 */
export type BrowseMode = 'pagination' | 'continuous'

export const BROWSE_MODE_STORAGE_KEY = 'relive.photos.browseMode'

/** 读取浏览模式。非法/不存在降级为 pagination。 */
export function readBrowseMode(stored: string | null): BrowseMode {
  return stored === 'continuous' ? 'continuous' : 'pagination'
}

/** 是否接近列表末尾（真实可见区间，不含 overscan）。 */
export function shouldLoadByVisibleRange(p: {
  active: boolean
  loading: boolean
  error: boolean
  hasMore: boolean
  rowCount: number
  lastVisibleRowIndex: number
  thresholdRows: number
}): boolean {
  if (!p.active) return false
  if (p.loading) return false
  if (p.error) return false
  if (!p.hasMore) return false
  if (p.rowCount <= 0) return true // 首屏
  if (p.lastVisibleRowIndex < 0) return false
  return p.rowCount - p.lastVisibleRowIndex <= p.thresholdRows
}

/**
 * appendDedup 将新批次追加到已加载数组，按照片 ID 去重。
 * 返回 { items, fresh }：items 为追加后数组，fresh 为实际新增项数。
 * 泛型 T 约束为含 id 的对象，保留原类型。
 */
export function appendDedup<T extends { id: number }>(existing: T[], batch: T[]): {
  items: T[]
  fresh: number
} {
  const seen = new Set(existing.map(p => p.id))
  const freshItems: T[] = []
  for (const p of batch) {
    if (!seen.has(p.id)) {
      seen.add(p.id)
      freshItems.push(p)
    }
  }
  return { items: [...existing, ...freshItems], fresh: freshItems.length }
}

/**
 * isCursorStalled 判定分页是否停滞（防风暴）。
 * hasMore=true 但 nextCursor 为空、或与请求 cursor 相同、或已被消费过 → 停滞。
 */
export function isCursorStalled(p: {
  hasMore: boolean
  nextCursor: string
  requestCursor: string
  consumedCursors: Set<string>
}): boolean {
  if (!p.hasMore) return false
  if (p.nextCursor === '') return true
  if (p.nextCursor === p.requestCursor) return true
  if (p.consumedCursors.has(p.nextCursor)) return true
  return false
}

/**
 * 视口宽度 → 照片网格列数（与 index.vue 的 CSS 媒体查询一致）。
 * 默认 10 列；≤1400 8 列；≤1200 6 列；≤992 5 列；≤480 2 列。
 */
export function columnsForViewport(width: number): number {
  if (width <= 480) return 2
  if (width <= 992) return 5
  if (width <= 1200) return 6
  if (width <= 1400) return 8
  return 10
}
