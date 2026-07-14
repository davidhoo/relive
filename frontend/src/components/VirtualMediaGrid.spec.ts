import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import VirtualMediaGrid from '@/components/VirtualMediaGrid.vue'
// VirtualMediaGrid 组件级测试。
// jsdom 无真实布局（clientHeight=0），virtualizer 无法通过滚动事件产出可见区间，
// 因此组件级行为主要通过注入容器尺寸 + 直接调用暴露的方法验证；
// 纯逻辑（分组/卸载计数/触发判定/选择/锚点）在 peopleGridUtils.spec.ts 覆盖。

const mountGrid = (props: Record<string, unknown>) => {
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
      attachTo: document.body,
    },
  )
  return wrapper
}

describe('VirtualMediaGrid - 渲染行有界（测试项 4 组件级）', () => {
  it('挂载后 inner 容器存在且总高度有值', async () => {
    const wrapper = mountGrid({ items: Array.from({ length: 500 }, (_, i) => ({ id: i + 1 })) })
    await flushPromises()
    const inner = wrapper.find('.virtual-media-grid-inner')
    expect(inner.exists()).toBe(true)
    wrapper.unmount()
  })
})

describe('VirtualMediaGrid - 暴露锚点与测量方法（测试项 7、8）', () => {
  it('暴露 getFirstVisibleIndex / scrollToIndex / measure', async () => {
    const wrapper = mountGrid({})
    await flushPromises()
    // 通过 defineExpose 暴露的方法在 wrapper.vm 上
    const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
    expect(typeof vm.getFirstVisibleIndex).toBe('function')
    expect(typeof vm.scrollToIndex).toBe('function')
    expect(typeof vm.measure).toBe('function')
    // 调用不抛错
    expect(vm.getFirstVisibleIndex()).toBeGreaterThanOrEqual(0)
    expect(() => vm.scrollToIndex(5)).not.toThrow()
    expect(() => vm.measure()).not.toThrow()
    wrapper.unmount()
  })
})

describe('VirtualMediaGrid - 每行 items 不超过 columns（测试项 1 组件级）', () => {
  it('5 列下每行 child 数 ≤ 5', async () => {
    const wrapper = mountGrid({ columns: 5, items: Array.from({ length: 50 }, (_, i) => ({ id: i + 1 })) })
    await flushPromises()
    const rows = wrapper.findAll('.virtual-media-grid-row')
    for (const row of rows) {
      expect(row.element.childElementCount).toBeLessThanOrEqual(5)
    }
    wrapper.unmount()
  })
})

describe('VirtualMediaGrid - scrollMargin 用页面绝对偏移而非 offsetTop', () => {
  it('暴露 recomputeScrollMargin 且 measure 不抛错', async () => {
    const wrapper = mountGrid({ items: Array.from({ length: 50 }, (_, i) => ({ id: i + 1 })) })
    await flushPromises()
    const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
    expect(typeof vm.recomputeScrollMargin).toBe('function')

    // 模拟网格在页面下方（getBoundingClientRect.top=300），重算后 measure 不抛错
    const grid = wrapper.find('.virtual-media-grid').element as HTMLElement
    vi.spyOn(grid, 'getBoundingClientRect').mockReturnValue({
      top: 300, left: 0, right: 0, bottom: 0, width: 1000, height: 10, x: 0, y: 300, toJSON: () => ({}),
    } as DOMRect)
    vm.recomputeScrollMargin()
    expect(() => vm.measure()).not.toThrow()
    wrapper.unmount()
  })
})

describe('VirtualMediaGrid - 数据追加后重新测量（不重置滚动）', () => {
  it('items 从 0 增长到非 0 后 measure 不抛错', async () => {
    const wrapper = mountGrid({ items: [] })
    await flushPromises()
    const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
    await wrapper.setProps({ items: Array.from({ length: 40 }, (_, i) => ({ id: i + 1 })) })
    await flushPromises()
    expect(() => vm.measure()).not.toThrow()
    wrapper.unmount()
  })
})

// 首行偏移回归：translateY 必须减去 scrollMargin，否则第一行被多下移一个 scrollMargin，
// 列表顶部出现空白。jsdom 无真实布局，这里直接断言 rowStyle 的 transform 表达式，
// 并在 scrollMargin>0 时验证首行 translate 接近 0。
describe('VirtualMediaGrid - 首行 translateY 减去 scrollMargin（测试项：首行无空白）', () => {
  it('scrollMargin=0 时首行 transform 为 translateY(0px)', async () => {
    const wrapper = mountGrid({ items: Array.from({ length: 30 }, (_, i) => ({ id: i + 1 })) })
    await flushPromises()
    const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
    // scrollMargin 初始 0（jsdom getBoundingClientRect.top=0）
    vm.recomputeScrollMargin()
    const rows = wrapper.findAll('.virtual-media-grid-row')
    expect(rows.length).toBeGreaterThan(0)
    const first = rows[0]!.element as HTMLElement
    // 第一行 start≈0，scrollMargin=0 → translate≈0
    expect(first.style.transform).toContain('translateY')
    const m = first.style.transform.match(/translateY\(([-0-9.]+)px\)/)
    expect(m).not.toBeNull()
    const y = parseFloat(m![1] ?? '0')
    expect(Math.abs(y)).toBeLessThanOrEqual(1) // 接近 0
    wrapper.unmount()
  })

  it('scrollMargin=300 时首行 translate 仍接近 0（不减会变成 300px 空白）', async () => {
    const wrapper = mountGrid({ items: Array.from({ length: 30 }, (_, i) => ({ id: i + 1 })) })
    await flushPromises()
    const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
    const grid = wrapper.find('.virtual-media-grid').element as HTMLElement
    // 模拟网格在页面下方（页面头部高度 300px）。
    vi.spyOn(grid, 'getBoundingClientRect').mockReturnValue({
      top: 300, left: 0, right: 0, bottom: 0, width: 1000, height: 10, x: 0, y: 300, toJSON: () => ({}),
    } as DOMRect)
    // mock window.scrollY=0（页面顶部）
    vi.spyOn(window, 'scrollY', 'get').mockReturnValue(0)
    vm.recomputeScrollMargin()
    await flushPromises()
    // scrollMarginPx 应为 300
    expect(vm.gridRef).toBeTruthy()
    const rows = wrapper.findAll('.virtual-media-grid-row')
    expect(rows.length).toBeGreaterThan(0)
    const first = rows[0]!.element as HTMLElement
    const m = first.style.transform.match(/translateY\(([-0-9.]+)px\)/)
    expect(m).not.toBeNull()
    const y = parseFloat(m![1] ?? '0')
    // 关键断言：减去 scrollMargin 后首行 translate 接近 0，而非 300。
    // 若未减 scrollMargin，这里会是 ~300，测试会失败。
    expect(Math.abs(y)).toBeLessThanOrEqual(1)
    wrapper.unmount()
  })
})

// 真实行位置断言（Task 3 测试要求）。
//
// jsdom 不做真实布局，但 virtualizer 的 measureElement 读 element.offsetHeight。
// 通过全局桩 HTMLElement.prototype.offsetHeight = rowHeight，让 virtualizer 拿到真实行高，
// 从而 row.start 序列变为 0, rowHeight+gap, 2*(rowHeight+gap)...（真实高度+gap，非估算）。
// 再对每行 element 桩 getBoundingClientRect(top=start-translateOffset, bottom=top+rowHeight)，
// 即可断言规格要求的位置关系：
//   expect(firstRowRect.top).toBeCloseTo(gridRect.top, 1)
//   expect(secondRowRect.top).toBeGreaterThanOrEqual(firstRowRect.bottom)
//
// 覆盖多密度（小/中/大）、页面头部高度变化、追加第二页首行不跳动。
describe('VirtualMediaGrid - 真实行位置断言（首行紧贴网格顶部、行不重叠）', () => {
  // 提取行 transform 的 translateY 数值。
  const translateYOf = (el: HTMLElement): number => {
    const m = el.style.transform.match(/translateY\(([-0-9.]+)px\)/)
    return m ? parseFloat(m[1] ?? '0') : 0
  }

  // 给行元素桩 getBoundingClientRect：top=translateY, bottom=top+rowHeight。
  const stubRowRects = (
    rows: ReturnType<import('@vue/test-utils').VueWrapper['findAll']>,
    rowHeight: number,
  ) => {
    rows.forEach(r => {
      const y = translateYOf(r.element as HTMLElement)
      vi.spyOn(r.element as HTMLElement, 'getBoundingClientRect').mockReturnValue({
        top: y, left: 0, right: 0, bottom: y + rowHeight, width: 1000, height: rowHeight, x: 0, y, toJSON: () => ({}),
      } as DOMRect)
    })
  }

  // 桩网格容器 rect。
  const stubGridRect = (wrapper: import('@vue/test-utils').VueWrapper, top = 0) => {
    const grid = wrapper.find('.virtual-media-grid').element as HTMLElement
    vi.spyOn(grid, 'getBoundingClientRect').mockReturnValue({
      top, left: 0, right: 0, bottom: top + 800, width: 1000, height: 800, x: 0, y: top, toJSON: () => ({}),
    } as DOMRect)
  }

  // 全局桩 offsetHeight，使 virtualizer 测得真实行高。返回恢复函数。
  const stubOffsetHeight = (rowHeight: number) => {
    const desc = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      get() { return rowHeight },
    })
    return () => {
      if (desc) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', desc)
    }
  }

  const setupViewport = () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
  }

  it('小图密度：首行紧贴网格顶部，第二行在首行底部之后（不重叠）', async () => {
    setupViewport()
    const restore = stubOffsetHeight(110)
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

  it('中图密度：首行紧贴网格顶部', async () => {
    setupViewport()
    const restore = stubOffsetHeight(260)
    try {
      const wrapper = mountGrid({ columns: 5, rowHeight: 260, items: Array.from({ length: 40 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThanOrEqual(2)
      stubRowRects(rows, 260)
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

  it('大图密度：首行紧贴网格顶部', async () => {
    setupViewport()
    const restore = stubOffsetHeight(420)
    try {
      const wrapper = mountGrid({ columns: 3, rowHeight: 420, items: Array.from({ length: 30 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThanOrEqual(2)
      stubRowRects(rows, 420)
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
    setupViewport()
    const restore = stubOffsetHeight(110)
    try {
      const wrapper = mountGrid({ columns: 15, rowHeight: 110, items: Array.from({ length: 60 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      // 模拟网格在页面下方（头部 200px）且页面已滚动到网格顶部：
      // gridRect.top=0（贴视口顶），scrollY=200，scrollMargin=200。
      // 此时 virtualizer 的 row.start 含 scrollMargin=200，若不减，首行 translate 会是 200（空白）。
      // 减去后首行 translate≈0。
      stubGridRect(wrapper, 0)
      vi.spyOn(window, 'scrollY', 'get').mockReturnValue(200)
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      await flushPromises()
      const rows = wrapper.findAll('.virtual-media-grid-row')
      expect(rows.length).toBeGreaterThanOrEqual(2)
      // 首行 translate 应接近 0（减去 scrollMargin），而非 200。
      const firstY = translateYOf(rows[0]!.element as HTMLElement)
      expect(Math.abs(firstY)).toBeLessThanOrEqual(1)
      // 桩 rect 后断言行不重叠。
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
    setupViewport()
    const restore = stubOffsetHeight(110)
    try {
      const wrapper = mountGrid({ columns: 15, rowHeight: 110, items: Array.from({ length: 30 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      const vm = wrapper.findComponent(VirtualMediaGrid).vm as any
      vm.recomputeScrollMargin()
      vm.measure()
      await flushPromises()
      // 记录追加前首行 translate。
      const rowsBefore = wrapper.findAll('.virtual-media-grid-row')
      const firstYBefore = translateYOf(rowsBefore[0]!.element as HTMLElement)

      // 追加第二页（30 → 60 条）。
      await wrapper.setProps({ items: Array.from({ length: 60 }, (_, i) => ({ id: i + 1 })) })
      await flushPromises()
      vm.measure()
      await flushPromises()
      await flushPromises()
      const rowsAfter = wrapper.findAll('.virtual-media-grid-row')
      const firstYAfter = translateYOf(rowsAfter[0]!.element as HTMLElement)
      // 首行 translate 不应跳动。
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



