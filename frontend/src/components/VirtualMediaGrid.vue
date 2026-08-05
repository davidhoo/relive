<template>
  <div ref="gridRef" class="virtual-media-grid">
    <!--
      element virtualizer 模式：网格容器不再自带滚动条，占据正常文档流位置；
      滚动由最近的 .main-content（应用真正的滚动容器）驱动，不再用 window。
      内层相对定位容器撑开总高度，虚拟行绝对定位在其内。
    -->
    <div
      class="virtual-media-grid-inner"
      :style="{ height: `${totalHeight}px`, position: 'relative' }"
    >
      <div
        v-for="row in virtualRows"
        :key="row.key"
        class="virtual-media-grid-row"
        :class="`is-${sizeClass}`"
        :data-index="row.index"
        :ref="measureRow"
        :style="rowStyle(row)"
      >
        <template v-for="item in row.items" :key="itemKey(item)">
          <slot name="item" :item="item" :index="itemIndex(item)" />
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'

// VirtualMediaGrid：基于 .main-content 元素滚动的行级虚拟网格。
// 照片与人脸共用同一套滚动/分页/密度计算逻辑。
// 已加载数据组织为虚拟行，仅挂载可见行 + 前后各 overscan 行。
//
// 滚动源：应用真正的滚动容器是 MainLayout 的 .main-content（height:100vh; overflow:hidden
// 在外层，.main-content overflow-y:auto）。useWindowVirtualizer 监听 window 滚动，但用户
// 滚 .main-content 时 window 不动 → virtualizer 收不到偏移 → visible-range-change 不推进 →
// 连续分页中断。改为 useVirtualizer，把 .main-content 作为 scrollElement。
//
// 与旧版（window 模式）差异：
//   - 滚动源改为 gridRef.closest('.main-content')（不使用全局 querySelector，避免多窗口/嵌套取错）。
//   - scrollMargin 改为相对滚动容器坐标系：
//       gridRect.top - scrollContainerRect.top + scrollContainer.scrollTop
//     （window 模式下是 gridRect.top + window.scrollY）。
//   - 行高优先采用真实 DOM 测量（measureElement），rowHeight 仅作首屏估算。
//   - 组件不再自行决定是否加载，统一输出 visible-range-change，由父组件结合
//     active/loading/error/hasMore 决定。删除 watch(items.length)->maybeLoadMore。
//   - 行位置公式仍是 translateY(row.start - scrollMargin)，scrollMargin 来自 .main-content 坐标系。

export interface VirtualMediaGridProps {
  // 已加载的全部数据项
  items: any[]
  // 当前列数（密度：15 / 5 / 3）
  columns: number
  // 每一行的估算高度（px），仅用于首屏估算，真实高度由 measureElement 测量
  rowHeight: number
  // 行间 gap（px）
  gap?: number
  // 可见区域前后额外渲染的行数
  overscan?: number
  // 当前视口/密度样式标识（用于 CSS grid-template-columns 选择），如 'small' | 'medium' | 'large'
  sizeClass: string
}

const props = withDefaults(defineProps<VirtualMediaGridProps>(), {
  gap: 10,
  overscan: 5,
})

const emit = defineEmits<{
  (e: 'visible-range-change', payload: {
    firstRowIndex: number
    lastRowIndex: number
    rowCount: number
  }): void
}>()

const gridRef = ref<HTMLElement | null>(null)

// 实际滚动容器：最近的 .main-content。元素模式下由 virtualizer 监听其 scrollTop。
// 用 ref 包裹，元素变化时 computed options 重算，virtualizer 重新 setOptions + _willUpdate。
const scrollElRef = ref<HTMLElement | null>(null)

const rowCount = computed(() =>
  props.items.length === 0 ? 0 : Math.ceil(props.items.length / props.columns),
)

// 取最近 .main-content：从 gridRef 向上找，不用全局 querySelector。
// 测试 / SSR / 嵌套布局下若不存在则返回 null，virtualizer 进入无滚动元素态，安全降级。
const resolveScrollElement = (): HTMLElement | null => {
  if (!gridRef.value) return null
  // closest 在 Element 原型上，gridRef.value 是 HTMLElement 一定有。
  const el = gridRef.value.closest('.main-content') as HTMLElement | null
  return el || null
}

// scrollMargin = 网格容器在滚动容器内容坐标系中的顶部偏移。
// 元素模式下 virtualizer 监听 scrollElement.scrollTop，row.start 基于 scrollMargin 起算；
// scrollMargin 必须表达“网格顶部相对滚动容器内容顶部的距离”：
//   gridRect.top - scrollContainerRect.top + scrollContainer.scrollTop
// （gridRect.top - scrollContainerRect.top 是网格在滚动容器视口内的静态偏移，
//  加上 scrollTop 还原到内容坐标）。保存在响应式 ref，布局/滚动变化时重算。
const scrollMarginPx = ref(0)

const recomputeScrollMargin = () => {
  const el = gridRef.value
  const scroller = scrollElRef.value
  if (!el || !scroller) {
    scrollMarginPx.value = 0
    return
  }
  const gridRect = el.getBoundingClientRect()
  const scrollRect = scroller.getBoundingClientRect()
  const abs = gridRect.top - scrollRect.top + scroller.scrollTop
  scrollMarginPx.value = abs > 0 ? abs : 0
}

// options 必须用 computed 包裹：useVirtualizer 内部 watch(() => unref(options))
// 依赖该 ref 在 count/columns/scrollMargin/scrollElement 变化时重新 setOptions，翻页与密度切换才生效。
// getScrollElement 返回 scrollElRef.value；vue-virtual 内部 watch getScrollElement() 返回值，
// 元素引用变化时触发 _willUpdate，确保切换容器后重新挂监听。
// estimateSize 只返回行本身估算高度；行间距统一交给 virtualizer 的 gap，避免重复计算。
const virtualizer = useVirtualizer(
  computed(() => ({
    count: rowCount.value,
    getScrollElement: () => scrollElRef.value,
    estimateSize: () => props.rowHeight,
    overscan: props.overscan,
    gap: props.gap,
    scrollMargin: scrollMarginPx.value,
    // 真实行高优先：用默认 measureElement，行元素带 data-index 即可被测量。
    indexAttribute: 'data-index',
  })),
)

const virtualRows = computed(() => {
  const rows = virtualizer.value.getVirtualItems()
  return rows.map(vr => {
    const startIndex = vr.index * props.columns
    const items = props.items.slice(startIndex, startIndex + props.columns)
    return {
      key: `row-${vr.index}`,
      index: vr.index,
      start: vr.start,
      items,
    }
  })
})
const totalHeight = computed(() => virtualizer.value.getTotalSize())

// 行 transform：useVirtualizer 给出的 row.start 已是“相对列表顶部”的坐标，
// 但它内部为把滚动容器 scrollTop 对齐到列表坐标，已把 scrollMargin 计入 start（即 start 包含
// 网格顶部到滚动容器内容顶部的固定偏移）。而本组件内层绝对定位容器起点就在列表顶部（top:0），
// 若直接 translateY(start)，第一行会被多下移一个 scrollMargin，表现为列表顶部出现一块空白。
// 因此必须减去 scrollMarginPx，使第一行最终落在列表起点（translate 接近 0）。
const rowStyle = (row: { start: number }) => ({
  position: 'absolute' as const,
  top: 0,
  left: 0,
  width: '100%',
  transform: `translateY(${row.start - scrollMarginPx.value}px)`,
  display: 'grid',
  gap: `${props.gap}px`,
})

// measureElement：把行 DOM 交给 virtualizer 测量真实高度。
// 每个 v-for 行通过 :ref 回调注册；virtualizer 按 data-index 归位。
const measureRow = (el: Element | any) => {
  if (el) {
    virtualizer.value.measureElement(el as HTMLElement)
  }
}

// 用于 slot 内部需要原始 index 的场景（如选择集合恢复）
const itemIndex = (item: any) => props.items.indexOf(item)

const itemKey = (item: any) => (item && item.id != null ? item.id : itemIndex(item))

// 可见区间变化 → 通知父组件。父组件据此 + active/loading/error/hasMore 决定是否加载。
// 这是唯一的加载触发源，删除旧版 watch(items.length) 隐式翻页。
//
// 严格分离两类范围：
//   - getVirtualItems()：含 overscan 预渲染行，仅供 virtualRows 渲染 DOM；
//   - virtualizer.range：真实视口范围（startIndex/endIndex，不含 overscan），用于事件派发与分页触发。
//
// 旧实现用 getVirtualItems() 末行作为 lastVisibleRowIndex，又把 rowCount 计入去重 key：
//   请求完成 → items/rowCount 增长 → watch 重新派发同一范围（因 rowCount 变了）→
//   overscan 末行被误判为真实可见末行 → Detail 认为“仍接近底部” → 再次请求下一页。
// 改用 virtualizer.range 的真实 startIndex/endIndex，去重 key 仅含 startIndex-endIndex，
// 数据追加导致 rowCount 变化但真实视口未动时不再派发，闭环断开。
let lastEmitted = ''
watch(
  () => {
    const range = virtualizer.value.range
    if (!range) return ''
    return `${range.startIndex}-${range.endIndex}`
  },
  key => {
    if (key === lastEmitted) return
    lastEmitted = key
    const range = virtualizer.value.range
    if (!range) {
      emit('visible-range-change', { firstRowIndex: 0, lastRowIndex: 0, rowCount: rowCount.value })
      return
    }
    emit('visible-range-change', {
      firstRowIndex: range.startIndex,
      lastRowIndex: range.endIndex,
      rowCount: rowCount.value,
    })
  },
)

// 每次数据追加后重新测量：新挂载行的真实高度可能与估算不符，需让 virtualizer 重测，
// 否则后续行偏移会基于旧估算值导致行覆盖或可见区判断错误。不重置滚动位置——measure
// 只重算尺寸/偏移，不调用 scrollToIndex。
watch(
  () => props.items.length,
  (n, old) => {
    if (n > 0) {
      nextTick(() => virtualizer.value?.measure())
    }
    void old
  },
)

// 列数变化后真实卡片尺寸可能变化，需重新测量已挂载行。
watch(
  () => props.columns,
  () => nextTick(() => virtualizer.value?.measure()),
)

// 密度切换 / tab pane 由隐藏变可见时 virtualizer 需重新测量。
// 由父组件调用 getFirstVisibleIndex / scrollToIndex / measure。
const getFirstVisibleIndex = (): number => {
  const items = virtualizer.value.getVirtualItems()
  if (items.length === 0) return 0
  return (items[0]?.index ?? 0) * props.columns
}

const scrollToIndex = (index: number) => {
  const rowIndex = Math.floor(index / props.columns)
  virtualizer.value.scrollToIndex(rowIndex, { align: 'start' })
}

// 滚动容器读取/恢复：暴露给父组件，Tab 切换时保存与恢复 .main-content.scrollTop。
// 没有取得滚动容器时安全返回，不抛异常。
const getScrollOffset = (): number => {
  return scrollElRef.value?.scrollTop ?? 0
}

const scrollToOffset = (offset: number) => {
  const scroller = scrollElRef.value
  if (!scroller) return
  scroller.scrollTop = offset
}

const measure = () => {
  // 重新测量前先重算 scrollMargin（隐藏 Tab 变可见、人物信息区高度变化都会改变相对偏移）。
  recomputeScrollMargin()
  virtualizer.value?.measure()
}

// 显式监听滚动容器解析：scrollElRef 从 null→元素（或换元素）时，virtualizer 内部虽会
// watch getScrollElement() 触发 _willUpdate 重新挂监听，但 scrollMargin/可见区间的重算
// 靠这里显式驱动，避免依赖 scrollMarginPx 变化的间接联动。容器变化必伴随布局变化，
// 重算 scrollMargin + measure 后 visible-range-change 才会按新坐标系推进。
watch(scrollElRef, () => {
  recomputeScrollMargin()
  virtualizer.value?.measure()
})

// scrollMargin 依赖相对滚动容器的位置，窗口/布局/滚动容器变化后重新测量并重算偏移。
let resizeRaf = 0
const onViewportResize = () => {
  if (resizeRaf) cancelAnimationFrame(resizeRaf)
  resizeRaf = requestAnimationFrame(() => {
    recomputeScrollMargin()
    virtualizer.value?.measure()
  })
}

// ResizeObserver 监听网格容器自身布局变化（人物信息区高度变化导致网格下移、
// Tab pane 展开等），重算 scrollMargin 并重新测量。容器不存在时（SSR）跳过。
let resizeObserver: ResizeObserver | null = null
const setupResizeObserver = () => {
  if (typeof ResizeObserver === 'undefined' || !gridRef.value) return
  resizeObserver = new ResizeObserver(() => {
    recomputeScrollMargin()
    virtualizer.value?.measure()
  })
  resizeObserver.observe(gridRef.value)
}

onMounted(() => {
  // 挂载后从 gridRef.closest('.main-content') 取滚动容器，赋值给 scrollElRef 触发 options 重算。
  scrollElRef.value = resolveScrollElement()
  recomputeScrollMargin()
  setupResizeObserver()
  // 挂载后再次确认偏移与容器：挂载瞬间布局可能尚未稳定。
  nextTick(() => {
    if (!scrollElRef.value) {
      scrollElRef.value = resolveScrollElement()
    }
    recomputeScrollMargin()
    virtualizer.value?.measure()
  })
  if (typeof window !== 'undefined') {
    window.addEventListener('resize', onViewportResize)
  }
})

onBeforeUnmount(() => {
  if (resizeRaf) cancelAnimationFrame(resizeRaf)
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
  if (typeof window !== 'undefined') {
    window.removeEventListener('resize', onViewportResize)
  }
})

defineExpose({
  getFirstVisibleIndex,
  scrollToIndex,
  measure,
  gridRef,
  getScrollOffset,
  scrollToOffset,
  // 暴露重算供父组件在 Tab 切换、密度切换等显式场景调用。
  recomputeScrollMargin,
})
</script>

<style scoped>
.virtual-media-grid {
  width: 100%;
  position: relative;
}

.virtual-media-grid-inner {
  width: 100%;
}

.virtual-media-grid-row.is-small {
  grid-template-columns: repeat(15, minmax(0, 1fr));
}

.virtual-media-grid-row.is-medium {
  grid-template-columns: repeat(5, minmax(0, 1fr));
}

.virtual-media-grid-row.is-large {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

@media (max-width: 1200px) {
  .virtual-media-grid-row.is-small {
    grid-template-columns: repeat(10, minmax(0, 1fr));
  }
  .virtual-media-grid-row.is-medium {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .virtual-media-grid-row.is-small {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }
  .virtual-media-grid-row.is-medium {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .virtual-media-grid-row.is-large {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 480px) {
  .virtual-media-grid-row.is-small {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
  .virtual-media-grid-row.is-medium,
  .virtual-media-grid-row.is-large {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
