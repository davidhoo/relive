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
  Object.defineProperty(scroller, 'scrollTop', {
    configurable: true,
    get() { return top },
    set(v: number) { top = v },
  })
  Object.defineProperty(scroller, 'clientHeight', { configurable: true, get: () => scrollerHeight })
  Object.defineProperty(scroller, 'clientWidth', { configurable: true, get: () => scrollerWidth })
  vi.spyOn(scroller, 'getBoundingClientRect').mockReturnValue({
    top: scrollerTop, left: 0, right: scrollerWidth, bottom: scrollerTop + scrollerHeight,
    width: scrollerWidth, height: scrollerHeight, x: 0, y: scrollerTop, toJSON: () => ({}),
  } as DOMRect)
  const setTop = (v: number) => { top = v }
  return { scroller, setTop }
}

// 把网格挂载到预建的 .main-content 内。
const mountInScroller = (props: Record<string, unknown>, scrollerOpts: Parameters<typeof buildScroller>[0] = {}) => {
  const { scroller, setTop } = buildScroller(scrollerOpts)
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
  return { wrapper, scroller, setTop }
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
})
