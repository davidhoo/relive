<template>
  <div class="config-page">
    <el-card shadow="never">
      <template #header>
        <div style="display: flex; justify-content: space-between; align-items: center">
          <span><el-icon><Setting /></el-icon> 配置管理</span>
          <el-button type="primary" @click="handleAddConfig">
            <el-icon><Plus /></el-icon>
            新增配置
          </el-button>
        </div>
      </template>

      <el-table :data="configs" stripe v-loading="loading">
        <el-table-column prop="key" label="配置键" width="250" />
        <el-table-column prop="value" label="配置值" />
        <el-table-column prop="description" label="描述" />
        <el-table-column label="更新时间" width="180">
          <template #default="{ row }">
            {{ formatTime(row.updated_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row)">编辑</el-button>
            <el-button type="danger" link @click="handleDelete(row.key)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 编辑对话框 -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑配置' : '新增配置'"
      width="600px"
    >
      <el-form :model="form" label-width="100px">
        <el-form-item label="配置键">
          <el-input v-model="form.key" :disabled="isEdit" />
        </el-form-item>
        <el-form-item label="配置值">
          <el-input v-model="form.value" type="textarea" :rows="3" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleSave" :loading="saving">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import dayjs from 'dayjs'

interface Config {
  key: string
  value: string
  description: string
  updated_at: string
}

const configs = ref<Config[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const isEdit = ref(false)
const saving = ref(false)

const form = ref({
  key: '',
  value: '',
  description: '',
})

// 格式化时间
const formatTime = (time?: string) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

// 加载配置列表
const loadConfigs = async () => {
  loading.value = true
  try {
    // TODO: 调用配置列表 API
    await new Promise(resolve => setTimeout(resolve, 500))
    configs.value = [
      {
        key: 'ai.provider',
        value: 'qwen',
        description: 'AI Provider 配置',
        updated_at: new Date().toISOString(),
      },
      {
        key: 'display.algorithm',
        value: 'score_based',
        description: '展示算法配置',
        updated_at: new Date().toISOString(),
      },
    ]
  } catch (error: any) {
    ElMessage.error(error.message || '加载配置失败')
  } finally {
    loading.value = false
  }
}

// 新增配置
const handleAddConfig = () => {
  isEdit.value = false
  form.value = { key: '', value: '', description: '' }
  dialogVisible.value = true
}

// 编辑配置
const handleEdit = (config: Config) => {
  isEdit.value = true
  form.value = { ...config }
  dialogVisible.value = true
}

// 保存配置
const handleSave = async () => {
  if (!form.value.key || !form.value.value) {
    ElMessage.warning('请填写必填字段')
    return
  }

  saving.value = true
  try {
    // TODO: 调用保存配置 API
    await new Promise(resolve => setTimeout(resolve, 500))
    ElMessage.success('保存成功')
    dialogVisible.value = false
    await loadConfigs()
  } catch (error: any) {
    ElMessage.error(error.message || '保存失败')
  } finally {
    saving.value = false
  }
}

// 删除配置
const handleDelete = async (configKey: string) => {
  try {
    await ElMessageBox.confirm('确认删除该配置？', '提示', {
      type: 'warning',
    })

    // TODO: 调用删除配置 API
    await new Promise(resolve => setTimeout(resolve, 500))
    ElMessage.success('删除成功')
    await loadConfigs()
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || '删除失败')
    }
  }
}

onMounted(() => {
  loadConfigs()
})
</script>

<style scoped>
.config-page {
  padding: 20px;
}
</style>
