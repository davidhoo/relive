/**
 * photoListViewState: 照片管理页连续浏览的“详情返回恢复”快照。
 *
 * 仅内存快照（模块级单例），不写入 localStorage / 数据库。整页刷新后快照丢失，
 * 连续列表从第一批重新加载——这是设计要求（九、不包含 第 5 条）。
 *
 * 快照在“连续浏览模式下进入照片详情前”保存；返回列表时若筛选条件完全匹配则恢复数据、
 * 游标、hasMore/finished、滚动位置与首个可见锚点。不为了恢复位置而重新挂载此前全部 DOM。
 */

import type { Photo } from '@/types/photo'

/** 连续浏览的筛选条件指纹。任一字段不同即视为“筛选条件已变”，不恢复。 */
export interface PhotoListFilterFingerprint {
  search: string
  category: string
  tag: string
  analyzed: string
  has_thumbnail: string
  has_gps: string
  status: string
}

/** 连续浏览快照。仅保存恢复所需的最小信息。 */
export interface PhotoListSnapshot {
  /** 已加载照片摘要（仅恢复用，不持久化）。 */
  photos: Photo[]
  /** 下一页游标（opaque 字符串）。 */
  nextCursor: string
  /** 是否还有更多。 */
  hasMore: boolean
  /** 是否已加载完成。 */
  finished: boolean
  /** 当前筛选条件指纹，恢复时必须完全匹配。 */
  filter: PhotoListFilterFingerprint
  /** 当前总数（页面标题用）。 */
  total: number
  /** .main-content 滚动位置（px）。 */
  scrollTop: number
  /** 第一个可见照片的 ID（用于虚拟网格锚点恢复）。 */
  firstVisiblePhotoId: number | null
}

// 用模块级闭包变量保存快照（不暴露给外部直接修改）。
let snapshot: PhotoListSnapshot | null = null

/** savePhotoListSnapshot 保存连续浏览快照。 */
export function savePhotoListSnapshot(s: PhotoListSnapshot): void {
  snapshot = {
    photos: s.photos,
    nextCursor: s.nextCursor,
    hasMore: s.hasMore,
    finished: s.finished,
    filter: { ...s.filter },
    total: s.total,
    scrollTop: s.scrollTop,
    firstVisiblePhotoId: s.firstVisiblePhotoId,
  }
}

/** consumePhotoListSnapshot 取出并清除快照。筛选条件不匹配时返回 null（不恢复）。 */
export function consumePhotoListSnapshot(filter: PhotoListFilterFingerprint): PhotoListSnapshot | null {
  if (!snapshot) return null
  if (!filtersEqual(snapshot.filter, filter)) {
    snapshot = null
    return null
  }
  const s = snapshot
  snapshot = null
  return s
}

/** clearPhotoListSnapshot 清除快照（筛选变化 / 模式切换时调用）。 */
export function clearPhotoListSnapshot(): void {
  snapshot = null
}

/** hasPhotoListSnapshot 判断当前是否存在快照（调试用）。 */
export function hasPhotoListSnapshot(): boolean {
  return snapshot !== null
}

/** filtersEqual 比较两组筛选条件指纹是否完全一致。 */
export function filtersEqual(a: PhotoListFilterFingerprint, b: PhotoListFilterFingerprint): boolean {
  return (
    a.search === b.search &&
    a.category === b.category &&
    a.tag === b.tag &&
    a.analyzed === b.analyzed &&
    a.has_thumbnail === b.has_thumbnail &&
    a.has_gps === b.has_gps &&
    a.status === b.status
  )
}
