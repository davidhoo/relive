<template>
  <el-container class="main-layout" :class="{ 'sidebar-collapsed': isCollapsed }">
    <!-- 侧边栏 -->
    <el-aside :width="isCollapsed ? '64px' : '240px'" class="sidebar">
      <div class="logo">
        <div class="logo-icon">
          <img src="/logo-192.png" alt="Relive" class="logo-img" />
        </div>
        <h2 v-show="!isCollapsed" class="logo-text">Relive</h2>
      </div>
      <el-menu
        :default-active="activeMenu"
        :router="true"
        :collapse="isCollapsed"
        :collapse-transition="false"
        class="sidebar-menu"
      >
        <el-menu-item
          v-for="route in menuRoutes"
          :key="route.path"
          :index="`/${route.path}`"
          class="menu-item"
        >
          <el-icon v-if="route.meta?.icon" class="menu-icon">
            <component :is="resolveMenuIcon(route.meta?.icon)" />
          </el-icon>
          <template #title>
            <span class="menu-title">{{ route.meta?.title }}</span>
          </template>
        </el-menu-item>
      </el-menu>

      <!-- 折叠按钮 -->
      <div class="collapse-trigger" @click="toggleCollapse">
        <div class="collapse-line"></div>
        <div class="collapse-arrow" :class="{ 'is-collapsed': isCollapsed }">
          <el-icon :size="12">
            <component :is="isCollapsed ? ArrowRight : ArrowLeft" />
          </el-icon>
        </div>
      </div>
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
            <div class="status-badge">
              <div class="status-indicator" :class="statusClass"></div>
              <span class="status-text">{{ statusText }}</span>
            </div>
            <el-dropdown @command="handleCommand" class="user-dropdown">
              <span class="user-info">
                <el-icon><User /></el-icon>
                <span class="username">{{ userStore.username }}</span>
                <el-icon><ArrowDown /></el-icon>
              </span>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="changePassword">
                    <el-icon><Key /></el-icon>
                    修改密码
                  </el-dropdown-item>
                  <el-dropdown-item divided command="logout">
                    <el-icon><SwitchButton /></el-icon>
                    退出登录
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
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
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSystemStore } from '@/stores/system'
import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  Cpu,
  DataLine,
  HomeFilled,
  Key,
  MagicStick,
  Monitor,
  Picture,
  Files,
  Location,
  Setting,
  SwitchButton,
  User,
  View,
} from '@element-plus/icons-vue'

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()

// 侧边栏折叠状态
const isCollapsed = ref(false)
const menuIconMap = {
  DataLine,
  Picture,
  Files,
  Location,
  MagicStick,
  Monitor,
  View,
  Setting,
  Cpu,
} as const

const resolveMenuIcon = (iconName?: unknown) => {
  if (typeof iconName !== 'string') return undefined
  return menuIconMap[iconName as keyof typeof menuIconMap]
}


// 切换折叠状态
const toggleCollapse = () => {
  isCollapsed.value = !isCollapsed.value
  // 保存到 localStorage
  localStorage.setItem('sidebar-collapsed', String(isCollapsed.value))
}

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
  if (!systemHealth.value) return 'status-error'
  return systemHealth.value.status === 'healthy' ? 'status-healthy' : 'status-error'
})

// 状态文本
const statusText = computed(() => {
  if (!systemHealth.value) return '系统异常'
  return systemHealth.value.status === 'healthy' ? '系统正常' : '系统异常'
})

// 用户状态
import { useUserStore } from '@/stores/user'
import { ElMessage, ElMessageBox } from 'element-plus'
const userStore = useUserStore()

// 处理下拉菜单命令
const handleCommand = (command: string) => {
  if (command === 'logout') {
    handleLogout()
  } else if (command === 'changePassword') {
    router.push('/change-Password')
  }
}

// 退出登录
const handleLogout = async () => {
  try {
    await ElMessageBox.confirm('确定要退出登录吗？', '提示', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    })
    await userStore.logout()
    ElMessage.success('已退出登录')
    router.push('/login')
  } catch (error: any) {
    if (error !== 'cancel') {
      console.error('Logout error:', error)
    }
  }
}

onMounted(() => {
  // 初始化折叠状态
  const saved = localStorage.getItem('sidebar-collapsed')
  if (saved !== null) {
    isCollapsed.value = saved === 'true'
  }

  // 获取用户信息和系统状态
  userStore.fetchUserInfo()
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

/* ============ 侧边栏 - WeDance 风格 ============ */
.sidebar {
  background: var(--color-bg-sidebar);
  box-shadow: 2px 0 8px rgba(0, 0, 0, 0.04);
  z-index: 100;
  overflow-y: auto;
  overflow-x: hidden;
  border-right: 1px solid var(--color-border);
  position: relative;
  transition: width var(--transition-base);
}

/* Logo 区域 */
.logo {
  height: 80px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--spacing-md);
  padding: var(--spacing-lg);
  background: var(--color-bg-sidebar);
  border-bottom: 1px solid var(--color-border);
  transition: all var(--transition-base);
}

.logo:hover {
  background: var(--color-bg-hover);
}

.logo-icon {
  width: 48px;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  overflow: hidden;
  transition: transform var(--transition-base);
}

.logo-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.logo:hover .logo-icon {
  transform: scale(1.05);
}

.logo-text {
  color: var(--color-text-primary);
  margin: 0;
  font-size: var(--font-size-2xl);
  font-weight: var(--font-weight-bold);
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
  border-radius: var(--radius-sm);
  color: var(--color-text-secondary);
  transition: all var(--transition-base);
  background: transparent;
}

.sidebar-menu :deep(.el-menu-item:hover) {
  background: var(--color-bg-tertiary) !important;
  color: var(--color-text-primary);
}

.sidebar-menu :deep(.el-menu-item.is-active) {
  background: var(--color-bg-tertiary) !important;
  color: var(--color-primary);
  font-weight: var(--font-weight-semibold);
  position: relative;
}

.sidebar-menu :deep(.el-menu-item.is-active::before) {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  transform: translateY(-50%);
  width: 4px;
  height: 24px;
  background: var(--color-primary);
  border-radius: 0 4px 4px 0;
}

/* 折叠触发区域 - WeDance 风格 */
.collapse-trigger {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  background: transparent;
  transition: all var(--transition-base);
}

.collapse-trigger:hover {
  background: var(--color-bg-hover);
}

.collapse-line {
  position: absolute;
  top: 50%;
  left: 16px;
  right: 16px;
  height: 1px;
  background: var(--color-border);
  transition: all var(--transition-base);
}

.collapse-trigger:hover .collapse-line {
  background: var(--color-border-dark);
}

.collapse-arrow {
  position: relative;
  z-index: 1;
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-sidebar);
  border: 1px solid var(--color-border);
  border-radius: 50%;
  color: var(--color-text-secondary);
  transition: all var(--transition-base);
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.05);
}

.collapse-trigger:hover .collapse-arrow {
  background: var(--color-primary);
  border-color: var(--color-primary);
  color: white;
  transform: scale(1.1);
  box-shadow: 0 4px 8px rgba(0, 184, 148, 0.25);
}

.collapse-arrow.is-collapsed {
  transform: rotate(180deg);
}

.collapse-arrow.is-collapsed:hover {
  transform: rotate(180deg) scale(1.1);
}

/* 折叠状态的菜单样式 */
.sidebar-collapsed .logo {
  padding: var(--spacing-sm);
  gap: 0;
}

.sidebar-collapsed .logo-icon {
  width: 40px;
  height: 40px;
  font-size: 24px;
}

.sidebar-collapsed .sidebar-menu {
  padding: var(--spacing-xs);
}

.sidebar-collapsed .menu-icon {
  margin-right: 0;
}

.sidebar-collapsed .sidebar-menu :deep(.el-menu-item) {
  justify-content: center;
  padding: 0 !important;
}

.sidebar-collapsed .sidebar-menu :deep(.el-menu-item.is-active::before) {
  width: 3px;
  height: 20px;
}

/* 折叠状态下的折叠触发器 */
.sidebar-collapsed .collapse-line {
  left: 8px;
  right: 8px;
}

.menu-icon {
  font-size: 20px;
  margin-right: var(--spacing-sm);
  transition: transform var(--transition-base);
}

.sidebar-menu :deep(.el-menu-item:hover) .menu-icon,
.sidebar-menu :deep(.el-menu-item.is-active) .menu-icon {
  transform: scale(1.05);
}

.menu-title {
  font-weight: var(--font-weight-medium);
  font-size: var(--font-size-base);
}

/* ============ 主容器 ============ */
.main-container {
  background: var(--color-bg-primary);
}

/* ============ 顶部栏 ============ */
.header {
  background: var(--color-bg-tertiary);
  border-bottom: 1px solid var(--color-border);
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.04);
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

.header-right {
  display: flex;
  align-items: center;
  gap: var(--spacing-lg);
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
  border: 1px solid var(--color-border);
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
}

.status-error {
  background: var(--color-error);
}

.status-text {
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-medium);
  color: var(--color-text-secondary);
}

/* 用户下拉菜单 */
.user-dropdown {
  cursor: pointer;
}

.user-info {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  padding: var(--spacing-sm) var(--spacing-md);
  border-radius: var(--radius-sm);
  transition: all var(--transition-base);
  color: var(--color-text-secondary);
}

.user-info:hover {
  background: var(--color-bg-secondary);
  color: var(--color-text-primary);
}

.user-info .el-icon {
  font-size: 18px;
}

.username {
  font-weight: var(--font-weight-medium);
  font-size: var(--font-size-base);
  max-width: 100px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
  background: var(--color-bg-primary);
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
    width: 64px !important;
  }

  .logo {
    padding: var(--spacing-sm);
    gap: 0;
  }

  .logo-text {
    display: none;
  }

  .logo-icon {
    width: 40px;
    height: 40px;
    font-size: 24px;
  }

  .menu-title {
    display: none;
  }

  .sidebar-menu {
    padding: var(--spacing-xs);
  }

  .sidebar-menu :deep(.el-menu-item) {
    justify-content: center;
    padding: 0 !important;
  }

  .menu-icon {
    margin-right: 0;
  }

  .collapse-trigger {
    display: none;
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
  background: var(--color-bg-secondary);
}

.sidebar::-webkit-scrollbar-thumb {
  background: var(--color-border-dark);
  border-radius: var(--radius-sm);
}

.sidebar::-webkit-scrollbar-thumb:hover {
  background: var(--color-text-tertiary);
}
</style>

