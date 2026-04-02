<template>
  <div class="system-page">
    <PageHeader title="系统信息" subtitle="查看系统运行状态和详细信息" :gradient="true" />

    <!-- 系统健康状态 -->
    <div class="health-section animate-fade-in">
      <div class="health-card" :class="healthCardClass">
        <div class="health-icon">
          <el-icon v-if="health?.status === 'healthy'"><SuccessFilled /></el-icon>
          <el-icon v-else><WarningFilled /></el-icon>
        </div>
        <div class="health-content">
          <div class="health-status">
            {{ health?.status === 'healthy' ? '系统运行正常' : '系统异常' }}
          </div>
          <div class="health-time">
            最后检查: {{ formatTime(health?.timestamp) }}
          </div>
        </div>
        <div class="health-badge">
          <el-tag :type="health?.status === 'healthy' ? 'success' : 'danger'" size="large" effect="dark">
            {{ health?.status?.toUpperCase() || 'UNKNOWN' }}
          </el-tag>
        </div>
      </div>
    </div>

    <!-- 系统信息卡片网格 -->
    <el-row :gutter="20" class="info-grid">
      <!-- 系统版本 -->
      <el-col :xs="24" :sm="12" :md="8" class="animate-fade-in animate-delay-1">
        <div class="info-card">
          <div class="info-icon stat-icon-primary">
            <el-icon><Platform /></el-icon>
          </div>
          <div class="info-content">
            <div class="info-label">系统版本</div>
            <div class="info-value">{{ health?.version || 'v1.0.0' }}</div>
          </div>
        </div>
      </el-col>

      <!-- Go 版本 -->
      <el-col :xs="24" :sm="12" :md="8" class="animate-fade-in animate-delay-2">
        <div class="info-card">
          <div class="info-icon stat-icon-success">
            <el-icon><DocumentCopy /></el-icon>
          </div>
          <div class="info-content">
            <div class="info-label">Go 版本</div>
            <div class="info-value">{{ stats?.go_version || '-' }}</div>
          </div>
        </div>
      </el-col>

      <!-- 运行时长 -->
      <el-col :xs="24" :sm="12" :md="8" class="animate-fade-in animate-delay-3">
        <div class="info-card">
          <div class="info-icon stat-icon-info">
            <el-icon><Timer /></el-icon>
          </div>
          <div class="info-content">
            <div class="info-label">运行时长</div>
            <div class="info-value uptime">{{ formatDuration(stats?.uptime) }}</div>
          </div>
        </div>
      </el-col>
    </el-row>

    <!-- 数据统计 -->
    <div class="section-title animate-fade-in">
      <el-icon><DataAnalysis /></el-icon>
      <span>数据统计</span>
    </div>

    <el-row :gutter="20" class="stats-grid">
      <!-- 照片总数 -->
      <el-col :xs="12" :sm="6" class="animate-fade-in">
        <div class="stat-mini-card">
          <div class="stat-mini-label">照片总数</div>
          <div class="stat-mini-value">{{ stats?.total_photos || 0 }}</div>
        </div>
      </el-col>

      <!-- 已分析照片 -->
      <el-col :xs="12" :sm="6" class="animate-fade-in animate-delay-1">
        <div class="stat-mini-card">
          <div class="stat-mini-label">已分析</div>
          <div class="stat-mini-value success">{{ stats?.analyzed_photos || 0 }}</div>
        </div>
      </el-col>

      <!-- 设备总数 -->
      <el-col :xs="12" :sm="6" class="animate-fade-in animate-delay-2">
        <div class="stat-mini-card">
          <div class="stat-mini-label">设备总数</div>
          <div class="stat-mini-value">{{ stats?.total_devices || 0 }}</div>
        </div>
      </el-col>

      <!-- 在线设备 -->
      <el-col :xs="12" :sm="6" class="animate-fade-in animate-delay-3">
        <div class="stat-mini-card">
          <div class="stat-mini-label">在线设备</div>
          <div class="stat-mini-value success">{{ stats?.online_devices || 0 }}</div>
        </div>
      </el-col>
    </el-row>

    <!-- 存储信息 -->
    <div class="section-title animate-fade-in">
      <el-icon><FolderOpened /></el-icon>
      <span>存储信息</span>
    </div>

    <el-row :gutter="20" class="storage-grid">
      <!-- 照片库总大小 -->
      <el-col :xs="24" :sm="12" class="animate-fade-in">
        <div class="storage-card">
          <div class="storage-header">
            <div class="storage-icon">
              <el-icon><PictureFilled /></el-icon>
            </div>
            <div class="storage-title-group">
              <div class="storage-title">照片库总大小</div>
              <el-tooltip content="系统已索引照片的总大小，不代表真实磁盘占用" placement="top">
                <el-icon class="storage-tip"><InfoFilled /></el-icon>
              </el-tooltip>
            </div>
          </div>
          <div class="storage-size">{{ formatSize(stats?.storage_size) }}</div>
          <div class="storage-footer">
            <div class="storage-label">总照片数</div>
            <div class="storage-count">{{ stats?.total_photos || 0 }} 张</div>
          </div>
        </div>
      </el-col>

      <!-- 数据库大小 -->
      <el-col :xs="24" :sm="12" class="animate-fade-in animate-delay-1">
        <div class="storage-card">
          <div class="storage-header">
            <div class="storage-icon">
              <el-icon><Collection /></el-icon>
            </div>
            <div class="storage-title">数据库大小</div>
          </div>
          <div class="storage-size">{{ formatSize(stats?.database_size) }}</div>
          <div class="storage-footer">
            <div class="storage-label">最后修改时间</div>
            <div class="storage-count">{{ formatTime(stats?.database_updated_at) }}</div>
          </div>
        </div>
      </el-col>
    </el-row>

    <!-- 危险操作区域 -->
    <div class="section-title animate-fade-in">
      <el-icon><Warning /></el-icon>
      <span>危险操作</span>
    </div>

    <el-row :gutter="20" class="danger-grid">
      <el-col :xs="24" :sm="12" class="animate-fade-in">
        <div class="danger-card">
          <div class="danger-header">
            <div class="danger-icon">
              <el-icon><DeleteFilled /></el-icon>
            </div>
            <div class="danger-title">
              <h3>系统还原</h3>
              <p class="danger-desc">清除所有数据，恢复到初始状态</p>
            </div>
          </div>
          <div class="danger-content">
            <p class="danger-warning">
              <el-icon><WarningFilled /></el-icon>
              此操作将永久删除以下数据，无法恢复：
            </p>
            <ul class="danger-list">
              <li>所有照片记录和元数据</li>
              <li>所有设备信息</li>
              <li>所有展示历史记录</li>
              <li>所有应用配置</li>
              <li>所有 API Key</li>
              <li>所有缩略图文件</li>
              <li>所有缓存数据</li>
              <li><strong>管理员密码将重置为 admin/admin</strong></li>
              <li><strong>服务会退出并重启，若未自动重启请手动启动</strong></li>
            </ul>
          </div>
          <div class="danger-footer">
            <el-button
              type="danger"
              size="large"
              @click="handleResetClick"
              class="reset-btn"
            >
              <el-icon><DeleteFilled /></el-icon>
              系统还原
            </el-button>
          </div>
        </div>
      </el-col>
    </el-row>

    <!-- 系统还原确认对话框 -->
    <el-dialog
      v-model="resetDialogVisible"
      title="系统还原确认"
      width="500px"
      :close-on-click-modal="false"
      class="reset-dialog"
    >
      <div class="reset-dialog-content">
        <div class="reset-warning-icon">
          <el-icon color="#f56c6c" :size="48"><WarningFilled /></el-icon>
        </div>
        <h3 class="reset-title">确定要还原系统吗？</h3>
        <p class="reset-desc">
          此操作将<strong>永久删除</strong>所有数据，包括照片记录、设备信息、展示历史、API Key、缩略图和缓存。
          删除的数据<strong>无法恢复</strong>！<br/>
          管理员密码将重置为 <strong>admin/admin</strong>。系统会在还原后退出并重启，若未自动重启，请手动启动服务后重新登录。
        </p>
        <div class="reset-confirm-input">
          <p class="reset-hint">
            请输入 <strong>RESET</strong> 以确认操作：
          </p>
          <el-input
            v-model="resetConfirmText"
            placeholder="请输入 RESET"
            size="large"
            input-class="reset-confirm-input-field"
          />
        </div>
      </div>
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="handleResetCancel" size="large">取消</el-button>
          <el-button
            type="danger"
            size="large"
            :loading="resetLoading"
            :disabled="resetConfirmText !== 'RESET'"
            @click="handleResetConfirm"
          >
            <el-icon><DeleteFilled /></el-icon>
            确认还原
          </el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  SuccessFilled,
  WarningFilled,
  Platform,
  DocumentCopy,
  InfoFilled,
  Timer,
  DataAnalysis,
  FolderOpened,
  PictureFilled,
  Collection,
  DeleteFilled,
  Warning
} from '@element-plus/icons-vue'
import { useSystemStore } from '@/stores/system'
import { useUserStore } from '@/stores/user'
import { systemApi } from '@/api/system'
import { ElMessage } from 'element-plus'
import dayjs from 'dayjs'
import duration from 'dayjs/plugin/duration'

dayjs.extend(duration)

const systemStore = useSystemStore()
const userStore = useUserStore()
const router = useRouter()

// 系统还原相关
const resetDialogVisible = ref(false)
const resetConfirmText = ref('')
const resetLoading = ref(false)

// 打开还原确认对话框
const handleResetClick = () => {
  resetConfirmText.value = ''
  resetDialogVisible.value = true
}

// 执行系统还原
const handleResetConfirm = async () => {
  if (resetConfirmText.value !== 'RESET') {
    ElMessage.error('请输入正确的确认文本 "RESET"')
    return
  }

  resetLoading.value = true
  try {
    const response = await systemApi.reset({ confirm_text: resetConfirmText.value })
    if (response.data.success) {
      ElMessage.success('已安排恢复出厂设置，服务即将退出；若未自动重启，请手动启动服务。')
      resetDialogVisible.value = false

      // 后端即将退出并重启，本地直接清掉登录态，避免 logout 请求撞上重启窗口。
      setTimeout(() => {
        userStore.clearUserState()
        router.push('/login')
      }, 1000)
    } else {
      ElMessage.error(response.data.message || '系统还原失败')
    }
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error?.message || '系统还原失败')
  } finally {
    resetLoading.value = false
  }
}

// 取消还原
const handleResetCancel = () => {
  resetDialogVisible.value = false
  resetConfirmText.value = ''
}

const health = computed(() => systemStore.health)
const stats = computed(() => systemStore.stats)

// 健康状态卡片样式
const healthCardClass = computed(() => {
  return health.value?.status === 'healthy' ? 'health-card-success' : 'health-card-error'
})

// 格式化时间
const formatTime = (time?: string | number) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

// 格式化时长
const formatDuration = (seconds?: number) => {
  if (!seconds) return '-'
  const d = dayjs.duration(seconds, 'seconds')
  const days = Math.floor(d.asDays())
  const hours = d.hours()
  const minutes = d.minutes()

  if (days > 0) {
    return `${days} 天 ${hours} 小时`
  }
  if (hours > 0) {
    return `${hours} 小时 ${minutes} 分钟`
  }
  return `${minutes} 分钟`
}

// 格式化文件大小
const formatSize = (size?: number) => {
  if (!size) return '-'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(2)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(2)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
}

onMounted(async () => {
  await systemStore.fetchHealth()
  await systemStore.fetchStats()
})
</script>

<style scoped>
/* ============ System 页面容器 ============ */
.system-page {
  padding: var(--spacing-xl);
  background: var(--color-bg-secondary);
  min-height: 100vh;
}

/* ============ 页面标题 ============ */
.page-header {
  margin-bottom: var(--spacing-2xl);
}

.page-title {
  font-size: var(--font-size-4xl);
  font-weight: var(--font-weight-bold);
  margin-bottom: var(--spacing-sm);
  line-height: 1.2;
}

.page-subtitle {
  font-size: var(--font-size-lg);
  color: var(--color-text-secondary);
}

/* ============ 健康状态卡片 ============ */
.health-section {
  margin-bottom: var(--spacing-2xl);
}

.health-card {
  display: flex;
  align-items: center;
  gap: var(--spacing-xl);
  padding: var(--spacing-2xl);
  border-radius: var(--radius-xl);
  background: var(--color-bg-primary);
  box-shadow: var(--shadow-lg);
  transition: all var(--transition-base);
  position: relative;
  overflow: hidden;
}

.health-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 4px;
  background: linear-gradient(135deg, var(--color-success) 0%, var(--color-success-light) 100%);
  transition: background var(--transition-base);
}

.health-card-error::before {
  background: linear-gradient(135deg, var(--color-error) 0%, var(--color-error-light) 100%);
}

.health-card:hover {
  box-shadow: var(--shadow-xl);
  transform: translateY(-2px);
}

.health-icon {
  width: 64px;
  height: 64px;
  border-radius: var(--radius-xl);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 32px;
  background: linear-gradient(135deg, var(--color-success) 0%, var(--color-success-light) 100%);
  color: white;
  flex-shrink: 0;
  box-shadow: var(--shadow-lg);
}

.health-card-error .health-icon {
  background: linear-gradient(135deg, var(--color-error) 0%, var(--color-error-light) 100%);
}

.health-content {
  flex: 1;
}

.health-status {
  font-size: var(--font-size-2xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
  margin-bottom: var(--spacing-xs);
}

.health-time {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
}

.health-badge {
  flex-shrink: 0;
}

/* ============ 信息卡片网格 ============ */
.info-grid {
  margin-bottom: var(--spacing-2xl);
}

.info-grid .el-col {
  margin-bottom: var(--spacing-md);
}

.info-card {
  display: flex;
  align-items: center;
  gap: var(--spacing-lg);
  padding: var(--spacing-xl);
  background: var(--color-bg-primary);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
  transition: all var(--transition-base);
  border: 1px solid var(--color-border);
}

.info-card:hover {
  box-shadow: var(--shadow-lg);
  transform: translateY(-4px);
  border-color: transparent;
}

.info-icon {
  width: 56px;
  height: 56px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 28px;
  flex-shrink: 0;
  transition: all var(--transition-base);
}

.info-card:hover .info-icon {
  transform: scale(1.1) rotate(5deg);
}

.info-content {
  flex: 1;
  min-width: 0;
}

.info-label {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
  margin-bottom: var(--spacing-xs);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: var(--font-weight-medium);
}

.info-value {
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.info-value.uptime {
  font-size: var(--font-size-lg);
}

/* ============ 分区标题 ============ */
.section-title {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin-bottom: var(--spacing-xl);
  padding-bottom: var(--spacing-md);
  border-bottom: 2px solid var(--color-border);
}

.section-title .el-icon {
  font-size: 24px;
  color: var(--color-primary);
}

/* ============ 迷你统计卡片 ============ */
.stats-grid {
  margin-bottom: var(--spacing-2xl);
}

.stats-grid .el-col {
  margin-bottom: var(--spacing-md);
}

.stat-mini-card {
  padding: var(--spacing-xl);
  background: var(--color-bg-primary);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-sm);
  transition: all var(--transition-base);
  text-align: center;
  border: 1px solid var(--color-border);
}

.stat-mini-card:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
  border-color: var(--color-primary);
}

.stat-mini-label {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
  margin-bottom: var(--spacing-sm);
  font-weight: var(--font-weight-medium);
}

.stat-mini-value {
  font-size: var(--font-size-3xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-text-primary);
  line-height: 1;
}

.stat-mini-value.success {
  background: linear-gradient(135deg, var(--color-success) 0%, var(--color-success-light) 100%);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

/* ============ 存储卡片 ============ */
.storage-grid {
  margin-bottom: var(--spacing-xl);
}

.storage-grid .el-col {
  margin-bottom: var(--spacing-md);
}

.storage-card {
  padding: var(--spacing-2xl);
  background: linear-gradient(135deg, var(--color-primary) 0%, var(--color-primary-light) 100%);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-lg);
  color: white;
  transition: all var(--transition-base);
}

.storage-card:hover {
  box-shadow: var(--shadow-2xl);
  transform: translateY(-4px);
}

.storage-header {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  margin-bottom: var(--spacing-xl);
}

.storage-icon {
  width: 48px;
  height: 48px;
  background: rgba(255, 255, 255, 0.2);
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
  transition: all var(--transition-base);
}

.storage-card:hover .storage-icon {
  background: rgba(255, 255, 255, 0.3);
  transform: scale(1.1);
}

.storage-title {
  font-size: var(--font-size-lg);
  font-weight: var(--font-weight-semibold);
}

.storage-title-group {
  display: flex;
  align-items: center;
  gap: var(--spacing-xs);
}

.storage-size {
  font-size: var(--font-size-4xl);
  font-weight: var(--font-weight-bold);
  margin-bottom: var(--spacing-lg);
  line-height: 1;
}

.storage-tip {
  font-size: 14px;
  opacity: 0.75;
  cursor: help;
}

.storage-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-top: var(--spacing-lg);
  border-top: 1px solid rgba(255, 255, 255, 0.2);
}

.storage-label {
  font-size: var(--font-size-sm);
  opacity: 0.8;
}

.storage-count {
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-semibold);
}

/* ============ 响应式设计 ============ */
@media (max-width: 1200px) {
  .system-page {
    padding: var(--spacing-lg);
  }

  .health-card {
    flex-wrap: wrap;
  }

  .health-badge {
    width: 100%;
    display: flex;
    justify-content: flex-end;
  }
}

@media (max-width: 768px) {
  .system-page {
    padding: var(--spacing-md);
  }

  .page-title {
    font-size: var(--font-size-2xl);
  }

  .health-card {
    padding: var(--spacing-lg);
  }

  .health-icon {
    width: 48px;
    height: 48px;
    font-size: 24px;
  }

  .health-status {
    font-size: var(--font-size-xl);
  }

  .info-card {
    padding: var(--spacing-lg);
  }

  .info-icon {
    width: 48px;
    height: 48px;
    font-size: 24px;
  }

  .info-value {
    font-size: var(--font-size-lg);
  }

  .stat-mini-value {
    font-size: var(--font-size-2xl);
  }

  .storage-size {
    font-size: var(--font-size-3xl);
  }

}

@media (max-width: 480px) {
  .health-card {
    gap: var(--spacing-md);
  }

  .section-title {
    font-size: var(--font-size-base);
  }

  .storage-footer {
    flex-direction: column;
    gap: var(--spacing-sm);
    align-items: flex-start;
  }
}

/* ============ 危险操作区域 ============ */
.danger-grid {
  margin-bottom: var(--spacing-2xl);
}

.danger-card {
  background: var(--color-bg-primary);
  border-radius: var(--radius-xl);
  border: 1px solid var(--color-error);
  box-shadow: var(--shadow-sm);
  overflow: hidden;
  transition: all var(--transition-base);
}

.danger-card:hover {
  box-shadow: var(--shadow-md);
  border-color: var(--color-error);
}

.danger-header {
  display: flex;
  align-items: center;
  gap: var(--spacing-lg);
  padding: var(--spacing-xl);
  background: linear-gradient(135deg, rgba(245, 108, 108, 0.1) 0%, rgba(245, 108, 108, 0.05) 100%);
  border-bottom: 1px solid rgba(245, 108, 108, 0.2);
}

.danger-icon {
  width: 56px;
  height: 56px;
  border-radius: var(--radius-lg);
  background: var(--color-error);
  color: white;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 28px;
  flex-shrink: 0;
}

.danger-title h3 {
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-semibold);
  color: var(--color-error);
  margin: 0 0 var(--spacing-xs) 0;
}

.danger-desc {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
  margin: 0;
}

.danger-content {
  padding: var(--spacing-xl);
}

.danger-warning {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-semibold);
  color: var(--color-error);
  margin: 0 0 var(--spacing-md) 0;
}

.danger-list {
  margin: 0;
  padding-left: var(--spacing-xl);
  color: var(--color-text-secondary);
  font-size: var(--font-size-sm);
}

.danger-list li {
  margin-bottom: var(--spacing-xs);
}

.danger-footer {
  padding: var(--spacing-lg) var(--spacing-xl);
  background: var(--color-bg-secondary);
  border-top: 1px solid var(--color-border);
  display: flex;
  justify-content: flex-end;
}

.reset-btn {
  font-weight: var(--font-weight-semibold);
}

/* ============ 系统还原对话框 ============ */
.reset-dialog-content {
  text-align: center;
  padding: var(--spacing-lg);
}

.reset-warning-icon {
  margin-bottom: var(--spacing-lg);
}

.reset-title {
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0 0 var(--spacing-md) 0;
}

.reset-desc {
  font-size: var(--font-size-base);
  color: var(--color-text-secondary);
  margin: 0 0 var(--spacing-xl) 0;
  line-height: 1.6;
}

.reset-desc strong {
  color: var(--color-error);
}

.reset-confirm-input {
  background: var(--color-bg-secondary);
  padding: var(--spacing-lg);
  border-radius: var(--radius-lg);
}

.reset-hint {
  font-size: var(--font-size-sm);
  color: var(--color-text-primary);
  margin: 0 0 var(--spacing-md) 0;
}

.reset-hint strong {
  color: var(--color-error);
  font-family: monospace;
  font-size: var(--font-size-base);
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: var(--spacing-md);
}
.reset-confirm-input-field {
  text-align: center;
  font-size: 16px;
  letter-spacing: 2px;
}
</style>
