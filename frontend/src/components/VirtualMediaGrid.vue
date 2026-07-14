<template>
  <div ref="gridRef" class="virtual-media-grid">
    <!--
      window virtualizer 模式：网格容器不再自带滚动条，占据正常文档流位置；
      滚动由浏览器 window 驱动，整个页面自然向下滚动（不引入列表内部滚动条）。
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
import { useWindowVirtualizer } from '@tanstack/vue-virtual'

// VirtualMediaGrid：基于 window 的行级虚拟网格。
// 照片与人脸共用同一套滚动/分页/密度计算逻辑。
// 已加载数据组织为虚拟行，仅挂载可见行 + 前后各 overscan 行。
//
// 与旧版差异：
//   - 滚动源改为 window（useWindowVirtualizer），不再有列表内部 overflow-y:auto。
//   - 行高优先采用真实 DOM 测量（measureElement），rowHeight 仅作首屏估算。
//   - 组件不再自行决定是否加载，统一输出 visible-range-change，由父组件结合
//     active/loading/error/hasMore 决定。删除 watch(items.length)->maybeLoadMore。
//   - scrollMargin 用 getBoundingClientRect().top + window.scrollY 计算页面绝对偏移，
//     不再用 offsetTop（offsetTop 相对 offsetParent，在 Element Plus 卡片/Tab 嵌套下不可靠）。

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

const rowCount = computed(() =>
  props.items.length === 0 ? 0 : Math.ceil(props.items.length / props.columns),
)

// scrollMargin = 网格容器在页面中的绝对顶部偏移。
// window virtualizer 把 window scroll 映射到列表内部坐标，需扣除网格顶部到页面顶部的
// 固定偏移（页面头部卡片高度）。offsetTop 相对 offsetParent，在 Element Plus 卡片/Tab 等
// 嵌套结构下不等于页面绝对位置，故改用 getBoundingClientRect().top + window.scrollY。
// 保存在响应式 ref 中，布局变化时重算，触发 virtualizer 重新 setOptions。
const scrollMarginPx = ref(0)

const recomputeScrollMargin = () => {
  const el = gridRef.value
  if (!el || typeof window === 'undefined') {
    scrollMarginPx.value = 0
    return
  }
  const rect = el.getBoundingClientRect()
  // 页面绝对偏移 = 视口内顶部位置 + 当前滚动量。负值时夹到 0。
  const abs = rect.top + window.scrollY
  scrollMarginPx.value = abs > 0 ? abs : 0
}

// options 必须用 computed 包裹：useWindowVirtualizer 内部 watch(() => unref(options))
// 依赖该 ref 在 count/columns/scrollMargin 变化时重新 setOptions，翻页与密度切换才生效。
// estimateSize 只返回行本身估算高度；行间距统一交给 virtualizer 的 gap，避免重复计算。
const virtualizer = useWindowVirtualizer(
  computed(() => ({
    count: rowCount.value,
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

// 行 transform：virtualizer 给出的 start 已是相对列表顶部的坐标，绝对定位容器起点即列表顶部，
// 故直接 translateY(start)。scrollMargin 已在 options 中用于把 window scroll 对齐到列表坐标。
const rowStyle = (row: { start: number }) => ({
  position: 'absolute' as const,
  top: 0,
  left: 0,
  width: '100%',
  transform: `translateY(${row.start}px)`,
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
let lastEmitted = ''
watch(
  () => {
    const items = virtualizer.value.getVirtualItems()
    if (items.length === 0) return ''
    return `${items[0]!.index}-${items[items.length - 1]!.index}-${rowCount.value}`
  },
  key => {
    if (key === lastEmitted) return
    lastEmitted = key
    const items = virtualizer.value.getVirtualItems()
    if (items.length === 0) {
      emit('visible-range-change', { firstRowIndex: 0, lastRowIndex: 0, rowCount: rowCount.value })
      return
    }
    emit('visible-range-change', {
      firstRowIndex: items[0]!.index,
      lastRowIndex: items[items.length - 1]!.index,
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

const measure = () => {
  // 重新测量前先重算 scrollMargin（隐藏 Tab 变可见、人物信息区高度变化都会改变绝对偏移）。
  recomputeScrollMargin()
  virtualizer.value?.measure()
}

// scrollMargin 依赖页面绝对位置，窗口/布局变化后重新测量并重算偏移。
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
  recomputeScrollMargin()
  setupResizeObserver()
  // 挂载后再次确认偏移：挂载瞬间布局可能尚未稳定。
  nextTick(() => {
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
