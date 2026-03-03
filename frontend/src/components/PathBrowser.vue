<template>
  <el-dialog
    v-model="visible"
    title="选择目录"
    width="600px"
    :close-on-click-modal="false"
  >
    <el-alert
      type="info"
      :closable="false"
      style="margin-bottom: 16px"
    >
      <template #title>
        <strong>Docker 路径说明</strong>
      </template>
      这里显示的是容器内的路径。如需挂载新的宿主机目录，请编辑 docker-compose.yml 文件中的 volumes 配置。
    </el-alert>
    <div class="path-browser">
      <!-- Current Path Display -->
      <div class="current-path">
        <el-icon><FolderOpened /></el-icon>
        <el-input
          v-model="currentPath"
          readonly
          class="path-input"
        />
      </div>

      <!-- Directory List -->
      <div class="directory-list" v-loading="loading">
        <div
          v-for="entry in entries"
          :key="entry.path"
          class="directory-item"
          :class="{ 'is-parent': entry.name === '..' }"
          @click="handleSelectEntry(entry)"
        >
          <el-icon v-if="entry.name === '..'"><ArrowUp /></el-icon>
          <el-icon v-else><Folder /></el-icon>
          <span class="entry-name">{{ entry.name }}</span>
        </div>

        <el-empty
          v-if="!loading && entries.length === 0"
          description="没有子目录"
          :image-size="80"
        />
      </div>

      <!-- Quick Access -->
      <div class="quick-access">
        <div class="quick-access-title">快速访问</div>
        <div class="quick-access-items">
          <el-tag
            v-for="shortcut in shortcuts"
            :key="shortcut.path"
            class="shortcut-tag"
            @click="loadDirectory(shortcut.path)"
          >
            {{ shortcut.name }}
          </el-tag>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" @click="handleConfirm">确认选择</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Folder, FolderOpened, ArrowUp } from '@element-plus/icons-vue'
import { configApi } from '@/api/config'

interface DirectoryEntry {
  name: string
  path: string
  is_dir: boolean
}

interface Shortcut {
  name: string
  path: string
}

const props = defineProps<{
  modelValue: boolean
  initialPath?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'select', path: string): void
}>()

const visible = ref(props.modelValue)
const loading = ref(false)
const currentPath = ref('/app')
const entries = ref<DirectoryEntry[]>([])

// Quick access shortcuts for Docker mounted paths
const shortcuts: Shortcut[] = [
  { name: '主目录 (/app/photos)', path: '/app/photos' },
  { name: '额外目录 1', path: '/app/photos_extra1' },
  { name: '额外目录 2', path: '/app/photos_extra2' },
  { name: '额外目录 3', path: '/app/photos_extra3' },
  { name: '根目录', path: '/app' },
]

watch(() => props.modelValue, (val) => {
  visible.value = val
  if (val) {
    const startPath = props.initialPath || '/app'
    loadDirectory(startPath)
  }
})

watch(() => visible.value, (val) => {
  emit('update:modelValue', val)
})

const loadDirectory = async (path: string) => {
  loading.value = true
  try {
    const result = await configApi.listDirectories(path)
    entries.value = result.entries
    currentPath.value = result.current_path
  } catch (error: any) {
    ElMessage.error('加载目录失败: ' + (error.message || '未知错误'))
  } finally {
    loading.value = false
  }
}

const handleSelectEntry = (entry: DirectoryEntry) => {
  if (entry.is_dir) {
    loadDirectory(entry.path)
  }
}

const handleConfirm = () => {
  emit('select', currentPath.value)
  visible.value = false
}
</script>

<style scoped>
.path-browser {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.current-path {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px;
  background: var(--el-fill-color-light);
  border-radius: 8px;
}

.current-path .el-icon {
  font-size: 20px;
  color: var(--el-color-primary);
}

.path-input {
  flex: 1;
}

.path-input :deep(.el-input__inner) {
  font-family: monospace;
  font-size: 14px;
}

.directory-list {
  max-height: 300px;
  overflow-y: auto;
  border: 1px solid var(--el-border-color);
  border-radius: 8px;
  padding: 8px;
}

.directory-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s;
}

.directory-item:hover {
  background: var(--el-fill-color-light);
}

.directory-item.is-parent {
  color: var(--el-color-primary);
  font-weight: 500;
}

.directory-item .el-icon {
  font-size: 18px;
  color: var(--el-color-warning);
}

.directory-item.is-parent .el-icon {
  color: var(--el-color-primary);
}

.entry-name {
  font-size: 14px;
}

.quick-access {
  padding-top: 12px;
  border-top: 1px solid var(--el-border-color);
}

.quick-access-title {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  margin-bottom: 8px;
}

.quick-access-items {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.shortcut-tag {
  cursor: pointer;
}

.shortcut-tag:hover {
  background: var(--el-color-primary-light-9);
  border-color: var(--el-color-primary);
  color: var(--el-color-primary);
}
</style>
