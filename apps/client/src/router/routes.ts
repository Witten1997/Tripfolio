import type { Component } from 'vue'
import type { RouteRecordRaw } from 'vue-router'

import type { Shell } from '@/shell/pickShell'

type Loader = () => Promise<{ default: Component }>

/** 同一路由在两个壳下指向不同页面组件；路由名与路径保持一致，便于共享跳转逻辑。 */
interface PagePair {
  desktop: Loader
  mobile: Loader
}

declare module 'vue-router' {
  interface RouteMeta {
    /** 不要求登录；已登录访问 authOnly 的公开页（登录、注册）会跳到旅行列表。 */
    public?: boolean
    /** 仅未登录时有意义的页面（登录、注册、找回密码）。 */
    guestOnly?: boolean
  }
}

// 移动壳的认证页在安卓阶段用 Vant 重做；网页优先阶段两个壳共用桌面实现。
const pages = {
  tripList: {
    desktop: () => import('@/desktop/pages/TripListPage.vue'),
    mobile: () => import('@/mobile/pages/TripListPage.vue'),
  },
  login: {
    desktop: () => import('@/desktop/pages/LoginPage.vue'),
    mobile: () => import('@/desktop/pages/LoginPage.vue'),
  },
  register: {
    desktop: () => import('@/desktop/pages/RegisterPage.vue'),
    mobile: () => import('@/desktop/pages/RegisterPage.vue'),
  },
  resetPassword: {
    desktop: () => import('@/desktop/pages/ResetPasswordPage.vue'),
    mobile: () => import('@/desktop/pages/ResetPasswordPage.vue'),
  },
  account: {
    desktop: () => import('@/desktop/pages/AccountPage.vue'),
    mobile: () => import('@/desktop/pages/AccountPage.vue'),
  },
  recycleBin: {
    desktop: () => import('@/desktop/pages/RecycleBinPage.vue'),
    mobile: () => import('@/desktop/pages/RecycleBinPage.vue'),
  },
  themes: {
    desktop: () => import('@/desktop/pages/ThemesPage.vue'),
    mobile: () => import('@/mobile/pages/ThemesPage.vue'),
  },
  // 旅行详情与页签：移动壳在安卓阶段用 Vant 重做，网页优先阶段共用桌面实现。
  tripDetail: {
    desktop: () => import('@/desktop/pages/TripDetailPage.vue'),
    mobile: () => import('@/desktop/pages/TripDetailPage.vue'),
  },
  tripItinerary: {
    desktop: () => import('@/desktop/pages/trip/ItineraryTab.vue'),
    mobile: () => import('@/desktop/pages/trip/ItineraryTab.vue'),
  },
  tripPacking: {
    desktop: () => import('@/desktop/pages/trip/PackingTab.vue'),
    mobile: () => import('@/desktop/pages/trip/PackingTab.vue'),
  },
  tripTodos: {
    desktop: () => import('@/desktop/pages/trip/TodosTab.vue'),
    mobile: () => import('@/desktop/pages/trip/TodosTab.vue'),
  },
  tripLedger: {
    desktop: () => import('@/desktop/pages/trip/LedgerTab.vue'),
    mobile: () => import('@/desktop/pages/trip/LedgerTab.vue'),
  },
  tripMap: {
    desktop: () => import('@/desktop/pages/trip/TripMapTab.vue'),
    mobile: () => import('@/desktop/pages/trip/TripMapTab.vue'),
  },
  tripPlaceholder: {
    desktop: () => import('@/desktop/pages/trip/PlaceholderTab.vue'),
    mobile: () => import('@/desktop/pages/trip/PlaceholderTab.vue'),
  },
} satisfies Record<string, PagePair>

/** 只在移动壳提供的页面（本地数据库等平台能力的自检）。 */
const mobileOnlyRoutes: RouteRecordRaw[] = [
  {
    path: '/dev/local-db',
    name: 'dev-local-db',
    component: () => import('@/mobile/pages/DevLocalDbPage.vue'),
    meta: { public: true },
  },
]

export function buildRoutes(shell: Shell): RouteRecordRaw[] {
  const pick = (pair: PagePair) => (shell === 'desktop' ? pair.desktop : pair.mobile)
  const guest = { public: true, guestOnly: true }
  return [
    { path: '/', redirect: { name: 'trips' } },
    { path: '/login', name: 'login', component: pick(pages.login), meta: guest },
    { path: '/register', name: 'register', component: pick(pages.register), meta: guest },
    {
      path: '/reset-password',
      name: 'reset-password',
      component: pick(pages.resetPassword),
      meta: guest,
    },
    { path: '/trips', name: 'trips', component: pick(pages.tripList) },
    {
      path: '/trips/:tripId',
      name: 'trip-detail',
      component: pick(pages.tripDetail),
      redirect: { name: 'trip-itinerary' },
      children: [
        { path: 'itinerary', name: 'trip-itinerary', component: pick(pages.tripItinerary) },
        { path: 'ledger', name: 'trip-ledger', component: pick(pages.tripLedger) },
        { path: 'map', name: 'trip-map', component: pick(pages.tripMap) },
        { path: 'packing', name: 'trip-packing', component: pick(pages.tripPacking) },
        {
          path: 'album',
          name: 'trip-album',
          component: pick(pages.tripPlaceholder),
          props: { title: '相册', slice: '切片 5' },
        },
        { path: 'todos', name: 'trip-todos', component: pick(pages.tripTodos) },
      ],
    },
    { path: '/account', name: 'account', component: pick(pages.account) },
    { path: '/recycle-bin', name: 'recycle-bin', component: pick(pages.recycleBin) },
    { path: '/themes', name: 'themes', component: pick(pages.themes), meta: { public: true } },
    ...(shell === 'mobile' ? mobileOnlyRoutes : []),
    { path: '/:pathMatch(.*)*', redirect: { name: 'trips' } },
  ]
}
