<template>
  <div class="section-header">
    <div class="section-header-main">
      <el-icon v-if="icon" class="section-header-icon">
        <component :is="icon" />
      </el-icon>
      <span class="section-header-title">{{ title }}</span>
    </div>
    <div v-if="$slots.actions" class="section-header-actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup lang="ts">
import type { Component } from 'vue'

defineProps<{
  title: string
  icon?: Component
}>()
</script>

<style scoped>
.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.section-header-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.section-header-icon {
  color: var(--color-primary);
}

.section-header-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.section-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
</style>
