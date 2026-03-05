<template>
  <div class="devices-page">
    <!-- 设备统计 -->
    <el-row :gutter="20" style="margin-bottom: 20px">
      <el-col :span="8">
        <el-card shadow="hover">
          <el-statistic title="总设备数" :value="stats?.total || 0">
            <template #prefix>
              <el-icon><Monitor /></el-icon>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="hover">
          <el-statistic title="在线设备" :value="stats?.online || 0">
            <template #prefix>
              <el-icon style="color: #67c23a"><CircleCheck /></el-icon>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="hover">
          <el-statistic title="离线设备" :value="(stats?.total || 0) - (stats?.online || 0)">
            <template #prefix>
              <el-icon style="color: #f56c6c"><CircleClose /></el-icon>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
    </el-row>

    <!-- 设备列表 -->
    <el-card shadow="never" v-loading="loading">
      <template #header>
        <div style="display: flex; justify-content: space-between; align-items: center">
          <span><el-icon><List /></el-icon> 设备列表</span>
          <div style="display: flex; gap: 12px">
            <el-button type="primary" @click="openCreateDialog">
              <el-icon><Plus /></el-icon>
              新增设备
            </el-button>
            <el-button @click="loadDevices">
              <el-icon><Refresh /></el-icon>
              刷新
            </el-button>
          </div>
        </div>
      </template>

      <el-table :data="devices" stripe>
        <el-table-column prop="device_id" label="设备 ID" width="120" />
        <el-table-column prop="name" label="设备名称" />
        <el-table-column label="类型" width="90">
          <template #default="{ row }">
            <el-tag size="small">{{ row.device_type || 'esp32' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="可用" width="70">
          <template #default="{ row }">
            <el-switch
              v-model="row.is_enabled"
              @change="(val: boolean) => toggleEnabled(row, val)"
              style="--el-switch-on-color: #67c23a"
            />
          </template>
        </el-table-column>
        <el-table-column label="状态" width="70">
          <template #default="{ row }">
            <el-tag :type="row.online ? 'success' : 'info'" size="small">
              {{ row.online ? '在线' : '离线' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="ip_address" label="IP 地址" width="130" />
        <el-table-column label="最后请求" width="150">
          <template #default="{ row }">
            {{ formatTime(row.last_heartbeat) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }">
            <el-button link @click="viewDevice(row.device_id)" style="color: var(--color-primary);">
              详情
            </el-button>
            <el-button link type="danger" @click="deleteDevice(row)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div style="margin-top: 20px; text-align: center">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="loadDevices"
          @current-change="loadDevices"
        />
      </div>
    </el-card>

    <!-- 创建设备对话框 -->
    <el-dialog v-model="createDialogVisible" title="新增设备" width="550px">
      <el-form :model="createForm" label-width="100px" ref="createFormRef">
        <el-form-item label="设备名称" required>
          <el-input v-model="createForm.name" placeholder="例如: 客厅相框" />
        </el-form-item>
        <el-form-item label="设备类型">
          <el-select v-model="createForm.device_type" placeholder="选择设备类型" style="width: 100%">
            <el-option label="ESP32" value="esp32" />
            <el-option label="ESP8266" value="esp8266" />
            <el-option label="Android" value="android" />
            <el-option label="iOS" value="ios" />
            <el-option label="Web" value="web" />
            <el-option label="Analyzer" value="analyzer" />
          </el-select>
        </el-form-item>
        <el-form-item label="平台">
          <el-select v-model="createForm.platform" placeholder="选择平台" style="width: 100%">
            <el-option label="嵌入式 (Embedded)" value="embedded" />
            <el-option label="移动端 (Mobile)" value="mobile" />
            <el-option label="Web" value="web" />
            <el-option label="服务 (Service)" value="service" />
          </el-select>
        </el-form-item>
        <el-form-item label="屏幕尺寸">
          <div style="display: flex; gap: 12px; align-items: center">
            <el-input-number v-model="createForm.screen_width" :min="1" placeholder="宽" />
            <span>×</span>
            <el-input-number v-model="createForm.screen_height" :min="1" placeholder="高" />
          </div>
        </el-form-item>
        <el-form-item label="硬件型号">
          <el-input v-model="createForm.hardware_model" placeholder="例如: ESP32-S3" />
        </el-form-item>
        <el-form-item label="MAC 地址">
          <el-input v-model="createForm.mac_address" placeholder="例如: AA:BB:CC:DD:EE:FF" />
        </el-form-item>
        <el-form-item label="固件版本">
          <el-input v-model="createForm.firmware_version" placeholder="例如: 1.0.0" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="createForm.description" type="textarea" rows="2" placeholder="可选" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="createDevice" :loading="creating">
          创建设备
        </el-button>
      </template>
    </el-dialog>

    <!-- 创建成功 - 显示 API Key -->
    <el-dialog v-model="apiKeyDialogVisible" title="设备创建成功" width="550px" :close-on-click-modal="false">
      <el-alert
        title="请妥善保存 API Key"
        description="此 API Key 仅在创建时显示一次，关闭后将无法再次查看。请将其配置到设备中使用。"
        type="warning"
        :closable="false"
        style="margin-bottom: 20px"
      />
      <el-descriptions :column="1" border>
        <el-descriptions-item label="设备 ID">{{ createdDevice?.device_id }}</el-descriptions-item>
        <el-descriptions-item label="设备名称">{{ createdDevice?.name }}</el-descriptions-item>
        <el-descriptions-item label="API Key">
          <div style="display: flex; gap: 12px">
            <el-input
              :model-value="createdDevice?.api_key"
              type="password"
              show-password
              readonly
              style="flex: 1"
            />
            <el-button type="primary" @click="copyApiKey(createdDevice?.api_key)">
              <el-icon><CopyDocument /></el-icon>
              复制
            </el-button>
          </div>
        </el-descriptions-item>
      </el-descriptions>
      <template #footer>
        <el-button type="primary" @click="closeApiKeyDialog">我已保存</el-button>
      </template>
    </el-dialog>

    <!-- 设备详情对话框 -->
    <el-dialog v-model="detailVisible" title="设备详情" width="600px">
      <el-descriptions :column="1" border v-if="currentDevice">
        <el-descriptions-item label="设备 ID">{{ currentDevice.device_id }}</el-descriptions-item>
        <el-descriptions-item label="设备名称">{{ currentDevice.name }}</el-descriptions-item>
        <el-descriptions-item label="API Key">
          <div style="display: flex; align-items: center; gap: 12px">
            <el-input
              v-model="currentDevice.api_key"
              type="password"
              show-password
              readonly
              style="flex: 1"
            />
            <el-button type="primary" @click="copyApiKey(currentDevice.api_key)">
              <el-icon><CopyDocument /></el-icon>
              复制
            </el-button>
          </div>
          <div class="form-hint">设备使用此 API Key 访问系统</div>
        </el-descriptions-item>
        <el-descriptions-item label="可用状态">
          <el-tag :type="currentDevice.is_enabled ? 'success' : 'danger'">
            {{ currentDevice.is_enabled ? '可用' : '已禁用' }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="在线状态">
          <el-tag :type="currentDevice.online ? 'success' : 'info'">
            {{ currentDevice.online ? '在线' : '离线' }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="设备类型">{{ currentDevice.device_type || '-' }}</el-descriptions-item>
        <el-descriptions-item label="平台">{{ currentDevice.platform || '-' }}</el-descriptions-item>
        <el-descriptions-item label="IP 地址">{{ currentDevice.ip_address || '-' }}</el-descriptions-item>
        <el-descriptions-item label="硬件型号">{{ currentDevice.hardware_model || '-' }}</el-descriptions-item>
        <el-descriptions-item label="MAC 地址">{{ currentDevice.mac_address || '-' }}</el-descriptions-item>
        <el-descriptions-item label="固件版本">{{ currentDevice.firmware_version || '-' }}</el-descriptions-item>
        <el-descriptions-item label="屏幕尺寸">
          {{ currentDevice.screen_width && currentDevice.screen_height
            ? `${currentDevice.screen_width} × ${currentDevice.screen_height}`
            : '-' }}
        </el-descriptions-item>
        <el-descriptions-item label="最后请求">{{ formatTime(currentDevice.last_heartbeat) }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(currentDevice.created_at) }}</el-descriptions-item>
      </el-descriptions>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { deviceApi, type CreateDeviceRequest, type CreateDeviceResponse } from '@/api/device'
import type { ESP32Device, DeviceStats } from '@/types/device'
import dayjs from 'dayjs'
import { Monitor, CircleCheck, CircleClose, List, Refresh, Plus, CopyDocument } from '@element-plus/icons-vue'

const devices = ref<ESP32Device[]>([])
const stats = ref<DeviceStats | null>(null)
const loading = ref(false)
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const detailVisible = ref(false)
const currentDevice = ref<ESP32Device | null>(null)

// 创建设备相关
const createDialogVisible = ref(false)
const creating = ref(false)
const createFormRef = ref()
const createForm = reactive<CreateDeviceRequest>({
  name: '',
  device_type: 'esp32',
  platform: 'embedded',
  screen_width: 800,
  screen_height: 600,
  hardware_model: '',
  mac_address: '',
  firmware_version: '',
  description: ''
})

// API Key 显示
const apiKeyDialogVisible = ref(false)
const createdDevice = ref<CreateDeviceResponse | null>(null)

// 格式化时间
const formatTime = (time?: string) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

// 加载设备列表
const loadDevices = async () => {
  loading.value = true
  try {
    const res = await deviceApi.getList({
      page: currentPage.value,
      page_size: pageSize.value,
    })
    devices.value = res.data?.data?.items || []
    total.value = res.data?.data?.total || 0
  } catch (error: any) {
    ElMessage.error(error.message || '加载设备列表失败')
  } finally {
    loading.value = false
  }
}

// 加载设备统计
const loadStats = async () => {
  try {
    const res = await deviceApi.getStats()
    stats.value = res.data?.data || null
  } catch (error) {
    console.error('Failed to load device stats:', error)
  }
}

// 打开创建设备对话框
const openCreateDialog = () => {
  createForm.name = ''
  createForm.device_type = 'esp32'
  createForm.platform = 'embedded'
  createForm.screen_width = 800
  createForm.screen_height = 600
  createForm.hardware_model = ''
  createForm.mac_address = ''
  createForm.firmware_version = ''
  createForm.description = ''
  createDialogVisible.value = true
}

// 创建设备
const createDevice = async () => {
  if (!createForm.name) {
    ElMessage.warning('请填写设备名称')
    return
  }

  creating.value = true
  try {
    const res = await deviceApi.create(createForm)
    if (res.data?.data) {
      createdDevice.value = res.data.data
      createDialogVisible.value = false
      apiKeyDialogVisible.value = true
      ElMessage.success('设备创建成功')
      await loadDevices()
      await loadStats()
    }
  } catch (error: any) {
    ElMessage.error(error.message || '创建设备失败')
  } finally {
    creating.value = false
  }
}

// 切换设备可用状态
const toggleEnabled = async (row: ESP32Device, enabled: boolean) => {
  try {
    await deviceApi.updateEnabled(row.id, enabled)
    ElMessage.success(enabled ? '设备已启用' : '设备已禁用')
    // 更新本地状态
    row.is_enabled = enabled
  } catch (error: any) {
    ElMessage.error(error.message || '操作失败')
    // 恢复开关状态
    row.is_enabled = !enabled
  }
}

// 关闭 API Key 对话框
const closeApiKeyDialog = () => {
  apiKeyDialogVisible.value = false
  createdDevice.value = null
}

// 删除设备
const deleteDevice = async (row: ESP32Device) => {
  try {
    await ElMessageBox.confirm(
      `确定要删除设备「${row.name || row.device_id}」吗？此操作不可恢复！`,
      '确认删除',
      {
        type: 'warning',
        confirmButtonText: '确认删除',
        cancelButtonText: '取消'
      }
    )

    await deviceApi.delete(row.id)
    ElMessage.success('删除成功')
    await loadDevices()
    await loadStats()
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || '删除失败')
    }
  }
}

// 查看设备详情
const viewDevice = async (deviceId: string) => {
  try {
    const res = await deviceApi.getById(deviceId)
    currentDevice.value = res.data?.data || null
    detailVisible.value = true
  } catch (error: any) {
    ElMessage.error(error.message || '加载设备详情失败')
  }
}

// 复制 API Key
const copyApiKey = async (apiKey?: string) => {
  if (!apiKey) {
    ElMessage.warning('API Key 不存在')
    return
  }
  try {
    await navigator.clipboard.writeText(apiKey)
    ElMessage.success('API Key 已复制到剪贴板')
  } catch (err) {
    const textarea = document.createElement('textarea')
    textarea.value = apiKey
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
    ElMessage.success('API Key 已复制到剪贴板')
  }
}

onMounted(async () => {
  await loadDevices()
  await loadStats()
})
</script>

<style scoped>
.devices-page {
  padding: 20px;
}

.form-hint {
  font-size: 12px;
  color: var(--color-text-tertiary);
  margin-top: 4px;
}
</style>
