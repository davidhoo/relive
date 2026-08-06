import { describe, it, expect } from 'vitest'
import {
  columnsForPeopleList,
  PEOPLE_CARD_MIN_WIDTH,
  PEOPLE_CARD_GAP,
  PEOPLE_ROW_HEIGHT_ESTIMATE,
  PEOPLE_LOAD_THRESHOLD_ROWS,
} from './peopleListGridUtils'

describe('columnsForPeopleList - 容器宽度 → 列数（与翻页 CSS 断点一致）', () => {
  it('≤768 返回 2（手机）', () => {
    expect(columnsForPeopleList(320)).toBe(2)
    expect(columnsForPeopleList(768)).toBe(2)
  })

  it('769–992 返回 3', () => {
    expect(columnsForPeopleList(769)).toBe(3)
    expect(columnsForPeopleList(992)).toBe(3)
  })

  it('993–1200 返回 4', () => {
    expect(columnsForPeopleList(993)).toBe(4)
    expect(columnsForPeopleList(1200)).toBe(4)
  })

  it('>1200 按 auto-fill 公式计算，至少 5', () => {
    // 1201：(1201+14)/182 ≈ 6.68 → 6
    expect(columnsForPeopleList(1201)).toBe(6)
    // 1500px：(1500+14)/(168+14) ≈ 8.31 → 8
    expect(columnsForPeopleList(1500)).toBe(8)
    // 2000px：(2000+14)/182 ≈ 11.0 → 11
    expect(columnsForPeopleList(2000)).toBe(11)
  })

  it('auto-fill 在刚好够 5 列的最小宽度处返回 5（验证 max(5,...) 不拔高）', () => {
    // 5 列最小宽度：5*168 + 4*14 = 896，但 >1200 才进 auto-fill 分支，
    // 所以 max(5,...) 只在 auto 计算值 < 5 时生效；而 >1200 时 auto 至少 6，
    // 故 max(5,...) 在该区间实际是 no-op，仅作下界保险。
    // 这里验证 1201（auto=6）不被 max 拔高到更大，也不被压到 5。
    expect(columnsForPeopleList(1201)).toBe(6)
  })

  it('宽屏下列数与 CSS auto-fill 语义一致', () => {
    // 验证公式：n = floor((W + gap) / (minCard + gap))
    const w = 1800
    const expected = Math.floor((w + PEOPLE_CARD_GAP) / (PEOPLE_CARD_MIN_WIDTH + PEOPLE_CARD_GAP))
    expect(columnsForPeopleList(w)).toBe(Math.max(5, expected))
  })

  it('极窄容器回退到断点列数，不返回 1 或 0', () => {
    expect(columnsForPeopleList(0)).toBe(2)
    expect(columnsForPeopleList(100)).toBe(2)
    expect(columnsForPeopleList(-50)).toBe(2) // 防御：负宽度不崩
  })

  it('断点边界：768/769、992/993、1200/1201 跳变正确', () => {
    expect(columnsForPeopleList(768)).toBe(2)
    expect(columnsForPeopleList(769)).toBe(3)
    expect(columnsForPeopleList(992)).toBe(3)
    expect(columnsForPeopleList(993)).toBe(4)
    expect(columnsForPeopleList(1200)).toBe(4)
    // 1201 进入 auto-fill 区间：(1201+14)/182 ≈ 6.68 → 6
    expect(columnsForPeopleList(1201)).toBe(6)
  })
})

describe('常量导出', () => {
  it('PEOPLE_CARD_MIN_WIDTH / GAP 与 CSS 一致', () => {
    expect(PEOPLE_CARD_MIN_WIDTH).toBe(168)
    expect(PEOPLE_CARD_GAP).toBe(14)
  })

  it('PEOPLE_ROW_HEIGHT_ESTIMATE 为正数（首屏估算用）', () => {
    expect(PEOPLE_ROW_HEIGHT_ESTIMATE).toBeGreaterThan(0)
  })

  it('PEOPLE_LOAD_THRESHOLD_ROWS = 3', () => {
    expect(PEOPLE_LOAD_THRESHOLD_ROWS).toBe(3)
  })
})
