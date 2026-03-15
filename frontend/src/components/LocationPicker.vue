<template>
  <el-dialog
    :model-value="visible"
    @update:model-value="$emit('update:visible', $event)"
    title="选择位置"
    width="800px"
    :close-on-click-modal="false"
    @opened="handleDialogOpened"
    @closed="handleDialogClosed"
  >
    <!-- 搜索栏 -->
    <div class="search-bar">
      <el-input
        v-model="searchQuery"
        placeholder="搜索地名..."
        clearable
        @keyup.enter="handleSearch"
        :prefix-icon="Search"
      />
      <el-button type="primary" @click="handleSearch" :loading="searching">
        搜索
      </el-button>
    </div>

    <!-- 搜索结果列表 -->
    <div v-if="searchResults.length > 0" class="search-results">
      <div
        v-for="result in searchResults"
        :key="result.place_id"
        class="search-result-item"
        @click="selectSearchResult(result)"
      >
        {{ result.display_name }}
      </div>
    </div>

    <!-- 地图容器 -->
    <div ref="mapContainer" class="map-container"></div>

    <!-- 当前选中坐标 -->
    <div class="coord-display" v-if="selectedLat !== null && selectedLng !== null">
      <el-tag type="info">
        {{ selectedLat.toFixed(6) }}, {{ selectedLng.toFixed(6) }}
      </el-tag>
    </div>
    <div class="coord-display" v-else>
      <el-text type="info" size="small">点击地图选择位置</el-text>
    </div>

    <template #footer>
      <el-button @click="$emit('update:visible', false)">取消</el-button>
      <el-button
        type="primary"
        @click="handleConfirm"
        :disabled="selectedLat === null || selectedLng === null"
      >
        确认
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { Search } from '@element-plus/icons-vue'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'

// Fix Leaflet default icon path issue with Vite
import iconUrl from 'leaflet/dist/images/marker-icon.png'
import iconRetinaUrl from 'leaflet/dist/images/marker-icon-2x.png'
import shadowUrl from 'leaflet/dist/images/marker-shadow.png'

delete (L.Icon.Default.prototype as any)._getIconUrl
L.Icon.Default.mergeOptions({
  iconUrl,
  iconRetinaUrl,
  shadowUrl,
})

const props = defineProps<{
  visible: boolean
  initialLat?: number
  initialLng?: number
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  confirm: [coords: { latitude: number; longitude: number }]
}>()

const mapContainer = ref<HTMLDivElement>()
let map: L.Map | null = null
let marker: L.Marker | null = null

const searchQuery = ref('')
const searching = ref(false)
const searchResults = ref<any[]>([])

const selectedLat = ref<number | null>(null)
const selectedLng = ref<number | null>(null)

const handleDialogOpened = () => {
  nextTick(() => {
    initMap()
  })
}

const handleDialogClosed = () => {
  if (map) {
    map.remove()
    map = null
    marker = null
  }
  searchResults.value = []
  searchQuery.value = ''
}

const initMap = () => {
  if (!mapContainer.value || map) return

  const hasInitial = props.initialLat != null && props.initialLng != null
  const center: [number, number] = hasInitial
    ? [props.initialLat!, props.initialLng!]
    : [35, 105]
  const zoom = hasInitial ? 12 : 4

  map = L.map(mapContainer.value).setView(center, zoom)

  L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
    attribution: '&copy; OpenStreetMap contributors',
    maxZoom: 19,
  }).addTo(map)

  if (hasInitial) {
    selectedLat.value = props.initialLat!
    selectedLng.value = props.initialLng!
    marker = L.marker(center).addTo(map)
  }

  map.on('click', (e: L.LeafletMouseEvent) => {
    placeMarker(e.latlng.lat, e.latlng.lng)
  })

  // Force resize after render
  setTimeout(() => map?.invalidateSize(), 100)
}

const placeMarker = (lat: number, lng: number) => {
  if (!map) return
  selectedLat.value = lat
  selectedLng.value = lng

  if (marker) {
    marker.setLatLng([lat, lng])
  } else {
    marker = L.marker([lat, lng]).addTo(map)
  }
}

const handleSearch = async () => {
  if (!searchQuery.value.trim()) return
  searching.value = true
  searchResults.value = []
  try {
    const params = new URLSearchParams({
      format: 'json',
      q: searchQuery.value.trim(),
      limit: '5',
      'accept-language': 'zh',
    })
    const resp = await fetch(`https://nominatim.openstreetmap.org/search?${params}`)
    searchResults.value = await resp.json()
  } catch {
    // silently fail
  } finally {
    searching.value = false
  }
}

const selectSearchResult = (result: any) => {
  const lat = parseFloat(result.lat)
  const lng = parseFloat(result.lon)
  if (map) {
    map.setView([lat, lng], 14)
  }
  placeMarker(lat, lng)
  searchResults.value = []
}

const handleConfirm = () => {
  if (selectedLat.value !== null && selectedLng.value !== null) {
    emit('confirm', {
      latitude: selectedLat.value,
      longitude: selectedLng.value,
    })
    emit('update:visible', false)
  }
}

// Reset selected coords when dialog opens with new props
watch(() => props.visible, (val) => {
  if (val) {
    if (props.initialLat != null && props.initialLng != null) {
      selectedLat.value = props.initialLat
      selectedLng.value = props.initialLng
    } else {
      selectedLat.value = null
      selectedLng.value = null
    }
  }
})
</script>

<style scoped>
.search-bar {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
}

.search-results {
  max-height: 150px;
  overflow-y: auto;
  border: 1px solid var(--el-border-color-light);
  border-radius: 4px;
  margin-bottom: 8px;
}

.search-result-item {
  padding: 8px 12px;
  cursor: pointer;
  font-size: 13px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}

.search-result-item:last-child {
  border-bottom: none;
}

.search-result-item:hover {
  background-color: var(--el-fill-color-light);
}

.map-container {
  width: 100%;
  height: 450px;
  border-radius: 4px;
  border: 1px solid var(--el-border-color-light);
}

.coord-display {
  margin-top: 8px;
  text-align: center;
}
</style>
