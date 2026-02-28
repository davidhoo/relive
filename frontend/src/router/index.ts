import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import MainLayout from '@/layouts/MainLayout.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: MainLayout,
    redirect: '/dashboard',
    children: [
      {
        path: 'dashboard',
        name: 'Dashboard',
        component: () => import('@/views/Dashboard/index.vue'),
        meta: { title: '仪表盘', icon: 'DataLine' },
      },
      {
        path: 'photos',
        name: 'Photos',
        component: () => import('@/views/Photos/index.vue'),
        meta: { title: '照片管理', icon: 'Picture' },
      },
      {
        path: 'photos/:id',
        name: 'PhotoDetail',
        component: () => import('@/views/Photos/Detail.vue'),
        meta: { title: '照片详情', hidden: true },
      },
      {
        path: 'analysis',
        name: 'Analysis',
        component: () => import('@/views/Analysis/index.vue'),
        meta: { title: 'AI 分析', icon: 'MagicStick' },
      },
      {
        path: 'devices',
        name: 'Devices',
        component: () => import('@/views/Devices/index.vue'),
        meta: { title: '设备管理', icon: 'Monitor' },
      },
      {
        path: 'display',
        name: 'Display',
        component: () => import('@/views/Display/index.vue'),
        meta: { title: '展示策略', icon: 'View' },
      },
      {
        path: 'export',
        name: 'Export',
        component: () => import('@/views/Export/index.vue'),
        meta: { title: '导出/导入', icon: 'Download' },
      },
      {
        path: 'config',
        name: 'Config',
        component: () => import('@/views/Config/index.vue'),
        meta: { title: '配置管理', icon: 'Setting' },
      },
      {
        path: 'system',
        name: 'System',
        component: () => import('@/views/System/index.vue'),
        meta: { title: '系统管理', icon: 'Cpu' },
      },
    ],
  },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

// 路由守卫
router.beforeEach((to, _from, next) => {
  // 设置页面标题
  if (to.meta.title) {
    document.title = `${to.meta.title} - Relive`
  }
  next()
})

export default router
