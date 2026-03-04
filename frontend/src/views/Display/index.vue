<template>
  <div class="display-page">
    <el-card shadow="never">
      <template #header>
        <span><el-icon><View /></el-icon> 展示策略配置</span>
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
        <el-form-item label="展示算法">
          <el-select v-model="form.algorithm" placeholder="请选择算法" style="width: 100%">
            <el-option label="随机选择" value="random" />
            <el-option label="按评分排序" value="score_based" />
            <el-option label="按时间排序" value="time_based" />
            <el-option label="智能推荐" value="smart" />
          </el-select>
        </el-form-item>

        <el-form-item label="最小评分阈值">
          <el-slider
            v-model="form.minScore"
            :min="0"
            :max="100"
            :step="5"
            show-stops
            show-input
          />
        </el-form-item>

        <el-form-item label="刷新间隔 (秒)">
          <el-input-number
            v-model="form.refreshInterval"
            :min="10"
            :max="3600"
            :step="10"
            style="width: 200px"
          />
        </el-form-item>

        <el-form-item label="启用动画">
          <el-switch v-model="form.enableAnimation" />
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
import { ElMessage } from 'element-plus'

const form = ref({
  algorithm: 'score_based',
  minScore: 60,
  refreshInterval: 60,
  enableAnimation: true,
})

const saving = ref(false)

// 保存配置
const handleSave = async () => {
  saving.value = true
  try {
    // TODO: 调用配置保存 API
    await new Promise(resolve => setTimeout(resolve, 500))
    ElMessage.success('配置已保存')
  } catch (error: any) {
    ElMessage.error(error.message || '保存配置失败')
  } finally {
    saving.value = false
  }
}

// 重置配置
const handleReset = () => {
  form.value = {
    algorithm: 'score_based',
    minScore: 60,
    refreshInterval: 60,
    enableAnimation: true,
  }
}

onMounted(() => {
  // TODO: 从 API 加载当前配置
})
</script>

<style scoped>
.display-page {
  padding: 20px;
}
</style>
