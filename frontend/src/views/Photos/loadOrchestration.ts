/**
 * 照片管理页首屏加载编排的纯逻辑辅助函数。
 *
 * 这些函数从 index.vue 抽出，便于在不引入 DOM 的情况下做单元测试：
 * - 首屏照片列表请求应携带 no_total=true
 * - 扫描路径折叠时不加载派生状态
 * - 扫描路径展开时派生状态延后加载
 * - 快速切换筛选/分页时，旧请求结果不得覆盖新状态
 */

/** 首屏照片列表请求参数。no_total 恒为 true（首屏优先展示网格，不等 total）。 */
export type FirstScreenListParams = {
  page: number
  page_size: number
  no_total: true
  [key: string]: number | string | boolean
}

/**
 * buildFirstScreenListParams 构建首屏照片列表请求参数。
 * 首屏优先展示照片网格，不等待 total，故携带 no_total=true。
 */
export function buildFirstScreenListParams(
  page: number,
  pageSize: number,
  filters: Record<string, string | boolean | undefined>,
): FirstScreenListParams {
  const params: FirstScreenListParams = {
    page,
    page_size: pageSize,
    no_total: true,
  }
  for (const [key, value] of Object.entries(filters)) {
    if (value === undefined || value === '' || value === false) continue
    params[key] = value
  }
  return params
}

/**
 * shouldLoadDerivedStatusOnMount 决定首屏挂载后是否加载扫描路径派生状态。
 * 仅当扫描路径展开时才加载；折叠态不请求 /photos/derived-status-by-paths。
 */
export function shouldLoadDerivedStatusOnMount(scanPathsCollapsed: boolean): boolean {
  return !scanPathsCollapsed
}

/**
 * shouldLoadDerivedStatusAfterListResolved 决定照片列表返回后是否加载派生状态。
 * 扫描路径展开时，派生统计请求在照片列表返回后再执行。
 */
export function shouldLoadDerivedStatusAfterListResolved(scanPathsCollapsed: boolean): boolean {
  return !scanPathsCollapsed
}

/**
 * isStaleRequest 判断请求结果是否已过期（快速切换筛选/分页时旧请求不得覆盖新状态）。
 *
 * 每次发起请求自增一个序号 reqId 并记录；结果返回时若 localReqId !== currentReqId，
 * 说明已有更新的请求发出，当前结果应丢弃。
 */
export function isStaleRequest(localReqId: number, currentReqId: number): boolean {
  return localReqId !== currentReqId
}

/**
 * nextReqId 生成下一个请求序号。取 currentReqId + 1，保证单调递增。
 */
export function nextReqId(currentReqId: number): number {
  return currentReqId + 1
}
