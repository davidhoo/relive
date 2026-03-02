<template>
  <div class="config-page">
    <!-- Scan Paths Card -->
    <el-card shadow="never" class="scan-paths-card">
      <template #header>
        <div class="card-header">
          <div>
            <el-icon class="header-icon"><FolderOpened /></el-icon>
            <span class="header-title">扫描路径配置</span>
          </div>
          <el-button type="primary" @click="handleAddPath">
            <el-icon><Plus /></el-icon>
            添加路径
          </el-button>
        </div>
      </template>

      <el-empty v-if="!scanPaths.length && !loading" description="暂无扫描路径配置">
        <el-button type="primary" @click="handleAddPath">添加第一个路径</el-button>
      </el-empty>

      <div v-else class="paths-list" v-loading="loading">
        <div
          v-for="path in scanPaths"
          :key="path.id"
          class="path-item"
          :class="{ disabled: !path.enabled }"
        >
          <div class="path-info">
            <div class="path-header">
              <el-checkbox v-model="path.enabled" @change="handleToggleEnabled(path)">
                {{ path.name }}
              </el-checkbox>
              <el-tag v-if="path.is_default" type="success" size="small">默认</el-tag>
            </div>
            <div class="path-location">{{ path.path }}</div>
            <div class="path-meta">
              <span v-if="path.last_scanned_at">
                <el-icon><Clock /></el-icon>
                上次扫描: {{ formatTime(path.last_scanned_at) }}
              </span>
              <span v-else class="never-scanned">从未扫描</span>
            </div>
          </div>
          <div class="path-actions">
            <el-button
              v-if="!path.is_default"
              link
              @click="handleSetDefault(path)"
              style="color: var(--color-primary)"
            >
              设为默认
            </el-button>
            <el-button link @click="handleEditPath(path)" style="color: var(--color-primary)">
              编辑
            </el-button>
            <el-button link @click="handleDeletePath(path.id)" style="color: var(--color-error)">
              删除
            </el-button>
          </div>
        </div>
      </div>
    </el-card>

    <!-- Geocode Configuration Card -->
    <el-card shadow="never" class="geocode-card">
      <template #header>
        <div class="card-header">
          <div>
            <el-icon class="header-icon"><Location /></el-icon>
            <span class="header-title">GPS 逆地理编码配置</span>
          </div>
          <el-button type="primary" @click="handleSaveGeocodeConfig" :loading="savingGeocode">
            <el-icon><Check /></el-icon>
            保存配置
          </el-button>
        </div>
      </template>

      <div v-loading="loadingGeocode">
        <el-form :model="geocodeConfig" label-width="140px" class="geocode-form">
          <!-- Provider Selection -->
          <el-form-item label="主要提供商">
            <el-select v-model="geocodeConfig.provider" placeholder="选择主要提供商" style="width: 100%">
              <el-option value="offline" label="离线数据库 (Offline)">
                <div class="provider-option">
                  <span>离线数据库 (Offline)</span>
                  <el-tag size="small" type="success">最快</el-tag>
                </div>
              </el-option>
              <el-option value="amap" label="高德地图 (AMap)">
                <div class="provider-option">
                  <span>高德地图 (AMap)</span>
                  <el-tag size="small">中国优选</el-tag>
                </div>
              </el-option>
              <el-option value="nominatim" label="OpenStreetMap (Nominatim)">
                <div class="provider-option">
                  <span>OpenStreetMap (Nominatim)</span>
                  <el-tag size="small" type="info">全球覆盖</el-tag>
                </div>
              </el-option>
            </el-select>
            <div class="form-hint">
              当前使用的地理编码服务提供商，优先级最高
            </div>
          </el-form-item>

          <!-- Fallback Provider -->
          <el-form-item label="备用提供商">
            <el-select v-model="geocodeConfig.fallback" placeholder="选择备用提供商" style="width: 100%">
              <el-option value="" label="无备用"></el-option>
              <el-option value="offline" label="离线数据库 (Offline)"></el-option>
              <el-option value="amap" label="高德地图 (AMap)"></el-option>
              <el-option value="nominatim" label="OpenStreetMap (Nominatim)"></el-option>
            </el-select>
            <div class="form-hint">
              主提供商失败时自动切换到备用提供商
            </div>
          </el-form-item>

          <!-- Cache Settings -->
          <el-divider content-position="left">缓存设置</el-divider>

          <el-form-item label="启用缓存">
            <el-switch v-model="geocodeConfig.cache_enabled" />
            <div class="form-hint">
              缓存可大幅提升性能，相同坐标不会重复查询
            </div>
          </el-form-item>

          <el-form-item label="缓存有效期" v-if="geocodeConfig.cache_enabled">
            <el-input-number
              v-model="geocodeConfig.cache_ttl"
              :min="3600"
              :max="604800"
              :step="3600"
              style="width: 200px"
            />
            <span style="margin-left: 12px">秒 ({{ Math.floor(geocodeConfig.cache_ttl / 3600) }} 小时)</span>
            <div class="form-hint">
              缓存数据保留时长，默认 24 小时
            </div>
          </el-form-item>

          <!-- AMap Configuration -->
          <el-divider content-position="left">
            <el-icon><Location /></el-icon>
            高德地图 (AMap) 配置
          </el-divider>

          <el-form-item label="API Key">
            <el-input
              v-model="geocodeConfig.amap_api_key"
              placeholder="请输入高德地图 API Key"
              type="password"
              show-password
            >
              <template #append>
                <el-button @click="openAmapDocs">
                  <el-icon><Link /></el-icon>
                  申请
                </el-button>
              </template>
            </el-input>
            <div class="form-hint">
              访问 <a href="https://lbs.amap.com/" target="_blank">https://lbs.amap.com/</a> 申请 API Key
            </div>
          </el-form-item>

          <el-form-item label="超时时间">
            <el-input-number
              v-model="geocodeConfig.amap_timeout"
              :min="5"
              :max="60"
              style="width: 150px"
            />
            <span style="margin-left: 12px">秒</span>
          </el-form-item>

          <!-- Nominatim Configuration -->
          <el-divider content-position="left">
            <el-icon><Location /></el-icon>
            Nominatim (OpenStreetMap) 配置
          </el-divider>

          <el-form-item label="服务端点">
            <el-input
              v-model="geocodeConfig.nominatim_endpoint"
              placeholder="https://nominatim.openstreetmap.org/reverse"
            />
            <div class="form-hint">
              默认使用官方服务，也可使用自建 Nominatim 服务
            </div>
          </el-form-item>

          <el-form-item label="超时时间">
            <el-input-number
              v-model="geocodeConfig.nominatim_timeout"
              :min="5"
              :max="60"
              style="width: 150px"
            />
            <span style="margin-left: 12px">秒</span>
          </el-form-item>

          <!-- Offline Configuration -->
          <el-divider content-position="left">
            <el-icon><Location /></el-icon>
            离线数据库配置
          </el-divider>

          <el-form-item label="最大搜索距离">
            <el-input-number
              v-model="geocodeConfig.offline_max_distance"
              :min="10"
              :max="500"
              :step="10"
              style="width: 150px"
            />
            <span style="margin-left: 12px">公里</span>
            <div class="form-hint">
              超过此距离的坐标将无法匹配到城市
            </div>
          </el-form-item>

          <el-alert
            title="离线数据库说明"
            type="info"
            :closable="false"
            style="margin-top: 16px"
          >
            <template #default>
              <div>离线提供商需要导入城市数据库才能使用。如未导入，系统会自动使用备用提供商。</div>
              <div style="margin-top: 8px">
                数据源：<a href="https://download.geonames.org/export/dump/" target="_blank">GeoNames</a>
                (推荐使用 <a href="https://download.geonames.org/export/dump/cities500.zip" target="_blank">cities500.zip</a> - 覆盖面更广)
              </div>
            </template>
          </el-alert>
        </el-form>
      </div>
    </el-card>

    <!-- Add/Edit Path Dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑扫描路径' : '添加扫描路径'"
      width="600px"
    >
      <el-form :model="pathForm" label-width="100px" ref="formRef">
        <el-form-item label="名称" required>
          <el-input v-model="pathForm.name" placeholder="例如: iPhone 2025-11" />
        </el-form-item>
        <el-form-item label="路径" required>
          <el-input v-model="pathForm.path" placeholder="/path/to/photos">
            <template #append>
              <el-button @click="handleValidatePath" :loading="validating">验证</el-button>
            </template>
          </el-input>
          <div v-if="validationResult" :class="['validation-result', validationResult.valid ? 'valid' : 'invalid']">
            <el-icon v-if="validationResult.valid"><CircleCheck /></el-icon>
            <el-icon v-else><CircleClose /></el-icon>
            <span>{{ validationResult.valid ? '路径有效' : validationResult.error }}</span>
          </div>
        </el-form-item>
        <el-form-item label="设置">
          <el-checkbox v-model="pathForm.is_default">设为默认路径</el-checkbox>
          <el-checkbox v-model="pathForm.enabled">启用此路径</el-checkbox>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleSavePath" :loading="saving">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { configApi, type ScanPathConfig, type GeocodeConfig } from '@/api/config'
import dayjs from 'dayjs'
import { v4 as uuidv4 } from 'uuid'

// Scan paths state
const scanPaths = ref<ScanPathConfig[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const isEdit = ref(false)
const saving = ref(false)
const validating = ref(false)
const validationResult = ref<{ valid: boolean; error?: string } | null>(null)

const pathForm = ref<Partial<ScanPathConfig>>({
  name: '',
  path: '',
  is_default: false,
  enabled: true,
})

// Geocode configuration state
const geocodeConfig = ref<GeocodeConfig>({
  provider: 'offline',
  fallback: 'nominatim',
  cache_enabled: true,
  cache_ttl: 86400,
  amap_api_key: '',
  amap_timeout: 10,
  nominatim_endpoint: 'https://nominatim.openstreetmap.org/reverse',
  nominatim_timeout: 10,
  offline_max_distance: 100
})
const loadingGeocode = ref(false)
const savingGeocode = ref(false)

const formatTime = (time?: string) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

// Scan paths functions
const loadScanPaths = async () => {
  loading.value = true
  try {
    const config = await configApi.getScanPaths()
    scanPaths.value = config.paths || []
  } catch (error: any) {
    ElMessage.error('加载扫描路径失败')
  } finally {
    loading.value = false
  }
}

const handleAddPath = () => {
  isEdit.value = false
  pathForm.value = {
    name: '',
    path: '',
    is_default: scanPaths.value.length === 0, // First path is default
    enabled: true,
  }
  validationResult.value = null
  dialogVisible.value = true
}

const handleEditPath = (path: ScanPathConfig) => {
  isEdit.value = true
  pathForm.value = { ...path }
  validationResult.value = null
  dialogVisible.value = true
}

const handleValidatePath = async () => {
  if (!pathForm.value.path) {
    ElMessage.warning('请输入路径')
    return
  }

  validating.value = true
  try {
    const result = await configApi.validatePath(pathForm.value.path)
    validationResult.value = result
    if (result.valid) {
      ElMessage.success('路径验证成功')
    }
  } catch (error: any) {
    ElMessage.error('路径验证失败')
  } finally {
    validating.value = false
  }
}

const handleSavePath = async () => {
  if (!pathForm.value.name || !pathForm.value.path) {
    ElMessage.warning('请填写完整信息')
    return
  }

  saving.value = true
  try {
    const newPaths = [...scanPaths.value]

    if (isEdit.value) {
      // Update existing
      const index = newPaths.findIndex(p => p.id === pathForm.value.id)
      if (index !== -1) {
        // If setting as default, unset others
        if (pathForm.value.is_default) {
          newPaths.forEach(p => p.is_default = false)
        }
        newPaths[index] = pathForm.value as ScanPathConfig
      }
    } else {
      // Add new
      const newPath: ScanPathConfig = {
        id: uuidv4(),
        name: pathForm.value.name!,
        path: pathForm.value.path!,
        is_default: pathForm.value.is_default || false,
        enabled: pathForm.value.enabled ?? true,
        created_at: new Date().toISOString(),
      }

      // If setting as default, unset others
      if (newPath.is_default) {
        newPaths.forEach(p => p.is_default = false)
      }

      newPaths.push(newPath)
    }

    await configApi.updateScanPaths({ paths: newPaths })
    ElMessage.success('保存成功')
    dialogVisible.value = false
    await loadScanPaths()
  } catch (error: any) {
    ElMessage.error('保存失败')
  } finally {
    saving.value = false
  }
}

const handleDeletePath = async (pathId: string) => {
  try {
    await ElMessageBox.confirm('确定要删除此路径吗？', '确认删除', {
      type: 'warning',
    })

    const newPaths = scanPaths.value.filter(p => p.id !== pathId)
    await configApi.updateScanPaths({ paths: newPaths })
    ElMessage.success('删除成功')
    await loadScanPaths()
  } catch {
    // User cancelled
  }
}

const handleSetDefault = async (path: ScanPathConfig) => {
  const newPaths = scanPaths.value.map(p => ({
    ...p,
    is_default: p.id === path.id,
  }))

  try {
    await configApi.updateScanPaths({ paths: newPaths })
    ElMessage.success('已设为默认路径')
    await loadScanPaths()
  } catch (error: any) {
    ElMessage.error('操作失败')
  }
}

const handleToggleEnabled = async (path: ScanPathConfig) => {
  try {
    await configApi.updateScanPaths({ paths: scanPaths.value })
    ElMessage.success(path.enabled ? '已启用' : '已禁用')
  } catch (error: any) {
    ElMessage.error('操作失败')
    // Revert
    path.enabled = !path.enabled
  }
}

// Geocode configuration functions
const loadGeocodeConfig = async () => {
  loadingGeocode.value = true
  try {
    const config = await configApi.getGeocodeConfig()
    geocodeConfig.value = config
  } catch (error: any) {
    ElMessage.error('加载地理编码配置失败')
  } finally {
    loadingGeocode.value = false
  }
}

const handleSaveGeocodeConfig = async () => {
  savingGeocode.value = true
  try {
    await configApi.updateGeocodeConfig(geocodeConfig.value)
    ElMessage.success('地理编码配置保存成功')
  } catch (error: any) {
    ElMessage.error('保存失败: ' + (error.message || '未知错误'))
  } finally {
    savingGeocode.value = false
  }
}

const openAmapDocs = () => {
  window.open('https://lbs.amap.com/', '_blank')
}

onMounted(() => {
  loadScanPaths()
  loadGeocodeConfig()
})
</script>

<style scoped>
.config-page {
  padding: var(--spacing-xl);
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.scan-paths-card,
.geocode-card {
  max-width: 1200px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-icon {
  margin-right: 8px;
  font-size: 18px;
  color: var(--color-primary);
}

.header-title {
  font-size: 16px;
  font-weight: 600;
}

.paths-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.path-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  transition: all 0.3s;
}

.path-item:hover {
  border-color: var(--color-primary);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.path-item.disabled {
  opacity: 0.6;
}

.path-info {
  flex: 1;
}

.path-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;
  font-weight: 600;
}

.path-location {
  color: var(--color-text-secondary);
  font-family: monospace;
  margin-bottom: 4px;
}

.path-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--color-text-tertiary);
}

.never-scanned {
  color: var(--color-warning);
}

.path-actions {
  display: flex;
  gap: 8px;
}

.validation-result {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  font-size: 14px;
}

.validation-result.valid {
  color: var(--color-success);
}

.validation-result.invalid {
  color: var(--color-error);
}

/* Geocode configuration styles */
.geocode-form {
  max-width: 800px;
}

.form-hint {
  font-size: 13px;
  color: var(--color-text-tertiary);
  margin-top: 4px;
  line-height: 1.5;
}

.form-hint a {
  color: var(--color-primary);
  text-decoration: none;
}

.form-hint a:hover {
  text-decoration: underline;
}

.provider-option {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}

:deep(.el-divider__text) {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

:deep(.el-alert) {
  line-height: 1.8;
}
</style>
