<template>
  <div class="devices-page">
    <!-- 设备统计 -->
    <el-row :gutter="20" style="margin-bottom: 20px">
      <el-col :span="8">
        <el-card shadow="hover">
          <el-statistic title="总设备数" :value="stats?.total_devices || 0">
            <template #prefix>
              <el-icon><Monitor /></el-icon>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="hover">
          <el-statistic title="在线设备" :value="stats?.online_devices || 0">
            <template #prefix>
              <el-icon style="color: #67c23a"><CircleCheck /></el-icon>
            </template>
          </el-statistic>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="hover">
          <el-statistic title="离线设备" :value="stats?.offline_devices || 0">
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
          <el-button type="primary" @click="loadDevices">
            <el-icon><Refresh /></el-icon>
            刷新
          </el-button>
        </div>
      </template>

      <el-table :data="devices" stripe>
        <el-table-column prop="device_id" label="设备 ID" width="180" />
        <el-table-column prop="device_name" label="设备名称" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.is_online ? 'success' : 'info'">
              {{ row.is_online ? '在线' : '离线' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="ip_address" label="IP 地址" width="150" />
        <el-table-column prop="firmware_version" label="固件版本" width="120" />
        <el-table-column label="照片数" width="100">
          <template #default="{ row }">
            {{ row.photo_count || 0 }}
          </template>
        </el-table-column>
        <el-table-column label="最后心跳" width="180">
          <template #default="{ row }">
            {{ formatTime(row.last_heartbeat) }}
          </template>
        </el-table-column>
        <el-table-column label="注册时间" width="180">
          <template #default="{ row }">
            {{ formatTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button link @click="viewDevice(row.device_id)" style="color: var(--color-primary); font-weight: 500;">
              详情
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

    <!-- 设备详情对话框 -->
    <el-dialog v-model="detailVisible" title="设备详情" width="600px">
      <el-descriptions :column="1" border v-if="currentDevice">
        <el-descriptions-item label="设备 ID">{{ currentDevice.device_id }}</el-descriptions-item>
        <el-descriptions-item label="设备名称">{{ currentDevice.device_name }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="currentDevice.is_online ? 'success' : 'info'">
            {{ currentDevice.is_online ? '在线' : '离线' }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="IP 地址">{{ currentDevice.ip_address }}</el-descriptions-item>
        <el-descriptions-item label="固件版本">{{ currentDevice.firmware_version }}</el-descriptions-item>
        <el-descriptions-item label="照片数量">{{ currentDevice.photo_count || 0 }}</el-descriptions-item>
        <el-descriptions-item label="最后心跳">{{ formatTime(currentDevice.last_heartbeat) }}</el-descriptions-item>
        <el-descriptions-item label="注册时间">{{ formatTime(currentDevice.created_at) }}</el-descriptions-item>
        <el-descriptions-item label="更新时间">{{ formatTime(currentDevice.updated_at) }}</el-descriptions-item>
      </el-descriptions>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { deviceApi } from '@/api/device'
import type { ESP32Device, DeviceStats } from '@/types/device'
import dayjs from 'dayjs'

const devices = ref<ESP32Device[]>([])
const stats = ref<DeviceStats | null>(null)
const loading = ref(false)
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const detailVisible = ref(false)
const currentDevice = ref<ESP32Device | null>(null)

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
    devices.value = res.data?.items || []
    total.value = res.data?.total || 0
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
    stats.value = res.data || null
  } catch (error) {
    console.error('Failed to load device stats:', error)
  }
}

// 查看设备详情
const viewDevice = async (deviceId: string) => {
  try {
    const res = await deviceApi.getById(deviceId)
    currentDevice.value = res.data || null
    detailVisible.value = true
  } catch (error: any) {
    ElMessage.error(error.message || '加载设备详情失败')
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
</style>
