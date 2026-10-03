import { createRouter, createWebHistory } from 'vue-router'
import { adminBasePath } from './config'
import { checkSetup, identity, restoreSession, setupRequired } from './api'

export const router = createRouter({
  history: createWebHistory(adminBasePath),
  routes: [
    { path: '/backups', name: 'backups', component: () => import('./pages/BackupsPage.vue') },
    { path: '/', redirect: '/overview' },
    { path: '/overview', name: 'overview', component: () => import('./pages/OverviewPage.vue') },
    { path: '/users', name: 'users', component: () => import('./pages/UsersPage.vue') },
    { path: '/trips', name: 'trips', component: () => import('./pages/TripsPage.vue') },
    { path: '/trips/:id', name: 'trip', component: () => import('./pages/TripDetailPage.vue') },
    { path: '/login', name: 'login', component: () => import('./pages/LoginPage.vue') },
    { path: '/setup', name: 'setup', component: () => import('./pages/SetupPage.vue') },
    { path: '/account', name: 'account', component: () => import('./pages/AccountPage.vue') },
    { path: '/audits', name: 'audits', component: () => import('./pages/AuditsPage.vue') },
    { path: '/runtime', name: 'runtime', component: () => import('./pages/RuntimePage.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/overview' },
  ],
})

router.beforeEach(async (to) => {
  try {
    await checkSetup(to.name === 'setup')
  } catch {
    return to.name === 'login' ? true : { name: 'login' }
  }
  if (setupRequired.value) return to.name === 'setup' ? true : { name: 'setup' }
  await restoreSession()
  if (to.name === 'setup') return { name: identity.value ? 'overview' : 'login' }
  if (!identity.value && to.name !== 'login') return { name: 'login' }
  if (identity.value && to.name === 'login') return { name: 'overview' }
})
