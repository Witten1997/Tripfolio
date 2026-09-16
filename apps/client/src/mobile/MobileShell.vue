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

const showBottomBar = computed(() => session.isAuthenticated && !route.meta.guestOnly)
const travelActive = computed(() =>
  ['trips', 'trip-detail', 'trip-itinerary', 'trip-ledger', 'trip-map', 'trip-packing', 'trip-album', 'trip-todos'].includes(
    String(route.name ?? ''),
  ),
)
const accountActive = computed(() =>
  ['account', 'themes', 'recycle-bin'].includes(String(route.name ?? '')),
)

function createTrip() {
  void router.push({ name: 'trips', query: { create: String(Date.now()) } })
}
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div class="mobile-shell">
      <div class="tf-backdrop" aria-hidden="true"></div>
      <main class="mobile-shell__main">
        <RouterView :key="String($route.params.tripId ?? '')" />
      </main>
      <nav v-if="showBottomBar" class="mobile-bottom-bar" aria-label="主要导航">
        <RouterLink
          :to="{ name: 'trips' }"
          class="mobile-bottom-bar__item"
          :class="{ 'is-active': travelActive }"
          aria-label="旅行"
        >
          <ActionIcon name="luggage" />
          <span>旅行</span>
        </RouterLink>
        <button class="mobile-bottom-bar__create" type="button" aria-label="新建旅行" @click="createTrip">
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
  --tf-map-toggle-bottom: 84px;
  min-height: 100dvh;
  background: transparent;
}

.mobile-shell__main {
  min-height: 100dvh;
  padding-bottom: calc(88px + env(safe-area-inset-bottom));
}

.mobile-shell__main :deep(.trip-detail) {
  padding: 16px;
}

.mobile-bottom-bar {
  position: fixed;
  right: 0;
  bottom: 0;
  left: 0;
  z-index: 30;
  display: grid;
  grid-template-columns: 1fr 88px 1fr;
  align-items: end;
  min-height: 64px;
  padding: 6px max(18px, env(safe-area-inset-right)) env(safe-area-inset-bottom)
    max(18px, env(safe-area-inset-left));
  background: var(--tf-surface-raised);
  border-top: 1px solid var(--tf-line-soft);
  box-shadow: var(--tf-shadow-2);
}

.mobile-bottom-bar__item {
  display: flex;
  min-height: 54px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  color: var(--tf-text-3);
  font-size: 12px;
  font-weight: 600;
  text-decoration: none;
  touch-action: manipulation;
}

.mobile-bottom-bar__item :deep(svg) {
  width: 23px;
  height: 23px;
}

.mobile-bottom-bar__item.is-active {
  color: var(--tf-accent);
}

.mobile-bottom-bar__create {
  align-self: start;
  justify-self: center;
  display: grid;
  place-items: center;
  width: 64px;
  height: 64px;
  margin-top: -24px;
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
  width: 30px;
  height: 30px;
}

.mobile-bottom-bar__create:focus-visible,
.mobile-bottom-bar__item:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 3px;
}

.mobile-bottom-bar__create:active {
  transform: scale(0.96);
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
