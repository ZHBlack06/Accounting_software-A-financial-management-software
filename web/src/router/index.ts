import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true },
  },
  {
    path: '/',
    component: () => import('@/layouts/AppLayout.vue'),
    children: [
      { path: '', name: 'dashboard', component: () => import('@/views/DashboardView.vue') },
      { path: 'transactions', name: 'transactions', component: () => import('@/views/TransactionsView.vue') },
      { path: 'reports', name: 'reports', component: () => import('@/views/ReportsView.vue') },
      { path: 'import', name: 'import', component: () => import('@/views/ImportView.vue') },
      { path: 'settings', name: 'settings', component: () => import('@/views/SettingsView.vue') },
    ],
  },
  { path: '/:pathMatch(.*)*', name: 'notFound', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

// 全局前置守卫：无 token 且非公开页 → /login；有 token 时每会话校验一次
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.token) {
    if (to.meta.public) return true
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login') return { name: 'dashboard' }
  if (!auth.checked) {
    const ok = await auth.check()
    if (!ok) return { name: 'login', query: { redirect: to.fullPath } }
  }
  return true
})
