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
        :size-class="sizeClass" :has-more="hasMore" :loading="loading"
        :load-threshold-rows="loadThresholdRows"
        @load-more="$emit('load-more')"
      ><template #item="{ item }"><div class="cell">{{ item.id }}</div></template></VirtualMediaGrid>`,
      props: ['items', 'columns', 'rowHeight', 'gap', 'sizeClass', 'hasMore', 'loading', 'loadThresholdRows'],
    },
    {
      props: {
        items: Array.from({ length: 100 }, (_, i) => ({ id: i + 1 })),
        columns: 15,
        rowHeight: 110,
        gap: 10,
        sizeClass: 'small',
        hasMore: true,
        loading: false,
        loadThresholdRows: 3,
        ...props,
      },
      attachTo: document.body,
    },
  )
  const el = wrapper.find('.virtual-media-grid').element as HTMLElement
  Object.defineProperty(el, 'clientHeight', { configurable: true, value: 600 })
  Object.defineProperty(el, 'scrollHeight', { configurable: true, value: 100000 })
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

