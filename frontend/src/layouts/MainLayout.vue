<template>
  <el-container class="main-layout">
    <!-- 侧边栏 -->
    <el-aside width="240px" class="sidebar">
      <div class="logo">
        <div class="logo-icon">
          <el-icon><PictureFilled /></el-icon>
        </div>
        <h2 class="logo-text">Relive</h2>
      </div>
      <el-menu
        :default-active="activeMenu"
        :router="true"
        class="sidebar-menu"
      >
        <el-menu-item
          v-for="route in menuRoutes"
          :key="route.path"
          :index="route.path"
          class="menu-item"
        >
          <el-icon v-if="route.meta?.icon" class="menu-icon">
            <component :is="route.meta.icon" />
          </el-icon>
          <span class="menu-title">{{ route.meta?.title }}</span>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <!-- 主内容区 -->
    <el-container class="main-container">
      <!-- 顶部导航 -->
      <el-header class="header">
        <div class="header-content">
          <div class="header-left">
            <el-breadcrumb separator="/" class="breadcrumb">
              <el-breadcrumb-item :to="{ path: '/' }">
                <el-icon><HomeFilled /></el-icon>
                首页
              </el-breadcrumb-item>
              <el-breadcrumb-item v-if="currentRoute?.meta?.title">
                {{ currentRoute.meta.title }}
              </el-breadcrumb-item>
            </el-breadcrumb>
          </div>
          <div class="header-right">
            <div class="status-badge" v-if="systemHealth">
              <div class="status-indicator" :class="statusClass"></div>
              <span class="status-text">{{ statusText }}</span>
            </div>
          </div>
        </div>
      </el-header>

      <!-- 内容区 -->
      <el-main class="main-content">
        <router-view v-slot="{ Component }">
          <transition name="fade-slide" mode="out-in">
            <component :is="Component" :key="route.path" />
          </transition>
        </router-view>
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSystemStore } from '@/stores/system'

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()

// 当前激活的菜单
const activeMenu = computed(() => {
  const path = route.path
  if (path.startsWith('/photos/')) {
    return '/photos'
  }
  return path
})

// 当前路由
const currentRoute = computed(() => route)

// 菜单路由（过滤掉隐藏的）
const menuRoutes = computed(() => {
  const mainRoute = router.getRoutes().find(r => r.path === '/')
  if (!mainRoute?.children) return []
  return mainRoute.children.filter(r => !r.meta?.hidden)
})

// 系统健康状态
const systemHealth = computed(() => systemStore.health)

// 状态样式类
const statusClass = computed(() => {
  return systemHealth.value?.status === 'healthy' ? 'status-healthy' : 'status-error'
})

// 状态文本
const statusText = computed(() => {
  return systemHealth.value?.status === 'healthy' ? '系统正常' : '系统异常'
})

onMounted(() => {
  systemStore.fetchHealth()
  // 每30秒刷新一次健康状态
  setInterval(() => {
    systemStore.fetchHealth()
  }, 30000)
})
</script>

<style scoped>
/* ============ 主布局容器 ============ */
.main-layout {
  height: 100vh;
  overflow: hidden;
}

/* ============ 侧边栏 ============ */
.sidebar {
  background: linear-gradient(180deg, #0f172a 0%, #020617 100%);
  box-shadow: var(--shadow-lg);
  z-index: 100;
  overflow-y: auto;
}

/* Logo 区域 */
.logo {
  height: 80px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--spacing-md);
  padding: var(--spacing-lg);
  background: linear-gradient(135deg, rgba(16, 185, 129, 0.15), rgba(6, 182, 212, 0.15));
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);
  position: relative;
  overflow: hidden;
}

.logo::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: var(--gradient-primary);
  opacity: 0;
  transition: opacity var(--transition-base);
}

.logo:hover::before {
  opacity: 0.1;
}

.logo-icon {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
  background: var(--gradient-primary);
  border-radius: var(--radius-lg);
  color: white;
  box-shadow: var(--shadow-md);
  position: relative;
  z-index: 1;
  transition: all var(--transition-spring);
}

.logo:hover .logo-icon {
  transform: scale(1.15) rotate(5deg);
}

.logo-text {
  color: white;
  margin: 0;
  font-size: var(--font-size-2xl);
  font-weight: var(--font-weight-bold);
  position: relative;
  z-index: 1;
  background: linear-gradient(135deg, #ffffff, #10b981);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

/* 菜单样式 */
.sidebar-menu {
  border-right: none;
  background: transparent;
  padding: var(--spacing-md);
}

.sidebar-menu :deep(.el-menu-item) {
  height: 48px;
  line-height: 48px;
  margin-bottom: var(--spacing-sm);
  border-radius: var(--radius-lg);
  color: rgba(255, 255, 255, 0.7);
  transition: all var(--transition-spring);
  position: relative;
  overflow: hidden;
  will-change: transform;
}

.sidebar-menu :deep(.el-menu-item::before) {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  transform: translateY(-50%);
  width: 4px;
  height: 0;
  background: var(--gradient-primary);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  transition: height var(--transition-spring);
}

/* 磁性菜单效果 */
.sidebar-menu :deep(.el-menu-item:hover) {
  background: rgba(16, 185, 129, 0.15) !important;
  color: white;
  transform: translateX(6px);
}

.sidebar-menu :deep(.el-menu-item:hover::before) {
  height: 60%;
}

.sidebar-menu :deep(.el-menu-item.is-active) {
  background: var(--gradient-primary) !important;
  color: white;
  box-shadow: 0 8px 16px -4px rgba(16, 185, 129, 0.4);
  transform: translateX(4px);
}

.sidebar-menu :deep(.el-menu-item.is-active::before) {
  height: 60%;
  background: white;
}

.menu-icon {
  font-size: 20px;
  margin-right: var(--spacing-sm);
  transition: all var(--transition-spring);
  will-change: transform;
}

.sidebar-menu :deep(.el-menu-item:hover) .menu-icon,
.sidebar-menu :deep(.el-menu-item.is-active) .menu-icon {
  transform: scale(1.15) rotate(5deg);
}

.menu-title {
  font-weight: var(--font-weight-medium);
  font-size: var(--font-size-base);
}

/* ============ 主容器 ============ */
.main-container {
  background: var(--color-bg-secondary);
}

/* ============ 顶部栏 ============ */
.header {
  background: var(--color-bg-primary);
  border-bottom: 1px solid var(--color-border);
  box-shadow: var(--shadow-sm);
  display: flex;
  align-items: center;
  padding: 0 var(--spacing-xl);
  z-index: 90;
}

.header-content {
  width: 100%;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-left {
  flex: 1;
}

.breadcrumb {
  font-size: var(--font-size-base);
}

.breadcrumb :deep(.el-breadcrumb__item) {
  display: flex;
  align-items: center;
  gap: var(--spacing-xs);
}

.breadcrumb :deep(.el-breadcrumb__inner) {
  display: flex;
  align-items: center;
  gap: var(--spacing-xs);
  color: var(--color-text-secondary);
  font-weight: var(--font-weight-medium);
  transition: color var(--transition-fast);
}

.breadcrumb :deep(.el-breadcrumb__inner:hover) {
  color: var(--color-primary);
}

.breadcrumb :deep(.el-breadcrumb__item:last-child .el-breadcrumb__inner) {
  color: var(--color-text-primary);
}

/* 状态徽章 */
.status-badge {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  padding: var(--spacing-sm) var(--spacing-lg);
  background: var(--color-bg-secondary);
  border-radius: var(--radius-full);
  transition: all var(--transition-base);
}

.status-badge:hover {
  box-shadow: var(--shadow-sm);
}

.status-indicator {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  animation: pulse 2s ease-in-out infinite;
}

.status-healthy {
  background: var(--color-success);
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.2);
}

.status-error {
  background: var(--color-error);
  box-shadow: 0 0 0 2px rgba(239, 68, 68, 0.2);
}

.status-text {
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-medium);
  color: var(--color-text-secondary);
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.5;
  }
}

/* ============ 主内容区 ============ */
.main-content {
  padding: 0;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--color-bg-secondary);
}

/* ============ 页面切换动画 ============ */
.fade-slide-enter-active,
.fade-slide-leave-active {
  transition: all var(--transition-base);
}

.fade-slide-enter-from {
  opacity: 0;
  transform: translateY(20px);
}

.fade-slide-leave-to {
  opacity: 0;
  transform: translateY(-20px);
}

/* ============ 响应式设计 ============ */
@media (max-width: 768px) {
  .sidebar {
    width: 80px !important;
  }

  .logo-text {
    display: none;
  }

  .menu-title {
    display: none;
  }

  .sidebar-menu {
    padding: var(--spacing-sm);
  }

  .sidebar-menu :deep(.el-menu-item) {
    justify-content: center;
  }

  .menu-icon {
    margin-right: 0;
  }

  .header {
    padding: 0 var(--spacing-md);
  }

  .breadcrumb :deep(.el-breadcrumb__inner) {
    font-size: var(--font-size-sm);
  }
}

/* ============ 滚动条美化 ============ */
.sidebar::-webkit-scrollbar,
.main-content::-webkit-scrollbar {
  width: 6px;
}

.sidebar::-webkit-scrollbar-track {
  background: rgba(255, 255, 255, 0.05);
}

.sidebar::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.2);
  border-radius: var(--radius-sm);
}

.sidebar::-webkit-scrollbar-thumb:hover {
  background: rgba(255, 255, 255, 0.3);
}
</style>
