// peopleGridUtils: 人物详情页虚拟网格与选择态的纯逻辑，无 DOM/组件依赖，便于单测。

export type GridSize = 'small' | 'medium' | 'large'

export interface GridRow<T> {
  index: number
  items: T[]
}

// 把一维已加载数据按列数切成二维行。最后一行可能不足 columns 个。
export function partitionRows<T>(items: T[], columns: number): GridRow<T>[] {
  if (columns <= 0) return []
  const rows: GridRow<T>[] = []
  for (let i = 0; i < items.length; i += columns) {
    rows.push({ index: rows.length, items: items.slice(i, i + columns) })
  }
  return rows
}

// 计算给定可见行区间内的卡片总数（含 overscan 行），用于断言 DOM 卡片数量有界。
export function visibleCardCount(params: {
  totalRows: number
  visibleStartRow: number
  visibleEndRow: number
  overscan: number
  columns: number
}): number {
  const start = Math.max(0, params.visibleStartRow - params.overscan)
  const end = Math.min(params.totalRows - 1, params.visibleEndRow + params.overscan)
  if (end < start) return 0
  return (end - start + 1) * params.columns
}

export interface ShouldLoadMoreParams {
  // 最后一个可见行的索引
  visibleLastRowIndex: number
  // 已加载数据对应的总行数
  rowCount: number
  // 是否正在加载
  loading: boolean
  // 是否还有更多数据
  hasMore: boolean
  // 距末端不足多少行触发
  threshold: number
  // 是否处于加载失败状态（失败状态下仍允许重试触发）
  error?: boolean
}

// 判断是否应触发下一页加载。loading=true 时不再触发；接近末端或失败重试时触发。
export function shouldLoadMore(p: ShouldLoadMoreParams): boolean {
  if (p.loading || !p.hasMore) return false
  if (p.rowCount <= 0) return false
  return p.rowCount - p.visibleLastRowIndex <= p.threshold
}

// 判断“是否移动了人物全部人脸”：基于人脸总数（分页 total）而非已加载长度。
// selectedCount >= facesTotal 且 facesTotal > 0 才认为全部已移动。
export function isAllFacesMoved(params: {
  selectedCount: number
  facesTotal: number
}): boolean {
  return params.facesTotal > 0 && params.selectedCount >= params.facesTotal
}

// 跨虚拟区域人脸选择集合操作（Set 语义）。
export function toggleFaceInSet(set: Set<number>, faceId: number, checked: boolean): Set<number> {
  const next = new Set(set)
  if (checked) {
    next.add(faceId)
  } else {
    next.delete(faceId)
  }
  return next
}

// 密度切换的滚动锚点：切换前列索引 → 切换后该 item 的新行索引。
export function anchorRowIndexAfterDensityChange(itemIndex: number, newColumns: number): number {
  if (newColumns <= 0) return 0
  return Math.floor(itemIndex / newColumns)
}
