import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import VirtualMediaGrid from '@/components/VirtualMediaGrid.vue'
// VirtualMediaGrid 组件级测试。
//
// 滚动源回归：网格现在以最近的 .main-content 作为滚动元素（useVirtualizer 元素模式），
// 不再用 window。元素模式 virtualizer 的可见尺寸来自 scrollElement 的 offsetHeight（getRect），
// 滚动偏移来自 scrollElement.scrollTop，挂载时通过 observeElementRect 同步读一次尺寸填入 scrollRect。
//
// 因此测试必须在挂载前就把 .main-content 的 offsetHeight/offsetWidth/scrollTop/rect 桩好：
// 预建 .main-content DOM 节点 → 桩属性 → 用 attachTo 挂载组件到该节点内。
// 行高通过桩 HTMLElement.prototype.offsetHeight 给定（measureElement 读 offsetHeight）。
//
// 自动滚动回归（P0）：旧实现在分页追加后调用全量 measure()，清空 itemSizeCache，
// virtualizer 对视口上方行的 estimate→actual 差值做 applyScrollAdjustment 改写 scrollTop，
// 推进真实可见区间 → visible-range-change → 父组件再次分页 → 无限闭环。
// 回归测试用真实行高（160）≠ 估算行高（110）复现，修复后 scrollTop 不得自动增加、
// 无用户滚动时不得重复派发推进后的可见区间。

// jsdom 不提供 ResizeObserver。需要两类桩：
//  1. virtual-core 内部 observer（measureElement 注册节点用）——空实现即可，让注册路径不抛错；
//  2. 组件自身的 ResizeObserver（监听网格容器布局变化）——可控实现，测试可手动派发回调，
//     用于验证“纯高度变化不触发 measure / 不改写 scrollTop”。
// 这里导出可控实现：observe 记录 target，trigger(entry) 手动派发回调。组件 RO 与
// virtual-core 内部 RO 共用同一个全局类，因此都能被测试驱动。
class ControllableResizeObserver {
  callback: ResizeObserverCallback
  targets = new Set<Element>()
  static instances: ControllableResizeObserver[] = []
  constructor(cb: ResizeObserverCallback) {
    this.callback = cb
    ControllableResizeObserver.instances.push(this)
  }
  observe(t: Element) { this.targets.add(t) }
  unobserve(t: Element) { this.targets.delete(t) }
  disconnect() { this.targets.clear() }
  // 测试用：手动派发一次 resize 回调，模拟浏览器在尺寸变化时通知 observer。
  trigger(entry: { target: Element; borderBoxSize?: { inlineSize: number; blockSize: number }[]; contentRect?: DOMRectReadOnly }) {
    this.callback([entry as any], this)
  }
}
;(globalThis as any).ResizeObserver = ControllableResizeObserver
;(window as any).ResizeObserver = ControllableResizeObserver

// 每个用例前清空实例记录，避免跨用例污染。
const resetROInstances = () => { ControllableResizeObserver.instances = [] }
// 取监听指定元素的 RO 实例（组件 RO observe 的是网格容器 .virtual-media-grid）。
const roFor = (el: Element) =>
  ControllableResizeObserver.instances.find(ro => ro.targets.has(el))


// 预建 .main-content 滚动容器 DOM，桩尺寸/偏移/rect。返回容器与 setTop。
const buildScroller = (opts: {
  scrollerTop?: number
  scrollTop?: number
  scrollerHeight?: number
  scrollerWidth?: number
} = {}) => {
  const {
    scrollerTop = 0,
    scrollTop = 0,
    scrollerHeight = 800,
    scrollerWidth = 1000,
  } = opts
  const scroller = document.createElement('div')
  scroller.className = 'main-content'
  Object.defineProperty(scroller, 'offsetHeight', { configurable: true, get: () => scrollerHeight })
  Object.defineProperty(scroller, 'offsetWidth', { configurable: true, get: () => scrollerWidth })
  let top = scrollTop
  // 记录通过 scrollTo 写入的 scrollTop（virtualizer 滚动补偿走 scrollToFn → element.scrollTo）。
  // 自动滚动回归据此断言“追加数据是否改写了滚动位置”。
  const scrollToCalls: number[] = []
  Object.defineProperty(scroller, 'scrollTop', {
    configurable: true,
    get() { return top },
    set(v: number) { top = v },
  })
  // virtual-core elementScroll 调用 scrollElement.scrollTo({ top })。jsdom 不提供，桩之并记录。
  ;(scroller as any).scrollTo = (v: number | { top?: number }) => {
    const t = typeof v === 'number' ? v : (v && typeof v.top === 'number' ? v.top : top)
    scrollToCalls.push(t)
    top = t
  }
  Object.defineProperty(scroller, 'clientHeight', { configurable: true, get: () => scrollerHeight })
  Object.defineProperty(scroller, 'clientWidth', { configurable: true, get: () => scrollerWidth })
  vi.spyOn(scroller, 'getBoundingClientRect').mockReturnValue({
    top: scrollerTop, left: 0, right: scrollerWidth, bottom: scrollerTop + scrollerHeight,
    width: scrollerWidth, height: scrollerHeight, x: 0, y: scrollerTop, toJSON: () => ({}),
  } as DOMRect)
  const setTop = (v: number) => { top = v }
  const getTop = () => top
  return { scroller, setTop, getTop, scrollToCalls }
}

// 把网格挂载到预建的 .main-content 内。
const mountInScroller = (props: Record<string, unknown>, scrollerOpts: Parameters<typeof buildScroller>[0] = {}) => {
  const built = buildScroller(scrollerOpts)
  const { scroller, setTop } = built
  document.body.appendChild(scroller)
  const wrapper = mount(
    {
      components: { VirtualMediaGrid },
      template: `<VirtualMediaGrid
        :items="items" :columns="columns" :row-height="rowHeight" :gap="gap"
        :size-class="sizeClass"
        @visible-range-change="$emit('visible-range-change', $event)"
      ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
      props: ['items', 'columns', 'rowHeight', 'gap', 'sizeClass'],
    },
    {
      props: {
        items: Array.from({ length: 100 }, (_, i) => ({ id: i + 1 })),
        columns: 15,
        rowHeight: 110,
        gap: 10,
        sizeClass: 'small',
        ...props,
      },
      attachTo: scroller,
    },
  )
  return { wrapper, ...built }
}

// 简化挂载：用默认 800px 高 .main-content。
const mountGrid = (props: Record<string, unknown>) => mountInScroller(props).wrapper

// 桩全局行高：measureElement 读 element.offsetHeight。返回恢复函数。
const stubRowOffsetHeight = (rowHeight: number) => {
  const desc = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get() { return rowHeight },
  })
  return () => {
    if (desc) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', desc)
  }
}

const translateYOf = (el: HTMLElement): number => {
  const m = el.style.transform.match(/translateY\(([-0-9.]+)px\)/)
  return m ? parseFloat(m[1] ?? '0') : 0
}

describe('VirtualMediaGrid - 渲染行有界（测试项 4 组件级）', () => {
  it('挂载后 inner 容器存在', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ items: Array.from({ length: 500 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const inner = wrapper.find('.virtual-media-grid-inner')
      expect(inner.exists()).toBe(true)
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

describe('VirtualMediaGrid - 暴露锚点与测量方法（测试项 7、8）', () => {
  it('暴露 getFirstVisibleIndex / scrollToIndex / measure / getScrollOffset / scrollToOffset', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({})
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      expect(typeof vm.getFirstVisibleIndex).toBe('function')
      expect(typeof vm.scrollToIndex).toBe('function')
      expect(typeof vm.measure).toBe('function')
      expect(typeof vm.getScrollOffset).toBe('function')
      expect(typeof vm.scrollToOffset).toBe('function')
      expect(vm.getFirstVisibleIndex()).toBeGreaterThanOrEqual(0)
      expect(() => vm.scrollToIndex(5)).not.toThrow()
      expect(() => vm.measure()).not.toThrow()
      expect(vm.getScrollOffset()).toBe(0)
      expect(() => vm.scrollToOffset(123)).not.toThrow()
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

describe('VirtualMediaGrid - 每行 items 不超过 columns（测试项 1 组件级）', () => {
  it('5 列下每行 child 数 ≤ 5', async () => {
    const restore = stubRowOffsetHeight(260)
    try {
      const wrapper = mountGrid({ columns: 5, items: Array.from({ length: 50 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      for (const row of rows) {
        expect(row.element.childElementCount).toBeLessThanOrEqual(5)
      }
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  // columns 是列数唯一来源：行内 grid-template-columns 由 columns prop 内联决定，
  // 不再依赖 sizeClass 的 CSS 媒体查询。sizeClass 仅作密度样式标识，不参与列数。
  it('columns prop 内联决定 grid-template-columns，与 sizeClass 无关（测试项 1）', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      // columns=7，sizeClass='small'（不对应任何 7 列媒体查询）
      const wrapper = mountGrid({ columns: 7, sizeClass: 'small', items: Array.from({ length: 70 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThan(0)
      for (const row of rows) {
        const tpl = (row.element as HTMLElement).style.gridTemplateColumns
        expect(tpl).toBe('repeat(7, minmax(0, 1fr))')
        // 每行 child 不超过 columns
        expect(row.element.childElementCount).toBeLessThanOrEqual(7)
      }
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('切换 columns prop 后行内 grid-template-columns 同步变化', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ columns: 5, items: Array.from({ length: 50 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      let rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThan(0)
      expect((rows[0]!.element as HTMLElement).style.gridTemplateColumns).toBe('repeat(5, minmax(0, 1fr))')

      await wrapper.setProps({ columns: 3 })
      await flushPromises()
      rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThan(0)
      expect((rows[0]!.element as HTMLElement).style.gridTemplateColumns).toBe('repeat(3, minmax(0, 1fr))')
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

describe('VirtualMediaGrid - 取最近 .main-content 作为滚动源（测试项 1）', () => {
  it('getScrollOffset 读取最近的 .main-content.scrollTop，而非 window', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const { wrapper, scroller } = mountInScroller({}, { scrollTop: 0 })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      expect(vm.gridRef).toBeTruthy()
      // 网格的 closest('.main-content') 即预建容器；getScrollOffset 读它的 scrollTop。
      expect(vm.getScrollOffset()).toBe(scroller.scrollTop)
      // window.scrollY 仍为 0（jsdom），证明不是从 window 取。
      expect(window.scrollY).toBe(0)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('嵌套多层时取最近的祖先 .main-content', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const outer = document.createElement('div')
      outer.className = 'main-content'
      Object.defineProperty(outer, 'offsetHeight', { configurable: true, get: () => 800 })
      Object.defineProperty(outer, 'offsetWidth', { configurable: true, get: () => 1000 })
      Object.defineProperty(outer, 'scrollTop', { configurable: true, get: () => 999, set() {} })
      vi.spyOn(outer, 'getBoundingClientRect').mockReturnValue({
        top: 0, left: 0, right: 1000, bottom: 800, width: 1000, height: 800, x: 0, y: 0, toJSON: () => ({}),
      } as DOMRect)

      const inner = document.createElement('div')
      inner.className = 'main-content'
      Object.defineProperty(inner, 'offsetHeight', { configurable: true, get: () => 600 })
      Object.defineProperty(inner, 'offsetWidth', { configurable: true, get: () => 1000 })
      Object.defineProperty(inner, 'scrollTop', { configurable: true, get: () => 42, set() {} })
      vi.spyOn(inner, 'getBoundingClientRect').mockReturnValue({
        top: 0, left: 0, right: 1000, bottom: 600, width: 1000, height: 600, x: 0, y: 0, toJSON: () => ({}),
      } as DOMRect)

      outer.appendChild(inner)
      document.body.appendChild(outer)

      const wrapper = mount(VirtualMediaGrid, {
        props: {
          items: Array.from({ length: 30 }, (_, i) => ({ id: i + 1 })),
          columns: 15, rowHeight: 110, gap: 10, sizeClass: 'small',
        },
        slots: { item: '<div class="cell">{{ item.id }}</div>' },
        attachTo: inner,
      })
      await flushPromises()
      const vm = wrapper.vm as any
      // 取最近的 inner，其 scrollTop=42，而非 outer 的 999。
      expect(vm.getScrollOffset()).toBe(42)
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

describe('VirtualMediaGrid - scrollMargin 相对滚动容器计算', () => {
  it('暴露 recomputeScrollMargin 且 measure 不抛错', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ items: Array.from({ length: 50 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      expect(typeof vm.recomputeScrollMargin).toBe('function')
      const grid = wrapper.find('.virtual-media-grid').element as HTMLElement
      vi.spyOn(grid, 'getBoundingClientRect').mockReturnValue({
        top: 300, left: 0, right: 0, bottom: 0, width: 1000, height: 10, x: 0, y: 300, toJSON: () => ({}),
      } as DOMRect)
      vm.recomputeScrollMargin()
      expect(() => vm.measure()).not.toThrow()
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

describe('VirtualMediaGrid - 数据追加后重新测量（不重置滚动）', () => {
  it('items 从 0 增长到非 0 后 measure 不抛错', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ items: [] })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      await wrapper.setProps({ items: Array.from({ length: 40 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      expect(() => vm.measure()).not.toThrow()
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

// 首行偏移回归：translateY 必须减去 scrollMargin，否则第一行被多下移一个 scrollMargin。
describe('VirtualMediaGrid - 首行 translateY 减去 scrollMargin（首行无空白）', () => {
  it('scrollMargin=0 时首行 transform 接近 translateY(0px)', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ items: Array.from({ length: 30 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      await flushPromises()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThan(0)
      const y = translateYOf(rows[0]!.element as HTMLElement)
      expect(Math.abs(y)).toBeLessThanOrEqual(1)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('scrollMargin=300 时首行 translate 仍接近 0（不减会变成 300px 空白）', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ items: Array.from({ length: 30 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      const grid = wrapper.find('.virtual-media-grid').element as HTMLElement
      vi.spyOn(grid, 'getBoundingClientRect').mockReturnValue({
        top: 300, left: 0, right: 0, bottom: 0, width: 1000, height: 10, x: 0, y: 300, toJSON: () => ({}),
      } as DOMRect)
      vm.recomputeScrollMargin()
      await flushPromises()
      expect(vm.gridRef).toBeTruthy()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThan(0)
      const y = translateYOf(rows[0]!.element as HTMLElement)
      expect(Math.abs(y)).toBeLessThanOrEqual(1)
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

describe('VirtualMediaGrid - 真实行位置断言（首行紧贴网格顶部、行不重叠）', () => {
  const stubRowRects = (rows: ReturnType<import('@vue/test-utils').VueWrapper['findAll']>, rowHeight: number) => {
    rows.forEach(r => {
      const y = translateYOf(r.element as HTMLElement)
      vi.spyOn(r.element as HTMLElement, 'getBoundingClientRect').mockReturnValue({
        top: y, left: 0, right: 0, bottom: y + rowHeight, width: 1000, height: rowHeight, x: 0, y, toJSON: () => ({}),
      } as DOMRect)
    })
  }

  const stubGridRect = (wrapper: import('@vue/test-utils').VueWrapper, top = 0) => {
    const grid = wrapper.find('.virtual-media-grid').element as HTMLElement
    vi.spyOn(grid, 'getBoundingClientRect').mockReturnValue({
      top, left: 0, right: 0, bottom: top + 800, width: 1000, height: 800, x: 0, y: top, toJSON: () => ({}),
    } as DOMRect)
  }

  it('小图密度：首行紧贴网格顶部，第二行在首行底部之后（不重叠）', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ columns: 15, rowHeight: 110, items: Array.from({ length: 60 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThanOrEqual(2)
      stubRowRects(rows, 110)
      stubGridRect(wrapper, 0)
      const first = rows[0]!.element.getBoundingClientRect()
      const second = rows[1]!.element.getBoundingClientRect()
      const grid = wrapper.find('.virtual-media-grid').element.getBoundingClientRect()
      expect(first.top).toBeCloseTo(grid.top, 1)
      expect(second.top).toBeGreaterThanOrEqual(first.bottom)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('页面头部高度变化（scrollMargin>0）：首行 translate 接近 0，无 scrollMargin 空白', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ columns: 15, rowHeight: 110, items: Array.from({ length: 60 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      stubGridRect(wrapper, 200)
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThanOrEqual(2)
      const firstY = translateYOf(rows[0]!.element as HTMLElement)
      expect(Math.abs(firstY)).toBeLessThanOrEqual(1)
      stubRowRects(rows, 110)
      const first = rows[0]!.element.getBoundingClientRect()
      const second = rows[1]!.element.getBoundingClientRect()
      expect(second.top).toBeGreaterThanOrEqual(first.bottom)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('追加第二页后首行位置不跳动（仍紧贴网格顶部）', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    const restore = stubRowOffsetHeight(110)
    try {
      const wrapper = mountGrid({ columns: 15, rowHeight: 110, items: Array.from({ length: 30 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      const rowsBefore = wrapper.findAll('.virtual-media-grid-row')
      const firstYBefore = translateYOf(rowsBefore[0]!.element as HTMLElement)

      await wrapper.setProps({ items: Array.from({ length: 60 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      vm.measure()
      await flushPromises()
      await flushPromises()
      const rowsAfter = wrapper.findAll('.virtual-media-grid-row')
      const firstYAfter = translateYOf(rowsAfter[0]!.element as HTMLElement)
      expect(Math.abs(firstYAfter - firstYBefore)).toBeLessThanOrEqual(1)

      stubRowRects(rowsAfter, 110)
      stubGridRect(wrapper, 0)
      const first = rowsAfter[0]!.element.getBoundingClientRect()
      const grid = wrapper.find('.virtual-media-grid').element.getBoundingClientRect()
      expect(first.top).toBeCloseTo(grid.top, 1)
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

// ===== 集成测试：真实 VirtualMediaGrid + 父级 .main-content 滚动容器（测试项 2、3、10）=====
//
// jsdom 不做真实滚动，但元素模式 virtualizer 的 scrollOffset 来自 scrollElement.scrollTop（桩入），
// 可见尺寸来自 scrollElement.offsetHeight（桩入）。改 scrollTop 后调用 measure() 触发重算，
// 验证可见行区间随 scrollTop 变化、接近底部时派发 visible-range-change。
// 这正是线上根因回归：window 模式下滚 .main-content 不触发；元素模式下 scrollTop 变化驱动。
describe('VirtualMediaGrid - .main-content 滚动驱动可见区间（集成，测试项 2、3、10）', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
  })

  it('scrollTop 变化后可见行区间发生变化（测试项 2）', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      // 600 项 / 15 = 40 行，行高 110 + gap 10 = 120/行，总高 4800。
      const { wrapper, scroller, setTop } = mountInScroller(
        { columns: 15, rowHeight: 110, items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) },
        { scrollerHeight: 800, scrollTop: 0 },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      const firstBefore = vm.getFirstVisibleIndex()
      expect(firstBefore).toBeGreaterThanOrEqual(0)

      // 滚到 2000px：改 scrollTop 后派发 scroll 事件，让 virtualizer 的
      // observeElementOffset 回调更新 scrollOffset（jsdom setter 不自动派发）。
      setTop(2000)
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()
      const firstAfter = vm.getFirstVisibleIndex()
      expect(firstAfter).toBeGreaterThan(firstBefore)
      expect(firstAfter).toBeGreaterThanOrEqual(12) // 至少跳过若干行
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('实际滚动容器接近列表底部时派发 visible-range-change（测试项 3）', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const events: { firstRowIndex: number; lastRowIndex: number; rowCount: number }[] = []
      const { scroller } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid
            :items="items" :columns="15" :row-height="110" :gap="10" :size-class="'small'"
            @visible-range-change="onRange"
          ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) }),
          methods: { onRange(p: any) { events.push(p) } },
        },
        { attachTo: scroller },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      // 40 行 * 120 = 4800 总高，视口 800；接近底部：scrollTop=4050 → 末端在 4800。
      // lastVisibleRowIndex 应接近 39（末行）。
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      events.length = 0

      // 模拟滚到底部附近：改 scrollTop + 派发 scroll 事件更新 virtualizer scrollOffset。
      ;(scroller as any).scrollTop = 4050
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()
      expect(events.length).toBeGreaterThan(0)
      const last = events[events.length - 1]!
      expect(last.rowCount).toBe(40)
      expect(last.lastRowIndex).toBeGreaterThanOrEqual(37) // 接近末端
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('长列表中实际挂载的卡片数量保持有界（测试项 10）', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const { wrapper, scroller, setTop } = mountInScroller(
        { columns: 15, rowHeight: 110, items: Array.from({ length: 3000 }, (_, i) => ({ id: i + 1 })) },
        { scrollerHeight: 800, scrollTop: 0 },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      // 3000/15=200 行；可见 800/120≈7 行 + overscan 5*2 = ~17 行，远小于 200。
      let rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThan(0)
      expect(rows.length).toBeLessThan(50)

      // 深度滚动后行数仍应有界。
      setTop(20000)
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()
      rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeLessThan(50)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  // ===== 真实视口范围（virtualizer.range）派发回归 =====
  //
  // 旧实现用 getVirtualItems() 末行派发事件，导致：
  //   1) 事件 lastRowIndex 含 overscan 行，被父组件误判为真实可见末行；
  //   2) 去重 key 含 rowCount，数据追加导致同一视口重复派发，触发请求风暴。
  // 改用 virtualizer.range.startIndex/endIndex 派发、去重 key 仅 startIndex-endIndex 后：
  //   - 事件范围严格等于真实视口（不含 overscan）；
  //   - 实际挂载末行可大于事件 lastRowIndex（因 overscan 预渲染）；
  //   - 仅追加一页、scrollTop 不变时不重复派发；scrollTop 变化才派发。

  it('事件范围与 virtualizer.range 一致，实际挂载最后一行可大于事件 lastRowIndex', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const events: { firstRowIndex: number; lastRowIndex: number; rowCount: number }[] = []
      const { scroller } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid
            :items="items" :columns="15" :row-height="110" :gap="10" :size-class="'small'"
            @visible-range-change="onRange"
          ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) }),
          methods: { onRange(p: any) { events.push(p) } },
        },
        { attachTo: scroller },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      // measure() 是重测尺寸/偏移；range 未变不应派发新事件。
      vm.measure()
      await flushPromises()
      await flushPromises()

      const rows = wrapper.findAll('.virtual-media-grid-row')
      const mountedIdx = rows.map(r => Number((r.element as HTMLElement).dataset.index))
      const maxMounted = Math.max(...mountedIdx)

      // 至少有一条事件，且事件 lastRowIndex 严格小于挂载末行（overscan 预渲染超出真实视口）。
      // 当数据足够长（600 项 / 40 行，视口只覆盖前 ~7 行）时，overscan 必然使挂载末行 > 真实末行。
      const ev = events[events.length - 1]
      expect(ev).toBeTruthy()
      expect(ev!.lastRowIndex).toBeLessThanOrEqual(maxMounted)
      // 严格小于：scrollTop=0 时真实可见末行约 6，挂载末行约 11（+overscan 5）。
      expect(maxMounted).toBeGreaterThan(ev!.lastRowIndex)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('仅追加一页数据、保持 scrollTop 不变：不因 rowCount 增长重复派发同一范围', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const events: { firstRowIndex: number; lastRowIndex: number; rowCount: number }[] = []
      const { scroller } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid
            :items="items" :columns="15" :row-height="110" :gap="10" :size-class="'small'"
            @visible-range-change="onRange"
          ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) }),
          methods: { onRange(p: any) { events.push(p) } },
        },
        { attachTo: scroller },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      // 排空初始派发。
      const initialCount = events.length
      void initialCount
      events.length = 0

      // 仅追加一页数据，scrollTop 不变：真实视口范围未动，不应派发新事件。
      await wrapper.setData({ items: Array.from({ length: 900 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      vm.measure()
      await flushPromises()
      await flushPromises()
      expect(events.length).toBe(0)

      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('用户真实滚动导致 range 改变时正常派发事件', async () => {
    const restore = stubRowOffsetHeight(110)
    try {
      const events: { firstRowIndex: number; lastRowIndex: number; rowCount: number }[] = []
      const { scroller } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid
            :items="items" :columns="15" :row-height="110" :gap="10" :size-class="'small'"
            @visible-range-change="onRange"
          ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) }),
          methods: { onRange(p: any) { events.push(p) } },
        },
        { attachTo: scroller },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      events.length = 0

      // 滚动一段距离 → 真实 range 改变 → 应派发事件。
      ;(scroller as any).scrollTop = 2000
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()
      expect(events.length).toBeGreaterThan(0)
      const ev = events[events.length - 1]!
      expect(ev.firstRowIndex).toBeGreaterThan(0)
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})

// ===== P0 自动滚动回归：真实行高 ≠ 估算行高 =====
//
// 线上根因：分页追加数据后调用 virtualizer.measure()（清空 itemSizeCache），virtualizer 对
// 视口上方行的 estimate→actual 差值做 applyScrollAdjustment，主动改写 scrollTop；scrollTop 变化
// 推进真实可见区间 → visible-range-change → 父组件判定接近末端 → 请求下一页 → 无限闭环。
//
// 这组测试用真实行高 160 ≠ 估算 110 复现：修复前，追加一页会让 scrollTop 自动增加、
// 可见区间被推进、并再次派发事件；修复后，scrollTop 不变、无重复派发、用户真实滚动到末端仍能加载。
// 真实行高通过桩 HTMLElement.prototype.offsetHeight = 160（measureElement 读 offsetHeight）给定。
describe('VirtualMediaGrid - 分页追加不自动滚动（P0，真实行高 160 ≠ 估算 110）', () => {
  // 估算行高固定 110，真实行高通过 stubRowOffsetHeight(160) 给定。
  const ESTIMATE = 110
  const REAL = 160

  beforeEach(() => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    resetROInstances()
  })

  it('设置非零 scrollTop 后追加一页：scrollTop 不得自动增加', async () => {
    const restore = stubRowOffsetHeight(REAL)
    try {
      const events: { firstRowIndex: number; lastRowIndex: number; rowCount: number }[] = []
      const { scroller, getTop, scrollToCalls } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      // 300 项 / 15 = 20 行；真实行高 160 + gap 10 = 170/行，总高 3400。
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid :items="items" :columns="15" :row-height="${ESTIMATE}" :gap="10" size-class="small" @visible-range-change="onRange"><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 300 }, (_, i) => ({ id: i + 1 })) }),
          methods: { onRange(p: any) { events.push(p) } },
        },
        { attachTo: scroller },
      )
      await flushPromises()
      // 用户滚动到 500px 并派发 scroll，让 virtualizer 把该位置的行测出真实高度（写入缓存）。
      ;(scroller as any).scrollTop = 500
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()

      const beforeTop = getTop()
      const rangeBefore = (wrapper.findComponent(VirtualMediaGrid).vm as any).virtualizer?.range
      const keyBefore = rangeBefore ? `${rangeBefore.startIndex}-${rangeBefore.endIndex}` : ''
      // 清空记录，只观察“追加后”是否被补偿写回 / 区间是否被推进。
      scrollToCalls.length = 0
      events.length = 0

      // 追加一页（不模拟任何用户滚动）：父组件 items 增长，等价于分页追加。
      await wrapper.setData({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      await flushPromises()
      await flushPromises()

      const afterTop = getTop()
      // 核心断言：追加数据不得改写用户滚动位置（允许浏览器取整误差 1px）。
      expect(Math.abs(afterTop - beforeTop)).toBeLessThanOrEqual(1)
      // 补偿若发生会走 scrollTo；修复后追加不应触发任何 scrollTo 补偿写回。
      expect(scrollToCalls.length).toBe(0)
      // 真实可见区间不得被推进。
      const rangeAfter = (wrapper.findComponent(VirtualMediaGrid).vm as any).virtualizer?.range
      const keyAfter = rangeAfter ? `${rangeAfter.startIndex}-${rangeAfter.endIndex}` : ''
      expect(keyAfter).toBe(keyBefore)
      // 不得因追加而派发新的可见区间事件。
      expect(events.length).toBe(0)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('追加数据但用户未继续滚动：不得再次派发推进后的可见区间', async () => {
    const restore = stubRowOffsetHeight(REAL)
    try {
      const events: { firstRowIndex: number; lastRowIndex: number; rowCount: number }[] = []
      const { scroller } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid
            :items="items" :columns="15" :row-height="${ESTIMATE}" :gap="10" :size-class="'small'"
            @visible-range-change="onRange"
          ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 300 }, (_, i) => ({ id: i + 1 })) }),
          methods: { onRange(p: any) { events.push(p) } },
        },
        { attachTo: scroller },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      // 用户滚动到中段并稳定。
      ;(scroller as any).scrollTop = 500
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()
      vm.recomputeScrollMargin()
      await flushPromises()
      // 记录稳定后的真实可见区间。
      const rangeBefore = vm.virtualizer?.range
      const keyBefore = rangeBefore ? `${rangeBefore.startIndex}-${rangeBefore.endIndex}` : ''
      events.length = 0

      // 追加一页，用户不再滚动。
      await wrapper.setData({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      await flushPromises()
      await flushPromises()

      const rangeAfter = vm.virtualizer?.range
      const keyAfter = rangeAfter ? `${rangeAfter.startIndex}-${rangeAfter.endIndex}` : ''
      // 真实可见区间不得被推进。
      expect(keyAfter).toBe(keyBefore)
      // 不得因追加而派发“区间被推进”的事件：去重 key 未变，但 rowCount 变化可能触发一次
      // startIndex/endIndex 不变的事件（旧实现的残留）。这里只断言没有“推进后的”事件——
      // 即没有事件 whose (startIndex,endIndex) 与稳定区间不同。
      const advanced = events.filter(e => `${e.firstRowIndex}-${e.lastRowIndex}` !== keyBefore)
      expect(advanced.length).toBe(0)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('一次接近末端事件最多触发一次分页：响应追加后不得自动请求第三页', async () => {
    // 组件级验证：接近末端 → 派发一次事件 → 追加一页 → 追加后真实区间不推进 → 不再派发新事件。
    // 父组件（Detail）侧的“最多一次”由 in-flight 锁与“无新事件不续页”共同保证，这里验证组件侧
    // 不会因追加而自发产生第二次“接近末端”事件。
    //
    // 关键陷阱：数据从 2 行追加到 4 行时，虚拟器会“就地测量”新挂载行（measureElement），
    // 这些行在视口内（首屏），其 estimate→actual 差值使它们进入真实视口区间，
    // virtualizer.range.endIndex 因此从 1 扩到 3——这是“新数据自身进入视口”，而非滚动补偿推进。
    // 这正是首屏“为填满视口而补页”的合法路径。要复现“追加不得推进”，必须在追加前数据已超过视口、
    // 用户已滚动到中段，使追加的行落在视口之外（不在可见区），此时 range 才应保持不变。
    const restore = stubRowOffsetHeight(REAL)
    try {
      const events: { firstRowIndex: number; lastRowIndex: number; rowCount: number }[] = []
      const { scroller } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      // 600 项 / 15 = 40 行，真实行高 160+gap10=170/行，总高 6800，远超视口。
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid
            :items="items" :columns="15" :row-height="${ESTIMATE}" :gap="10" :size-class="'small'"
            @visible-range-change="onRange"
          ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) }),
          methods: { onRange(p: any) { events.push(p) } },
        },
        { attachTo: scroller },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      // 用户滚动到中段并稳定：使可见区间落在数据中部，追加的行落在视口之外。
      ;(scroller as any).scrollTop = 2000
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()
      vm.recomputeScrollMargin()
      await flushPromises()
      const range0 = vm.virtualizer?.range
      const key0 = range0 ? `${range0.startIndex}-${range0.endIndex}` : ''
      events.length = 0

      // 追加一页（900 项 / 15 = 60 行），用户不滚动。新行落在已加载末尾之后，不在当前视口。
      await wrapper.setData({ items: Array.from({ length: 900 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      await flushPromises()
      await flushPromises()
      // 真实区间不得被推进（追加的行不在视口内，不应改变可见区间）。
      const range1 = vm.virtualizer?.range
      const key1 = range1 ? `${range1.startIndex}-${range1.endIndex}` : ''
      expect(key1).toBe(key0)
      // 不得派发“区间被推进”的事件。
      const advanced = events.filter(e => `${e.firstRowIndex}-${e.lastRowIndex}` !== key0)
      expect(advanced.length).toBe(0)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('用户真实滚动到新末端：可见区间推进并派发事件', async () => {
    const restore = stubRowOffsetHeight(REAL)
    try {
      const events: { firstRowIndex: number; lastRowIndex: number; rowCount: number }[] = []
      const { scroller } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid
            :items="items" :columns="15" :row-height="${ESTIMATE}" :gap="10" :size-class="'small'"
            @visible-range-change="onRange"
          ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) }),
          methods: { onRange(p: any) { events.push(p) } },
        },
        { attachTo: scroller },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      await flushPromises()
      const firstBefore = vm.getFirstVisibleIndex()
      events.length = 0

      // 用户真实向下滚动到接近末端。
      ;(scroller as any).scrollTop = 6000
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()

      const firstAfter = vm.getFirstVisibleIndex()
      expect(firstAfter).toBeGreaterThan(firstBefore)
      // 真实滚动应派发可见区间事件。
      expect(events.length).toBeGreaterThan(0)
      const last = events[events.length - 1]!
      expect(last.firstRowIndex).toBeGreaterThan(0)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('人脸密度（5 列）同样不因追加自动滚动', async () => {
    const restore = stubRowOffsetHeight(REAL)
    try {
      const { scroller, getTop, scrollToCalls } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid :items="items" :columns="5" :row-height="${ESTIMATE}" :gap="10" size-class="medium"><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 100 }, (_, i) => ({ id: i + 1 })) }),
        },
        { attachTo: scroller },
      )
      await flushPromises()
      ;(scroller as any).scrollTop = 400
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()
      const beforeTop = getTop()
      scrollToCalls.length = 0

      await wrapper.setData({ items: Array.from({ length: 200 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      await flushPromises()
      await flushPromises()
      const afterTop = getTop()
      expect(Math.abs(afterTop - beforeTop)).toBeLessThanOrEqual(1)
      expect(scrollToCalls.length).toBe(0)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('ResizeObserver 报告纯高度变化（宽度不变）：新代码不触发 measure / 区间推进（行为验证，非回归）', async () => {
    // ⚠️ 重要诚实声明：此用例是“新代码行为验证”，不是“旧 bug 回归测试”。
    //
    // jsdom 不做真实布局，RO 回调里调 measure() 清缓存后，measureElement 重新读 offsetHeight
    // 仍是 160，且 applyScrollAdjustment 在 jsdom 里不产生真实滚动事件链——因此 OLD 代码下
    // RO 无条件 measure 也无法让 range 在“已被 prime 过”后继续推进（prime 已把 range 推到
    // 真实视口），第二条纯高度回调在 OLD/NEW 下表现相同。本用例在 OLD 上也通过。
    //
    // 因此本用例不能证明“RO 修复断开了旧 bug 闭环”——它只验证：新代码在纯高度变化时
    // 不崩、不推进 range、不写 scrollTop。RO 修复的正确性最终依赖 NAS 实机验收（真浏览器
    // 会做真实布局，measure 清缓存→滚动补偿→range 推进的闭环在真机上才存在）。
    //
    // 真正能复现旧 bug 的是主闭环用例（watch(items.length) → measure），见上方 describe。
    const restore = stubRowOffsetHeight(REAL)
    try {
      const { scroller, getTop, scrollToCalls } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid :items="items" :columns="15" :row-height="${ESTIMATE}" :gap="10" size-class="small"><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 600 }, (_, i) => ({ id: i + 1 })) }),
        },
        { attachTo: scroller },
      )
      await flushPromises()
      // 用户滚动到中段并稳定。
      ;(scroller as any).scrollTop = 2000
      scroller.dispatchEvent(new Event('scroll'))
      await flushPromises()
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      await flushPromises()

      const gridEl = wrapper.find('.virtual-media-grid').element
      const gridRO = roFor(gridEl)
      expect(gridRO).toBeTruthy()
      // 先用一次 width=1000 的回调建立宽度基准（模拟浏览器首帧 RO 派发真实宽度），
      // 否则 lastGridWidth 基准为 0，第一次 height 回调会被误判为 width 变化。
      gridRO!.trigger({ target: gridEl, borderBoxSize: [{ inlineSize: 1000, blockSize: 100 }] })
      await flushPromises()
      await flushPromises()
      // 重新读取稳定后的 range（prime 回调可能因 width 0→1000 触发一次 measure，区间已稳定）。
      const rangeBefore = vm.virtualizer?.range
      const keyBefore = rangeBefore ? `${rangeBefore.startIndex}-${rangeBefore.endIndex}` : ''
      const beforeTop = getTop()
      scrollToCalls.length = 0

      // 派发纯高度变化：inlineSize（宽度）保持 1000，blockSize（高度）变大。
      // 新代码：宽度未变 → 跳过 measure → range 保持、scrollTop 不变。
      gridRO!.trigger({
        target: gridEl,
        borderBoxSize: [{ inlineSize: 1000, blockSize: 9999 }],
      })
      await flushPromises()
      await flushPromises()
      await flushPromises()

      const rangeAfter = vm.virtualizer?.range
      const keyAfter = rangeAfter ? `${rangeAfter.startIndex}-${rangeAfter.endIndex}` : ''
      // 行为验证：新代码纯高度变化时区间不推进、scrollTop 不被改写。
      // （注意：OLD 代码此断言也通过——见上方诚实声明，jsdom 无法复现 RO 闭环。）
      expect(keyAfter).toBe(keyBefore)
      expect(Math.abs(getTop() - beforeTop)).toBeLessThanOrEqual(1)
      expect(scrollToCalls.length).toBe(0)
      wrapper.unmount()
    } finally {
      restore()
    }
  })

  it('ResizeObserver 报告宽度变化（结构性）：允许重测，不推进 scrollTop（行为验证）', async () => {
    // 同上：行为验证用例，非旧 bug 回归测试。验证宽度变化时新代码不崩、组件仍可用、
    // 不因重测本身推进 scrollTop。RO 路径的旧 bug 复现依赖 NAS 实机（见上个用例声明）。
    const restore = stubRowOffsetHeight(REAL)
    try {
      const { scroller, getTop } = buildScroller({ scrollerHeight: 800, scrollTop: 0 })
      document.body.appendChild(scroller)
      const wrapper = mount(
        {
          components: { VirtualMediaGrid },
          template: `<VirtualMediaGrid :items="items" :columns="15" :row-height="${ESTIMATE}" :gap="10" size-class="small"><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
          data: () => ({ items: Array.from({ length: 300 }, (_, i) => ({ id: i + 1 })) }),
        },
        { attachTo: scroller },
      )
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      await flushPromises()

      const gridEl = wrapper.find('.virtual-media-grid').element
      const gridRO = roFor(gridEl)
      expect(gridRO).toBeTruthy()
      // 先用 width=1000 建立基准，再发 width=800 才能构成“宽度变化”。
      gridRO!.trigger({ target: gridEl, borderBoxSize: [{ inlineSize: 1000, blockSize: 100 }] })
      await flushPromises()
      await flushPromises()
      const beforeTop = getTop()

      // 宽度从 1000 变到 800（结构性变化）。
      expect(() => {
        gridRO!.trigger({
          target: gridEl,
          borderBoxSize: [{ inlineSize: 800, blockSize: 9999 }],
        })
      }).not.toThrow()
      await flushPromises()
      await flushPromises()
      // 结构性重测后组件仍可用：能取到 range。
      const range = vm.virtualizer?.range
      expect(range).toBeTruthy()
      // scrollTop 不应因重测本身被推进（重测不是滚动补偿）。
      expect(Math.abs(getTop() - beforeTop)).toBeLessThanOrEqual(1)
      wrapper.unmount()
    } finally {
      restore()
    }
  })
})
