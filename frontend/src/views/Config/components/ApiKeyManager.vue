<template>
  <el-card shadow="never" class="api-key-card">
    <template #header>
      <div class="card-header">
        <div class="header-title">
          <el-icon class="header-icon"><Key /></el-icon>
          <span>API Key 管理</span>
        </div>
        <el-button type="primary" @click="handleCreate">
          <el-icon><Plus /></el-icon>
          创建 API Key
        </el-button>
      </div>
    </template>

    <el-alert
      title="API Key 用于设备访问系统"
      description="设备（ESP32/Android/iOS等）通过 API Key 获取照片和上报状态。请妥善保管，不要泄露给他人。"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 16px"
    />

    <el-empty v-if="!apiKeys.length && !loading" description="暂无 API Key">
      <el-button type="primary" @click="handleCreate">创建第一个 API Key</el-button>
    </el-empty>

    <div v-else v-loading="loading" class="api-key-list">
      <div
        v-for="key in apiKeys"
        :key="key.id"
        class="api-key-item"
        :class="{ disabled: !key.is_active || isExpired(key.expires_at) }"
      >
        <div class="key-info">
          <div class="key-header">
            <el-switch
              v-model="key.is_active"
              @change="handleToggleStatus(key)"
              active-text="启用"
              inactive-text="禁用"
              inline-prompt
              style="--el-switch-on-color: var(--color-success); --el-switch-off-color: var(--color-error)"
            />
            <span class="key-name">{{ key.name }}</span>
            <el-tag v-if="!key.is_active" type="danger" size="small">已禁用</el-tag>
            <el-tag v-else-if="isExpired(key.expires_at)" type="warning" size="small">已过期</el-tag>
            <el-tag v-else type="success" size="small">正常</el-tag>
          </div>
          <div class="key-description">{{ key.description || '无描述' }}</div>
          <div class="key-meta">
            <span>创建于: {{ formatTime(key.created_at) }}</span>
            <span v-if="key.last_used_at">最后使用: {{ formatTime(key.last_used_at) }}</span>
            <span>使用次数: {{ key.use_count }}</span>
            <span v-if="key.expires_at">过期时间: {{ formatTime(key.expires_at) }}</span>
          </div>
        </div>
        <div class="key-actions">
          <el-button
            v-if="newlyCreatedKey?.id === key.id"
            type="success"
            size="small"
            @click="copyKey(newlyCreatedKey.key)"
          >
            <el-icon><CopyDocument /></el-icon>
            复制 Key
          </el-button>
          <el-button
            link
            @click="handleRegenerate(key)"
            style="color: var(--color-primary)"
          >
            重新生成
          </el-button>
          <el-button
            link
            @click="handleDelete(key)"
            style="color: var(--color-error)"
          >
            删除
          </el-button>
        </div>
      </div>
    </div>

    <!-- Create Dialog -->
    <el-dialog
      v-model="dialogVisible"
      title="创建 API Key"
      width="500px"
    >
      <el-form :model="form" label-width="80px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="例如: 客厅相框、我的手机" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input
            v-model="form.description"
            type="textarea"
            rows="3"
            placeholder="可选：描述此API Key的用途"
          />
        </el-form-item>
        <el-form-item label="过期时间">
          <el-date-picker
            v-model="form.expires_at"
            type="datetime"
            placeholder="可选：选择过期时间"
            value-format="YYYY-MM-DDTHH:mm:ss"
            style="width: 100%"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleSave" :loading="saving">
          创建
        </el-button>
      </template>
    </el-dialog>

    <!-- Show Key Dialog -->
    <el-dialog
      v-model="showKeyDialogVisible"
      title="API Key 创建成功"
      width="500px"
      :close-on-click-modal="false"
    >
      <el-alert
        title="请立即保存此API Key"
        description="此Key值只显示一次，关闭后将无法再次查看。"
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 16px"
      />
      <div class="key-display">
        <el-input
          v-model="displayedKey"
          readonly
          type="textarea"
          rows="3"
        />
        <el-button type="primary" @click="copyKey(displayedKey)" style="margin-top: 12px">
          <el-icon><CopyDocument /></el-icon>
          复制到剪贴板
        </el-button>
      </div>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import dayjs from 'dayjs'
import { getAPIKeys, createAPIKey, updateAPIKey, deleteAPIKey, regenerateAPIKey } from '@/api/config'
import type { APIKey, APIKeyWithKey } from '@/api/config'

const apiKeys = ref<APIKey[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const showKeyDialogVisible = ref(false)
const saving = ref(false)
const newlyCreatedKey = ref<{ id: number; key: string } | null>(null)
const displayedKey = ref('')

const form = ref({
  name: '',
  description: '',
  expires_at: undefined as string | undefined
})

const formatTime = (time?: string) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

const isExpired = (expiresAt?: string) => {
  if (!expiresAt) return false
  return dayjs().isAfter(dayjs(expiresAt))
}

const loadAPIKeys = async () => {
  loading.value = true
  try {
    const keys = await getAPIKeys()
    apiKeys.value = keys
  } catch (error: any) {
    ElMessage.error('加载 API Keys 失败')
  } finally {
    loading.value = false
  }
}

const handleCreate = () => {
  form.value = {
    name: '',
    description: '',
    expires_at: undefined
  }
  dialogVisible.value = true
}

const handleSave = async () => {
  if (!form.value.name) {
    ElMessage.warning('请输入名称')
    return
  }

  saving.value = true
  try {
    const result = await createAPIKey({
      name: form.value.name,
      description: form.value.description,
      expires_at: form.value.expires_at
    })
    newlyCreatedKey.value = { id: result.id, key: result.key }
    dialogVisible.value = false
    displayedKey.value = result.key
    showKeyDialogVisible.value = true
    ElMessage.success('API Key 创建成功')
    await loadAPIKeys()
  } catch (error: any) {
    ElMessage.error(error.message || '创建失败')
  } finally {
    saving.value = false
  }
}

const handleToggleStatus = async (key: APIKey) => {
  try {
    // key.is_active 已经被 switch 切换了，直接提交新值
    await updateAPIKey(key.id, { is_active: key.is_active })
    ElMessage.success(key.is_active ? '已启用' : '已禁用')
    await loadAPIKeys()
  } catch (error: any) {
    // 失败时恢复原状态
    key.is_active = !key.is_active
    ElMessage.error(error.message || '操作失败')
  }
}

const handleRegenerate = async (key: APIKey) => {
  try {
    await ElMessageBox.confirm(
      `确定要重新生成「${key.name}」的API Key吗？旧的Key将立即失效！`,
      '确认重新生成',
      {
        type: 'warning',
        confirmButtonText: '确认重新生成',
        cancelButtonText: '取消'
      }
    )

    const result = await regenerateAPIKey(key.id)
    newlyCreatedKey.value = { id: result.id, key: result.key }
    displayedKey.value = result.key
    showKeyDialogVisible.value = true
    ElMessage.success('API Key 重新生成成功')
    await loadAPIKeys()
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || '重新生成失败')
    }
  }
}

const handleDelete = async (key: APIKey) => {
  try {
    await ElMessageBox.confirm(
      `确定要删除 API Key「${key.name}」吗？此操作不可恢复！`,
      '确认删除',
      {
        type: 'error',
        confirmButtonText: '确认删除',
        cancelButtonText: '取消'
      }
    )

    await deleteAPIKey(key.id)
    ElMessage.success('删除成功')
    await loadAPIKeys()
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || '删除失败')
    }
  }
}

const copyKey = async (key: string) => {
  try {
    await navigator.clipboard.writeText(key)
    ElMessage.success('已复制到剪贴板')
  } catch (err) {
    const textarea = document.createElement('textarea')
    textarea.value = key
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
    ElMessage.success('已复制到剪贴板')
  }
}

onMounted(() => {
  loadAPIKeys()
})
</script>

<style scoped>
.api-key-card {
  margin-top: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.header-icon {
  color: var(--color-primary);
}

.api-key-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.api-key-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  transition: all 0.3s;
}

.api-key-item:hover {
  border-color: var(--color-primary);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.api-key-item.disabled {
  opacity: 0.6;
  background-color: var(--color-bg-secondary);
}

.key-info {
  flex: 1;
}

.key-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;
  font-weight: 600;
}

.key-name {
  font-weight: 600;
  font-size: 15px;
}

.key-description {
  color: var(--color-text-secondary);
  margin-bottom: 8px;
  font-size: 14px;
}

.key-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  font-size: 13px;
  color: var(--color-text-tertiary);
}

.key-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.key-display {
  display: flex;
  flex-direction: column;
}
</style>
