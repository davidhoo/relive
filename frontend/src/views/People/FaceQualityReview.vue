<template>
  <div class="face-quality-retired">
    <el-result
      icon="info"
      title="人脸质检已下线"
      sub-title="自动质检与审核页已停用。人工排除与恢复请到人物管理或照片详情中操作。"
    >
      <template #extra>
        <el-button type="primary" @click="goPeople">前往人物管理</el-button>
        <el-button @click="goPhotos">前往照片管理</el-button>
      </template>
    </el-result>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()
const goPeople = () => router.replace({ name: 'People' })
const goPhotos = () => router.replace({ name: 'Photos' })
</script>

<style scoped>
.face-quality-retired {
  padding: 48px 24px;
}
</style>
