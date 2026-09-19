<script setup lang="ts">
// 网页优先阶段，旅行详情等业务页暂复用 Element Plus；冷启动也须加载样式与中文配置。
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import '@/styles/bridge/element-plus.css'
import 'vant/lib/index.css'
import '@/styles/bridge/vant.css'
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import ActionIcon from '@/desktop/components/ActionIcon.vue'
import { useSessionStore } from '@/shared/stores/session'

const route = useRoute()
const router = useRouter()
const session = useSessionStore()

const detailRoutes = new Set([
  'trip-detail',
  'trip-itinerary',
  'trip-ledger',
  'trip-map',
  'trip-packing',
  'trip-album',
  'trip-todos',
])
const protectedBottomBarRoutes = new Set([
  'trips',
  'account',
  'personal-settings',
  'change-password',
  'login-devices',
  'recycle-bin',
  'expense-categories',
])
// 受保护页面已由路由守卫完成会话校验，底栏显示只跟当前页面有关，避免接口加载或令牌刷新时跳动。
const showBottomBar = computed(() => {
  if (route.meta.guestOnly || detailRoutes.has(String(route.name ?? ''))) return false
  // 受保护页通过路由守卫进入，即使加载请求期间令牌短暂刷新，底栏也不能消失。
  if (protectedBottomBarRoutes.has(String(route.name ?? ''))) return true
  return session.isAuthenticated
})
const travelActive = computed(() =>
  [
    'trips',
    'trip-detail',
    'trip-itinerary',
    'trip-ledger',
    'trip-map',
    'trip-packing',
    'trip-album',
    'trip-todos',
  ].includes(String(route.name ?? '')),
)
const accountActive = computed(() =>
  [
    'account',
    'personal-settings',
    'change-password',
    'login-devices',
    'themes',
    'recycle-bin',
    'expense-categories',
  ].includes(String(route.name ?? '')),
)

function createTrip() {
  void router.push({ name: 'trips', query: { create: String(Date.now()) } })
}
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div
      class="mobile-shell"
      :class="{
        'mobile-shell--has-bottom-bar': showBottomBar,
        'mobile-shell--trips': travelActive,
      }"
    >
      <div class="tf-backdrop" aria-hidden="true"></div>
      <main class="mobile-shell__main">
        <RouterView :key="String($route.params.tripId ?? '')" />
      </main>
      <nav
        v-if="showBottomBar"
        class="mobile-bottom-bar mobile-bottom-bar--trips"
        aria-label="主要导航"
      >
        <RouterLink
          :to="{ name: 'trips' }"
          class="mobile-bottom-bar__item"
          :class="{ 'is-active': travelActive }"
          aria-label="旅行"
        >
          <ActionIcon name="luggage" />
          <span>旅行</span>
        </RouterLink>
        <button
          class="mobile-bottom-bar__create"
          type="button"
          aria-label="新建旅行"
          @click="createTrip"
        >
          <ActionIcon name="plus" />
        </button>
        <RouterLink
          :to="{ name: 'account' }"
          class="mobile-bottom-bar__item"
          :class="{ 'is-active': accountActive }"
          aria-label="我的"
        >
          <ActionIcon name="user" />
          <span>我的</span>
        </RouterLink>
      </nav>
    </div>
  </ElConfigProvider>
</template>

<style scoped>
.mobile-shell {
  min-height: 100dvh;
  background: transparent;
}

.mobile-shell__main {
  min-height: 100dvh;
}

.mobile-shell--has-bottom-bar {
  --tf-map-toggle-bottom: 64px;
}

.mobile-shell--has-bottom-bar .mobile-shell__main {
  padding-bottom: calc(64px + env(safe-area-inset-bottom));
}

.mobile-shell__main :deep(.trip-detail) {
  padding: 16px;
}

.mobile-bottom-bar {
  display: grid;
  grid-template-columns: 1fr 72px 1fr;
  align-items: end;
  min-height: 52px;
  padding: 3px max(18px, env(safe-area-inset-right)) env(safe-area-inset-bottom)
    max(18px, env(safe-area-inset-left));
  background: var(--tf-surface-raised);
  border-top: 1px solid var(--tf-line-soft);
  box-shadow: var(--tf-shadow-2);
}

.mobile-bottom-bar__item {
  position: relative;
  display: flex;
  min-height: 46px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1px;
  color: var(--tf-text-3);
  font-size: 12px;
  font-weight: 600;
  text-decoration: none;
  touch-action: manipulation;
}

.mobile-bottom-bar__item :deep(svg) {
  width: 21px;
  height: 21px;
}

.mobile-bottom-bar__item.is-active {
  color: var(--tf-accent);
}

.mobile-bottom-bar__create {
  align-self: start;
  justify-self: center;
  display: grid;
  place-items: center;
  width: 54px;
  height: 54px;
  margin-top: -18px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: var(--tf-text-1);
  color: var(--tf-canvas);
  box-shadow: var(--tf-shadow-2);
  cursor: pointer;
  touch-action: manipulation;
}

.mobile-bottom-bar__create :deep(svg) {
  width: 27px;
  height: 27px;
}

.mobile-bottom-bar__create:focus-visible,
.mobile-bottom-bar__item:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 3px;
}

.mobile-bottom-bar__create:active {
  transform: scale(0.96);
}

.mobile-bottom-bar--trips {
  box-sizing: border-box;
  right: max(12px, env(safe-area-inset-right));
  bottom: max(10px, env(safe-area-inset-bottom));
  left: max(12px, env(safe-area-inset-left));
  min-height: 44px;
  height: 44px;
  padding: 0 14px;
  border: 1px solid color-mix(in srgb, var(--tf-surface-raised) 58%, transparent);
  border-radius: 28px 28px 20px 20px;
  background: color-mix(in srgb, var(--tf-surface-raised) 42%, transparent);
  box-shadow:
    0 18px 44px -22px color-mix(in srgb, var(--tf-accent) 36%, transparent),
    inset 0 1px 0 color-mix(in srgb, var(--tf-surface-raised) 82%, transparent),
    inset 0 -1px 0 color-mix(in srgb, var(--tf-accent) 8%, transparent);
  -webkit-backdrop-filter: blur(12px) saturate(125%);
  backdrop-filter: blur(12px) saturate(125%);
}

.mobile-bottom-bar--trips .mobile-bottom-bar__item {
  min-height: 42px;
  font-size: 11px;
}

.mobile-bottom-bar--trips .mobile-bottom-bar__item.is-active::after {
  position: absolute;
  right: 30%;
  bottom: 0;
  left: 32%;
  height: 2px;
  border-radius: 999px;
  background: var(--tf-accent);
  content: '';
}

.mobile-bottom-bar--trips .mobile-bottom-bar__create {
  width: 48px;
  height: 48px;
  margin-top: -18px;
  border: 3px solid color-mix(in srgb, var(--tf-surface-raised) 72%, transparent);
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  box-shadow:
    0 12px 24px -10px color-mix(in srgb, var(--tf-accent) 64%, transparent),
    inset 0 2px 0 color-mix(in srgb, var(--tf-surface-raised) 38%, transparent),
    inset 0 -3px 7px color-mix(in srgb, var(--tf-accent) 20%, transparent);
}

.mobile-bottom-bar--trips .mobile-bottom-bar__create :deep(svg) {
  width: 23px;
  height: 23px;
}

@media (prefers-reduced-motion: no-preference) {
  .mobile-bottom-bar__create,
  .mobile-bottom-bar__item {
    transition:
      color var(--tf-duration-fast) var(--tf-ease),
      transform var(--tf-duration-fast) var(--tf-ease);
  }
}
</style>
