<template>
  <div class="geocode-page">
    <PageHeader title="GPS 位置解析" subtitle="管理后台 GPS 逆地理编码队列，支持开始、停止和进度查看" :gradient="true">
      <template #actions>
        <el-button class="header-action-btn" @click="$router.push('/photos')">
          <el-icon><Picture /></el-icon>
          前往照片管理
        </el-button>
      </template>
    </PageHeader>

    <el-card shadow="never" class="section-card animate-fade-in">
      <template #header>
        <SectionHeader :icon="Location" title="后台任务">
          <template #actions>
            <span class="status-pill" :class="taskRunning ? 'warning' : taskStopping ? 'warning' : 'success'">
              {{ taskRunning ? '运行中' : taskStopping ? '停止中' : '未运行' }}
            </span>
          </template>
        </SectionHeader>
      </template>
      <div class="section-content">
        <div class="control-row control-row-stack">
          <div class="control-row-main">
            <el-button v-if="!taskRunning && !taskStopping" type="primary" size="large" @click="handleStart" :loading="starting" class="action-btn-primary">开启后台解析</el-button>
            <el-button v-else type="danger" size="large" @click="handleStop" :loading="stopping" :disabled="taskStopping" class="action-btn-danger">{{ taskStopping ? '停止中...' : '停止后台解析' }}</el-button>
            <el-button plain size="large" @click="handleRepairLegacyStatus" :loading="repairing">修复历史状态</el-button>
            <el-button plain size="large" type="warning" @click="handleRegeocodeAll" :loading="regeocoding">全量重建解析</el-button>
          </div>
          <div class="inline-note-wrap">
            <el-text type="info" class="inline-info-text aligned-note">后台解析会持续处理 GPS 逆地理编码队列。照片详情访问到未解析位置的照片时，会自动触发热点优先补队列。</el-text>
            <div class="inline-note-divider"></div>
          </div>
        </div>
        <div class="background-log-panel flat-log-panel">
          <div class="background-log-header">
            <span>任务日志（最后 100 行）</span>
            <el-button size="small" plain class="mini-action-btn" @click="loadBackgroundLogs">刷新</el-button>
          </div>
          <div class="background-log-body" ref="logContainerRef">
            <pre v-if="backgroundLogs.length">{{ backgroundLogs.join('\n') }}</pre>
            <div v-else class="background-log-empty">暂无 GPS 后台任务日志</div>
          </div>
        </div>
      </div>
    </el-card>

    <el-card shadow="never" class="section-card animate-fade-in animate-delay-1">
      <template #header>
        <SectionHeader :icon="DataLine" title="队列统计">
          <template #actions><el-button size="small" plain class="mini-action-btn" @click="loadData">刷新</el-button></template>
        </SectionHeader>
      </template>
      <div class="stats-grid">
        <div class="stat-item"><span class="stat-label">总任务</span><strong>{{ stats.total }}</strong></div>
        <div class="stat-item"><span class="stat-label">待处理</span><strong>{{ stats.pending + stats.queued }}</strong></div>
        <div class="stat-item"><span class="stat-label">处理中</span><strong>{{ stats.processing }}</strong></div>
        <div class="stat-item"><span class="stat-label">已完成</span><strong class="success">{{ stats.completed }}</strong></div>
        <div class="stat-item"><span class="stat-label">失败</span><strong class="danger">{{ stats.failed }}</strong></div>
        <div class="stat-item"><span class="stat-label">已取消</span><strong>{{ stats.cancelled }}</strong></div>
      </div>
    </el-card>

    <el-card shadow="never" class="section-card animate-fade-in animate-delay-2">
      <template #header><SectionHeader :icon="Clock" title="当前进度" /></template>
      <div class="runtime-inline-list">
        <div class="runtime-inline-row">
          <div class="runtime-inline-item"><span class="runtime-inline-label">任务状态</span><span class="runtime-inline-value">{{ task?.status || '-' }}</span></div>
          <div class="runtime-inline-item"><span class="runtime-inline-label">已处理</span><span class="runtime-inline-value">{{ task?.processed_jobs || 0 }}</span></div>
        </div>
        <div class="runtime-inline-row">
          <div class="runtime-inline-item"><span class="runtime-inline-label">当前照片</span><span class="runtime-inline-value mono">{{ task?.current_photo_id ? `Photo #${task.current_photo_id}` : '-' }}</span></div>
          <div class="runtime-inline-item"><span class="runtime-inline-label">开始时间</span><span class="runtime-inline-value">{{ formatTime(task?.started_at) }}</span></div>
        </div>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Clock, DataLine, Location, Picture } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { geocodeApi } from '@/api/geocode'
import type { GeocodeStats, GeocodeTask } from '@/types/geocode'

const task = ref<GeocodeTask | null>(null)
const stats = ref<GeocodeStats>({ total: 0, pending: 0, queued: 0, processing: 0, completed: 0, failed: 0, cancelled: 0 })
const starting = ref(false)
const stopping = ref(false)
const repairing = ref(false)
const regeocoding = ref(false)
const backgroundLogs = ref<string[]>([])
const logContainerRef = ref<HTMLElement | null>(null)
let timer: number | null = null

const taskRunning = computed(() => task.value?.status === 'running')
const taskStopping = computed(() => task.value?.status === 'stopping')
const formatTime = (value?: string) => value ? new Date(value).toLocaleString('zh-CN') : '-'
const loadBackgroundLogs = async () => { const res = await geocodeApi.getBackgroundLogs(); backgroundLogs.value = res.data?.data?.lines || [] }
const loadData = async () => { const [taskRes, statsRes, logsRes] = await Promise.all([geocodeApi.getTask(), geocodeApi.getStats(), geocodeApi.getBackgroundLogs()]); task.value = taskRes.data?.data || null; stats.value = statsRes.data?.data || stats.value; backgroundLogs.value = logsRes.data?.data?.lines || [] }
const handleStart = async () => { try { starting.value = true; await geocodeApi.startBackground(); ElMessage.success('GPS 逆地理编码后台任务已启动'); await loadData() } catch (error: any) { ElMessage.error(error.message || '启动 GPS 逆地理编码后台任务失败') } finally { starting.value = false } }
const handleStop = async () => { try { stopping.value = true; await geocodeApi.stopBackground(); ElMessage.info('已请求停止 GPS 逆地理编码后台任务'); await loadData() } catch (error: any) { ElMessage.error(error.message || '停止 GPS 逆地理编码后台任务失败') } finally { stopping.value = false } }
const handleRepairLegacyStatus = async () => { try { repairing.value = true; const res = await geocodeApi.repairLegacyStatus(); ElMessage.success(`历史 GPS 状态修复完成，共更新 ${res.data?.data?.count || 0} 张照片`); await loadData() } catch (error: any) { ElMessage.error(error.message || '修复历史 GPS 状态失败') } finally { repairing.value = false } }
const handleRegeocodeAll = async () => { try { await ElMessageBox.confirm('将对所有有 GPS 坐标的照片重新解析位置信息并覆盖更新，此操作在后台运行。确认继续？', '全量重建解析', { confirmButtonText: '确认', cancelButtonText: '取消', type: 'warning' }); regeocoding.value = true; await geocodeApi.regeocodeAll(); ElMessage.success('全量重建解析已在后台启动'); await loadData() } catch (error: any) { if (error !== 'cancel') ElMessage.error(error.message || '启动全量重建解析失败') } finally { regeocoding.value = false } }
onMounted(async () => { await loadData(); timer = window.setInterval(loadData, 5000) })
onUnmounted(() => { if (timer) clearInterval(timer) })
watch(backgroundLogs, async () => { await nextTick(); if (logContainerRef.value) logContainerRef.value.scrollTop = logContainerRef.value.scrollHeight })
</script>

<style scoped>
.geocode-page { display:flex; flex-direction:column; gap:20px; padding: var(--spacing-xl); }
.section-card { border-radius:18px; }
.section-card :deep(.el-card__header) { padding: 22px 28px; }
.section-card :deep(.el-card__body) { padding: 24px 28px; }
.section-content { display:flex; flex-direction:column; gap:12px; }
.control-row { display:flex; align-items:center; justify-content:space-between; gap:16px; }
.control-row-stack { flex-direction:column; align-items:stretch; }
.control-row-main { display:flex; align-items:center; gap:12px; }
.inline-note-wrap { width:100%; display:flex; flex-direction:column; gap:12px; }
.aligned-note { width:100%; text-align:left; }
.inline-note-divider { width:100%; height:1px; background: var(--color-border); }
.stats-grid { display:grid; grid-template-columns: repeat(auto-fit,minmax(140px,1fr)); gap:14px; }
.stat-item, .runtime-inline-item { padding:14px 16px; border-radius:14px; background: var(--el-fill-color-light); display:flex; flex-direction:column; gap:6px; }
.stat-label, .runtime-inline-label { color: var(--el-text-color-secondary); font-size:12px; }
.runtime-inline-list { display:flex; flex-direction:column; gap:12px; }
.runtime-inline-row { display:grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap:16px; }
.success { color: var(--el-color-success); }
.danger { color: var(--el-color-danger); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.status-pill { padding: 4px 10px; border-radius:999px; font-size:12px; }
.status-pill.success { color: var(--el-color-success); background: var(--el-color-success-light-9); }
.status-pill.warning { color: var(--el-color-warning); background: var(--el-color-warning-light-9); }
.background-log-panel { margin-top: 12px; border-radius:18px; border:1px solid var(--color-border); overflow:hidden; }
.background-log-header { display:flex; align-items:center; justify-content:space-between; gap:12px; padding:14px 16px; border-bottom:1px solid var(--color-border); background: var(--el-fill-color-light); color: var(--el-text-color-secondary); font-size:13px; }
.background-log-body { max-height:260px; overflow:auto; padding:14px 16px; background: var(--el-bg-color-overlay); }
.background-log-body pre { margin:0; white-space:pre-wrap; word-break:break-word; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size:12px; line-height:1.7; }
.background-log-empty { color: var(--el-text-color-secondary); font-size:13px; }
@media (max-width: 768px) { .geocode-page { padding: var(--spacing-lg); } .section-card :deep(.el-card__header), .section-card :deep(.el-card__body) { padding:20px; } .runtime-inline-row { grid-template-columns:1fr; } }
@media (max-width: 480px) { .geocode-page { padding: var(--spacing-md); } }
</style>
