import { createRouter, createWebHistory } from 'vue-router'
import { identity, restoreSession } from './api'

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/overview' },
    { path: '/overview', name: 'overview', component: () => import('./pages/OverviewPage.vue') },
    { path: '/users', name: 'users', component: () => import('./pages/UsersPage.vue') },
    { path: '/trips', name: 'trips', component: () => import('./pages/TripsPage.vue') },
    { path: '/trips/:id', name: 'trip', component: () => import('./pages/TripDetailPage.vue') },
    { path: '/login', name: 'login', component: () => import('./pages/LoginPage.vue') },
    { path: '/account', name: 'account', component: () => import('./pages/AccountPage.vue') },
    { path: '/audits', name: 'audits', component: () => import('./pages/AuditsPage.vue') },
    { path: '/runtime', name: 'runtime', component: () => import('./pages/RuntimePage.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/overview' },
  ],
})

router.beforeEach(async (to) => {
  await restoreSession()
  if (!identity.value && to.name !== 'login') return { name: 'login' }
  if (identity.value && to.name === 'login') return { name: 'overview' }
})
