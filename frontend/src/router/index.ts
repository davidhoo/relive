import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useUserStore } from '@/stores/user'
import MainLayout from '@/layouts/MainLayout.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/Login/index.vue'),
    meta: { public: true, title: '登录' }
  },
  {
    path: '/change-Password',
    name: 'ChangePassword',
    component: () => import('@/views/ChangePassword/index.vue'),
    meta: { title: '修改密码' }
  },
  {
    path: '/',
    component: MainLayout,
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', name: 'Dashboard', component: () => import('@/views/Dashboard/index.vue'), meta: { title: '仪表盘', icon: 'DataLine' } },
      { path: 'photos', name: 'Photos', component: () => import('@/views/Photos/index.vue'), meta: { title: '照片管理', icon: 'Picture' } },
      { path: 'photos/:id', name: 'PhotoDetail', component: () => import('@/views/Photos/Detail.vue'), meta: { title: '照片详情', hidden: true } },
      { path: 'analysis', name: 'Analysis', component: () => import('@/views/Analysis/index.vue'), meta: { title: 'AI 分析', icon: 'MagicStick' } },
      { path: 'thumbnails', name: 'Thumbnails', component: () => import('@/views/Thumbnails/index.vue'), meta: { title: '缩略图生成', icon: 'Files' } },
      { path: 'geocode', name: 'Geocode', component: () => import('@/views/Geocode/index.vue'), meta: { title: 'GPS 位置解析', icon: 'Location' } },
      { path: 'devices', name: 'Devices', component: () => import('@/views/Devices/index.vue'), meta: { title: '设备管理', icon: 'Monitor' } },
      { path: 'display', name: 'Display', component: () => import('@/views/Display/index.vue'), meta: { title: '展示策略', icon: 'View' } },
      { path: 'events', name: 'Events', component: () => import('@/views/Events/index.vue'), meta: { title: '事件浏览', icon: 'Collection' } },
      { path: 'events/:id', name: 'EventDetail', component: () => import('@/views/Events/Detail.vue'), meta: { title: '事件详情', hidden: true } },
      { path: 'config', name: 'Config', component: () => import('@/views/Config/index.vue'), meta: { title: '配置管理', icon: 'Setting' } },
      { path: 'system', name: 'System', component: () => import('@/views/System/index.vue'), meta: { title: '系统管理', icon: 'Cpu' } },
    ],
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

// 路由守卫
router.beforeEach(async (to, from, next) => {
  // 设置页面标题
  if (to.meta.title) {
    document.title = `${to.meta.title} - Relive`
  }

  const userStore = useUserStore()

  // 公开页面（登录页）直接放行
  if (to.meta.public) {
    // 如果已登录且不是首次登录，跳转到首页
    if (userStore.isLoggedIn && !userStore.isFirstLogin) {
      next('/')
      return
    }
    next()
    return
  }

  // 检查是否有 token
  if (!userStore.isLoggedIn) {
    // 没有 token，尝试从服务器获取用户信息（可能有 cookie）
    const userInfo = await userStore.fetchUserInfo()
    if (!userInfo) {
      ElMessage.warning('请先登录')
      next('/login')
      return
    }
  } else if (!userStore.userInfo) {
    // 有 token 但没有用户信息，需要从服务器获取（包括 isFirstLogin 状态）
    const userInfo = await userStore.fetchUserInfo()
    if (!userInfo) {
      // token 无效，重新登录
      ElMessage.warning('登录已过期，请重新登录')
      next('/login')
      return
    }
  }

  // 检查是否是首次登录（必须修改密码）
  if (userStore.isFirstLogin && to.path !== '/change-Password') {
    ElMessage.info('首次登录，请先修改密码')
    next('/change-Password')
    return
  }

  next()
})

export default router
