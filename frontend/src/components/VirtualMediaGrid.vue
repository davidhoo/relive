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
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
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

// scrollMargin = 网格容器在页面中的 offsetTop。
// window virtualizer 把 window scroll 映射到列表内部坐标，需扣除网格顶部到页面顶部
// 的固定偏移（页面头部卡片高度），否则首行 transform 会多算一段、可见区判断偏移。
const scrollMargin = computed(() => gridRef.value?.offsetTop ?? 0)

// options 必须用 computed 包裹：useWindowVirtualizer 内部 watch(() => unref(options))
// 依赖该 ref 在 count/columns/scrollMargin 变化时重新 setOptions，翻页与密度切换才生效。
const virtualizer = useWindowVirtualizer(
  computed(() => ({
    count: rowCount.value,
    estimateSize: () => props.rowHeight + props.gap,
    overscan: props.overscan,
    gap: props.gap,
    scrollMargin: scrollMargin.value,
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

// 行 transform 必须扣除 scrollMargin：virtualizer 给出的 start 已是相对列表顶部的坐标，
// 而绝对定位容器起点就是列表顶部，故直接 translateY(start)。无需再叠加 scrollMargin
// （scrollMargin 已在 virtualizer options 中用于把 window scroll 对齐到列表坐标）。
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

// 列数变化后真实卡片尺寸可能变化，需重新测量已挂载行。
watch(
  () => props.columns,
  () => nextTick(() => virtualizer.value?.measure()),
)
// items 首次到达后触发一次测量，避免估算高度与真实卡片不符导致行覆盖。
watch(
  () => props.items.length,
  (n, old) => {
    if (n > 0 && (old === 0 || old === undefined)) {
      nextTick(() => virtualizer.value?.measure())
    }
  },
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
  virtualizer.value?.measure()
}

// scrollMargin 依赖 offsetTop，窗口或布局变化后重新测量。
let resizeRaf = 0
const onViewportResize = () => {
  if (resizeRaf) cancelAnimationFrame(resizeRaf)
  resizeRaf = requestAnimationFrame(() => {
    virtualizer.value?.measure()
  })
}
if (typeof window !== 'undefined') {
  window.addEventListener('resize', onViewportResize)
}

onBeforeUnmount(() => {
  if (resizeRaf) cancelAnimationFrame(resizeRaf)
  if (typeof window !== 'undefined') {
    window.removeEventListener('resize', onViewportResize)
  }
})

defineExpose({
  getFirstVisibleIndex,
  scrollToIndex,
  measure,
  gridRef,
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
