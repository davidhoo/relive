<template>
  <div ref="scrollRef" class="virtual-media-grid" :style="containerStyle" @scroll.passive="onScroll">
    <div
      class="virtual-media-grid-inner"
      :style="{ height: `${totalHeight}px`, position: 'relative' }"
    >
      <div
        v-for="row in virtualRows"
        :key="row.key"
        class="virtual-media-grid-row"
        :class="`is-${sizeClass}`"
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
import { computed, nextTick, ref, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'

// VirtualMediaGrid：行级虚拟网格。
// 照片与人脸共用同一套滚动/分页/密度计算逻辑。
// 已加载数据组织为虚拟行，仅挂载可见行 + 前后各 overscan 行。

export interface VirtualMediaGridProps {
  // 已加载的全部数据项
  items: any[]
  // 当前列数（密度：15 / 5 / 3）
  columns: number
  // 每一行的高度（px）
  rowHeight: number
  // 行间 gap（px），仅用于行高补偿
  gap?: number
  // 可见区域前后额外渲染的行数
  overscan?: number
  // 当前视口/密度样式标识（用于 CSS grid-template-columns 选择），如 'small' | 'medium' | 'large'
  sizeClass: string
  // 最大高度（CSS 值），缺省时容器自适应外部布局
  maxHeight?: string
  // 触发下一页加载的阈值：当虚拟网格最后一个可见行距离已加载数据末端不足 loadThresholdRows 行时触发
  loadThresholdRows?: number
  // 是否还有更多数据可加载
  hasMore: boolean
  // 是否正在加载
  loading: boolean
}

const props = withDefaults(defineProps<VirtualMediaGridProps>(), {
  gap: 10,
  overscan: 5,
  maxHeight: undefined,
  loadThresholdRows: 3,
})

const emit = defineEmits<{
  (e: 'load-more'): void
}>()

const scrollRef = ref<HTMLElement | null>(null)

const rowCount = computed(() =>
  props.items.length === 0 ? 0 : Math.ceil(props.items.length / props.columns),
)

// options 必须用 computed 包裹，否则 rowCount.value / props.* 在对象创建时被求值为静态值，
// useVirtualizer 内部的 watch(() => unref(options)) 不会在 count 变化时重新 setOptions，
// 翻页新增的数据将无法渲染，虚拟化形同虚设。
const virtualizer = useVirtualizer(
  computed(() => ({
    count: rowCount.value,
    estimateSize: () => props.rowHeight + props.gap,
    overscan: props.overscan,
    getScrollElement: () => scrollRef.value,
    gap: props.gap,
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

const containerStyle = computed(() => {
  const style: Record<string, string> = {}
  if (props.maxHeight) {
    style.maxHeight = props.maxHeight
  }
  return style
})

const rowStyle = (row: { start: number }) => ({
  position: 'absolute' as const,
  top: 0,
  left: 0,
  width: '100%',
  transform: `translateY(${row.start}px)`,
  display: 'grid',
  gap: `${props.gap}px`,
})

// 用于 slot 内部需要原始 index 的场景（如选择集合恢复）
const itemIndex = (item: any) => props.items.indexOf(item)

const itemKey = (item: any) => (item && item.id != null ? item.id : itemIndex(item))

let loadingMore = false
const maybeLoadMore = () => {
  if (loadingMore || props.loading || !props.hasMore) return
  const items = virtualizer.value.getVirtualItems()
  if (items.length === 0) return
  const lastVisibleRow = items[items.length - 1]?.index ?? 0
  if (rowCount.value - lastVisibleRow <= props.loadThresholdRows) {
    loadingMore = true
    emit('load-more')
    // 防止同一帧内多次触发；下一页返回后会重置 loading，watch 会解除锁定
    nextTick(() => {
      loadingMore = false
    })
  }
}
// 滚动时检查是否需要加载下一页；同时监听 items 变化（首次加载 / 翻页后）。
const onScroll = () => maybeLoadMore()
watch(
  () => props.items.length,
  () => nextTick(() => maybeLoadMore()),
)
watch(
  () => props.loading,
  loading => {
    if (!loading) loadingMore = false
  },
)

// 密度切换：以切换前第一个可见项为滚动锚点，尽量保持在视口附近。
// 由父组件调用 getFirstVisibleIndex / scrollToIndex。
const getFirstVisibleIndex = (): number => {
  const items = virtualizer.value.getVirtualItems()
  if (items.length === 0) return 0
  return (items[0]?.index ?? 0) * props.columns
}

const scrollToIndex = (index: number) => {
  const rowIndex = Math.floor(index / props.columns)
  virtualizer.value.scrollToIndex(rowIndex, { align: 'start' })
}

// tab pane 由隐藏变可见时 virtualizer 需重新测量，否则可见区间可能停留在隐藏前的状态。
const measure = () => {
  virtualizer.value?.measure()
}

defineExpose({
  getFirstVisibleIndex,
  scrollToIndex,
  measure,
  scrollRef,
})
</script>

<style scoped>
.virtual-media-grid {
  width: 100%;
  overflow-y: auto;
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
