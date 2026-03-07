<template>
  <el-dialog
    v-model="visible"
    title="选择目录"
    width="600px"
    :close-on-click-modal="false"
  >
      <el-alert
      v-if="isDocker"
      type="info"
      :closable="false"
      class="docker-path-alert"
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
    </div>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" @click="handleConfirm">确认选择</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Folder, FolderOpened, ArrowUp } from '@element-plus/icons-vue'
import { configApi } from '@/api/config'
import { systemApi } from '@/api/system'

interface DirectoryEntry {
  name: string
  path: string
  is_dir: boolean
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
const currentPath = ref('/')
const defaultPath = ref('/')
const isDocker = ref(false)
const entries = ref<DirectoryEntry[]>([])

// 获取环境信息（默认路径）
const loadEnvironment = async () => {
  try {
    const response = await systemApi.getEnvironment()
    if (response.data?.data) {
      isDocker.value = response.data.data.is_docker
      defaultPath.value = response.data.data.default_path
      currentPath.value = defaultPath.value
    }
  } catch (error) {
    console.error('Failed to load environment:', error)
    // 使用默认路径作为回退
    isDocker.value = false
    defaultPath.value = '/'
    currentPath.value = '/'
  }
}

// 组件挂载时加载环境信息
onMounted(() => {
  loadEnvironment()
})

watch(() => props.modelValue, (val) => {
  visible.value = val
  if (val) {
    const startPath = props.initialPath || defaultPath.value
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
.docker-path-alert {
  margin-bottom: 16px;
}
</style>
