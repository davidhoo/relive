/**
 * peopleListGridUtils: 人物管理列表连续浏览虚拟网格的纯逻辑（无 DOM/组件依赖，便于单测）。
 *
 * 与人物详情页的 peopleGridUtils 不同：人物列表没有密度切换，列数只随列表容器宽度变化；
 * 翻页模式仍是普通 CSS auto-fill 网格，连续浏览模式才走 VirtualMediaGrid。
 * 因此列数计算独立实现，不与详情页 columnsForSize 耦合。
 */

/** 人物卡片最小宽度（px），与 .people-card-grid 的 minmax(168px, 1fr) 一致。 */
export const PEOPLE_CARD_MIN_WIDTH = 168
/** 人物卡片间距（px），与 .people-card-grid 的 gap: 14px 一致。 */
export const PEOPLE_CARD_GAP = 14
/** 连续浏览虚拟网格的估算行高（px），仅首屏估算用，真实行高由 measureElement 测量。 */
export const PEOPLE_ROW_HEIGHT_ESTIMATE = 240
/** 接近末尾触发下一页的行阈值。 */
export const PEOPLE_LOAD_THRESHOLD_ROWS = 3

/**
 * 列表容器宽度 → 连续浏览列数。
 *
 * 断点与翻页模式 CSS 媒体查询保持一致，保证两种模式在同一宽度下列数相同：
 *   ≤768  → 2（手机）
 *   769–992  → 3
 *   993–1200 → 4
 *   >1200    → 按 auto-fill 语义计算：floor((W + gap) / (minCard + gap))，至少 5。
 *
 * auto-fill 公式镜像 CSS `repeat(auto-fill, minmax(168px, 1fr))`：
 * 浏览器以"每列最小宽度 = 168px、列间距 = 14px"计算最多能放多少列。
 * 即列数 n 满足 n*168 + (n-1)*14 ≤ W，解得 n ≤ (W + 14) / (168 + 14)。
 * 宽度不足时回退到断点列数，避免极窄容器算出 1 列。
 */
export function columnsForPeopleList(containerWidth: number): number {
  if (containerWidth <= 768) return 2
  if (containerWidth <= 992) return 3
  if (containerWidth <= 1200) return 4
  const auto = Math.floor(
    (containerWidth + PEOPLE_CARD_GAP) / (PEOPLE_CARD_MIN_WIDTH + PEOPLE_CARD_GAP),
  )
  return Math.max(5, auto)
}
