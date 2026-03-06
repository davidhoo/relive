<template>
  <div class="display-page">
    <el-card shadow="never">
      <template #header>
        <span><el-icon><View /></el-icon> 展示策略</span>
      </template>

      <el-alert
        title="展示策略说明"
        type="info"
        :closable="false"
        style="margin-bottom: 20px"
      >
        <p>根据不同的算法策略，设备（电子相框/手机等）将从照片库中选择合适的照片进行展示。</p>
      </el-alert>

      <el-form :model="form" label-width="150px" style="max-width: 800px">
        <el-form-item label="展示策略">
          <el-select v-model="form.algorithm" placeholder="请选择策略" style="width: 100%">
            <el-option label="随机选择" value="random" />
            <el-option label="回忆优先" value="memory_first" />
            <el-option label="美观优先" value="beauty_first" />
            <el-option label="年度最佳" value="best_of_year" />
            <el-option label="智能推荐" value="smart" />
          </el-select>
        </el-form-item>

        <el-form-item label="每日挑选数量">
          <el-input-number
            v-model="form.dailyCount"
            :min="1"
            :max="20"
            :step="1"
            style="width: 200px"
          />
          <span class="help-text">每天为设备挑选展示的照片数量</span>
        </el-form-item>

        <el-form-item label="美学评分阈值">
          <el-slider
            v-model="form.minBeautyScore"
            :min="0"
            :max="100"
            :step="5"
            show-stops
            show-input
          />
        </el-form-item>

        <el-form-item label="回忆价值阈值">
          <el-slider
            v-model="form.minMemoryScore"
            :min="0"
            :max="100"
            :step="5"
            show-stops
            show-input
          />
        </el-form-item>

        <el-form-item>
          <el-button type="primary" @click="handleSave" :loading="saving">
            保存配置
          </el-button>
          <el-button @click="handleReset">重置</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { displayStrategyApi, defaultDisplayStrategyConfig } from '@/api/config'
import type { DisplayStrategyConfig } from '@/api/config'

const form = ref<DisplayStrategyConfig>({
  algorithm: 'smart',
  minBeautyScore: 60,
  minMemoryScore: 60,
  dailyCount: 3,
})

const saving = ref(false)
const loading = ref(false)

// 从 API 加载配置
const loadConfig = async () => {
  loading.value = true
  try {
    const config = await displayStrategyApi.getConfig()
    form.value = config
  } catch (error: any) {
    ElMessage.error('加载配置失败：' + (error.message || '未知错误'))
  } finally {
    loading.value = false
  }
}

// 保存配置
const handleSave = async () => {
  saving.value = true
  try {
    await displayStrategyApi.updateConfig(form.value)
    ElMessage.success('配置已保存')
  } catch (error: any) {
    ElMessage.error(error.message || '保存配置失败')
  } finally {
    saving.value = false
  }
}

// 重置配置
const handleReset = async () => {
  try {
    await ElMessageBox.confirm(
      '确定要重置为默认配置吗？',
      '确认重置',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    // 先重置表单
    form.value = { ...defaultDisplayStrategyConfig }
    // 保存重置后的配置
    try {
      await displayStrategyApi.updateConfig(form.value)
      ElMessage.success('配置已重置为默认值')
    } catch (apiError: any) {
      ElMessage.error(apiError.message || '保存重置配置失败')
    }
  } catch (error: any) {
    // 用户取消操作，不处理
    if (error === 'cancel') return
    // 其他错误（如弹窗异常）
    console.error('Reset dialog error:', error)
  }
}

onMounted(() => {
  loadConfig()
})
</script>

<style scoped>
.display-page {
  padding: 20px;
}

.help-text {
  margin-left: 10px;
  color: #909399;
  font-size: 12px;
}
</style>
