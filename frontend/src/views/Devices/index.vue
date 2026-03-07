<template>
  <div class="devices-page">
    <PageHeader title="设备管理" subtitle="管理设备、查看状态并维护展示配置" :gradient="true">
      <template #actions>
        <el-button type="primary" @click="openCreateDialog">
          <el-icon><Plus /></el-icon>
          新增设备
        </el-button>
        <el-button @click="loadDevices">
          <el-icon><Refresh /></el-icon>
          刷新
        </el-button>
      </template>
    </PageHeader>

    <!-- 设备统计 -->
    <el-row :gutter="20" class="stats-row">
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
              <el-icon class="success-icon"><CircleCheck /></el-icon>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="hover">
          <el-statistic title="离线设备" :value="(stats?.total || 0) - (stats?.online || 0)">
            <template #prefix>
              <el-icon class="danger-icon"><CircleClose /></el-icon>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
    </el-row>

    <!-- 设备列表 -->
    <el-card shadow="never" v-loading="loading">
      <template #header>
        <SectionHeader :icon="List" title="设备列表" />
      </template>

      <el-table :data="devices" stripe>
        <el-table-column prop="device_id" label="设备 ID" width="120" />
        <el-table-column prop="name" label="设备名称" />
        <el-table-column label="类型" width="100">
          <template #default="{ row }">
            <el-tag size="small">{{ formatDeviceType(row.device_type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="可用" width="70">
          <template #default="{ row }">
            <el-switch
              v-model="row.is_enabled"
              @change="(val: boolean) => toggleEnabled(row, val)"
              class="enabled-switch"
            />
          </template>
        </el-table-column>
        <el-table-column label="规格" min-width="190">
          <template #default="{ row }">
            <span>{{ formatRenderProfile(row.render_profile) }}</span>
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
        <el-table-column label="最后请求" width="180">
          <template #default="{ row }">
            <span class="last-request-time">{{ formatTime(row.last_seen) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }">
            <el-button link @click="viewDevice(row.device_id)" class="action-link">
              详情
            </el-button>
            <el-button link type="danger" @click="deleteDevice(row)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination-wrapper">
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
          <el-select v-model="createForm.device_type" placeholder="选择设备类型" class="full-width">
            <el-option label="嵌入式" value="embedded" />
            <el-option label="移动端" value="mobile" />
            <el-option label="Web" value="web" />
            <el-option label="离线程序" value="offline" />
            <el-option label="服务" value="service" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="createForm.device_type === 'embedded'" label="渲染规格">
          <el-select v-model="createForm.render_profile" placeholder="选择嵌入式规格" class="full-width">
            <el-option
              v-for="profile in renderProfiles"
              :key="profile.name"
              :label="profile.display_name"
              :value="profile.name"
            />
          </el-select>
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
        class="api-key-alert"
      />
      <el-descriptions :column="1" border>
        <el-descriptions-item label="设备 ID">{{ createdDevice?.device_id }}</el-descriptions-item>
        <el-descriptions-item label="设备名称">{{ createdDevice?.name }}</el-descriptions-item>
        <el-descriptions-item label="API Key">
          <div class="api-key-row">
            <el-input
              :model-value="createdDevice?.api_key"
              type="password"
              show-password
              readonly
              class="flex-1"
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
          <div class="detail-row">
            <el-input
              v-model="currentDevice.api_key"
              type="password"
              show-password
              readonly
              class="flex-1"
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
        <el-descriptions-item label="设备类型">{{ formatDeviceType(currentDevice.device_type) }}</el-descriptions-item>
        <el-descriptions-item label="渲染规格">
          <div class="render-profile-row">
            <el-select
              v-if="currentDevice.device_type === 'embedded'"
              v-model="renderProfileDraft"
              placeholder="选择渲染规格"
              class="render-profile-select"
            >
              <el-option
                v-for="profile in renderProfiles"
                :key="profile.name"
                :label="profile.display_name"
                :value="profile.name"
              />
            </el-select>
            <span v-else>{{ formatRenderProfile(currentDevice.render_profile) }}</span>
            <el-button
              v-if="currentDevice.device_type === 'embedded'"
              type="primary"
              @click="saveRenderProfile"
            >
              保存规格
            </el-button>
          </div>
        </el-descriptions-item>
        <el-descriptions-item label="IP 地址">{{ currentDevice.ip_address || '-' }}</el-descriptions-item>
        <el-descriptions-item label="最后请求">{{ formatTime(currentDevice.last_seen) }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(currentDevice.created_at) }}</el-descriptions-item>
      </el-descriptions>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { deviceApi, type CreateDeviceRequest, type CreateDeviceResponse } from '@/api/device'
import { dailyDisplayApi, type RenderProfileOption } from '@/api/display'
import type { Device, DeviceStats } from '@/types/device'
import dayjs from 'dayjs'
import { Monitor, CircleCheck, CircleClose, List, Refresh, Plus, CopyDocument } from '@element-plus/icons-vue'

const devices = ref<Device[]>([])
const stats = ref<DeviceStats | null>(null)
const renderProfiles = ref<RenderProfileOption[]>([])
const loading = ref(false)
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const detailVisible = ref(false)
const currentDevice = ref<Device | null>(null)
const renderProfileDraft = ref('')

// 创建设备相关
const createDialogVisible = ref(false)
const creating = ref(false)
const createFormRef = ref()
const createForm = reactive<CreateDeviceRequest>({
  name: '',
  device_type: 'embedded',
  description: '',
  render_profile: ''
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

const loadRenderProfiles = async () => {
  try {
    renderProfiles.value = await dailyDisplayApi.getRenderProfiles()
    if (!createForm.render_profile) {
      const defaultProfile = renderProfiles.value.find((profile) => profile.default_for_device)
      createForm.render_profile = defaultProfile?.name || renderProfiles.value[0]?.name || ''
    }
  } catch (error) {
    console.error('Failed to load render profiles:', error)
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
  createForm.device_type = 'embedded'
  createForm.description = ''
  createForm.render_profile = renderProfiles.value.find((profile) => profile.default_for_device)?.name || renderProfiles.value[0]?.name || ''
  createDialogVisible.value = true
}

// 格式化设备类型显示
const formatDeviceType = (type?: string) => {
  const typeMap: Record<string, string> = {
    embedded: '嵌入式',
    mobile: '移动端',
    web: 'Web',
    offline: '离线程序',
    service: '服务'
  }
  return typeMap[type || ''] || type || '-'
}

const formatRenderProfile = (profileName?: string) => {
  if (!profileName) return '-'
  return renderProfiles.value.find((profile) => profile.name === profileName)?.display_name || profileName
}

// 创建设备
const createDevice = async () => {
  if (!createForm.name) {
    ElMessage.warning('请填写设备名称')
    return
  }

  const payload: CreateDeviceRequest = {
    ...createForm,
    render_profile: createForm.device_type === 'embedded' ? createForm.render_profile : undefined
  }

  creating.value = true
  try {
    const res = await deviceApi.create(payload)
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
const toggleEnabled = async (row: Device, enabled: boolean) => {
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
const deleteDevice = async (row: Device) => {
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
    renderProfileDraft.value = currentDevice.value?.render_profile || ''
    detailVisible.value = true
  } catch (error: any) {
    ElMessage.error(error.message || '加载设备详情失败')
  }
}

const saveRenderProfile = async () => {
  if (!currentDevice.value?.id || !renderProfileDraft.value) return
  try {
    await deviceApi.updateRenderProfile(currentDevice.value.id, renderProfileDraft.value)
    currentDevice.value.render_profile = renderProfileDraft.value
    const target = devices.value.find((item) => item.id === currentDevice.value?.id)
    if (target) target.render_profile = renderProfileDraft.value
    ElMessage.success('渲染规格已更新')
  } catch (error: any) {
    ElMessage.error(error.message || '更新渲染规格失败')
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
  await loadRenderProfiles()
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

.last-request-time {
  white-space: nowrap;
}
.stats-row {
  margin-bottom: 20px;
}

.success-icon {
  color: #67c23a;
}

.danger-icon {
  color: #f56c6c;
}

.enabled-switch {
  --el-switch-on-color: #67c23a;
}

.action-link {
  color: var(--color-primary);
}

.pagination-wrapper {
  margin-top: 20px;
  text-align: center;
}

.api-key-alert {
  margin-bottom: 20px;
}

.render-profile-row {
  display: flex;
  gap: 12px;
  align-items: center;
}

.render-profile-select {
  width: 320px;
}

.full-width {
  width: 100%;
}

.api-key-row {
  display: flex;
  gap: 12px;
}

.detail-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.flex-1 {
  flex: 1;
}
</style>
