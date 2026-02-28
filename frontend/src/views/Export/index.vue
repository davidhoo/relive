<template>
  <div class="export-page">
    <!-- 导出 -->
    <el-card shadow="never" style="margin-bottom: 20px">
      <template #header>
        <span><el-icon><Upload /></el-icon> 导出照片数据</span>
      </template>

      <el-alert
        title="导出说明"
        type="info"
        :closable="false"
        style="margin-bottom: 20px"
      >
        <p>导出功能将照片数据和分析结果打包为 SQLite 数据库，可用于离线分析或数据迁移。</p>
      </el-alert>

      <el-form label-width="150px" style="max-width: 600px">
        <el-form-item label="输出路径">
          <el-input v-model="exportPath" placeholder="/path/to/export.db" />
        </el-form-item>
        <el-form-item label="仅导出已分析">
          <el-switch v-model="exportAnalyzedOnly" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="handleExport" :loading="exporting">
            <el-icon><Download /></el-icon>
            开始导出
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 导入 -->
    <el-card shadow="never">
      <template #header>
        <span><el-icon><Download /></el-icon> 导入分析结果</span>
      </template>

      <el-alert
        title="导入说明"
        type="info"
        :closable="false"
        style="margin-bottom: 20px"
      >
        <p>导入功能将 SQLite 数据库中的分析结果导入到系统中，通过 file_hash 匹配照片。</p>
      </el-alert>

      <el-form label-width="150px" style="max-width: 600px">
        <el-form-item label="导入路径">
          <el-input v-model="importPath" placeholder="/path/to/import.db" />
        </el-form-item>
        <el-form-item>
          <el-button type="success" @click="handleImport" :loading="importing">
            <el-icon><Upload /></el-icon>
            开始导入
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'

const exportPath = ref('')
const exportAnalyzedOnly = ref(true)
const exporting = ref(false)

const importPath = ref('')
const importing = ref(false)

// 导出
const handleExport = async () => {
  if (!exportPath.value) {
    ElMessage.warning('请输入导出路径')
    return
  }

  exporting.value = true
  try {
    // TODO: 调用导出 API
    await new Promise(resolve => setTimeout(resolve, 1000))
    ElMessage.success('导出成功')
  } catch (error: any) {
    ElMessage.error(error.message || '导出失败')
  } finally {
    exporting.value = false
  }
}

// 导入
const handleImport = async () => {
  if (!importPath.value) {
    ElMessage.warning('请输入导入路径')
    return
  }

  importing.value = true
  try {
    // TODO: 调用导入 API
    await new Promise(resolve => setTimeout(resolve, 1000))
    ElMessage.success('导入成功')
  } catch (error: any) {
    ElMessage.error(error.message || '导入失败')
  } finally {
    importing.value = false
  }
}
</script>

<style scoped>
.export-page {
  padding: 20px;
}
</style>
