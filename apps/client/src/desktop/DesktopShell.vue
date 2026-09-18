<script setup lang="ts">
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import '@/styles/bridge/element-plus.css'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import ActionIcon from '@/desktop/components/ActionIcon.vue'
import { authorizeAssetDownload } from '@/shared/api/assets'
import BrandLogo from '@/shared/components/BrandLogo.vue'
import { logout } from '@/shared/api/account'
import { useSessionStore } from '@/shared/stores/session'

const session = useSessionStore()
const route = useRoute()
const router = useRouter()
const avatarUrl = ref<string | null>(null)
const avatarInitial = computed(() => (session.account?.nickname ?? '').trim().slice(0, 1) || '·')
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

async function signOut() {
  await logout()
  await router.replace({ name: 'login' })
}

watch(
  () => session.account?.avatar_asset_id,
  async (assetId) => {
    avatarUrl.value = null
    if (!assetId) return
    try {
      const authorization = await authorizeAssetDownload(assetId, 'thumbnail')
      if (session.account?.avatar_asset_id === assetId) avatarUrl.value = authorization.url ?? null
    } catch {
      avatarUrl.value = null
    }
  },
  { immediate: true },
)
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div class="tf-backdrop" aria-hidden="true"></div>
    <div class="desktop-shell">
      <aside class="desktop-shell__sidebar tf-surface">
        <RouterLink to="/trips" class="desktop-shell__brand" aria-label="Tripfolio 行程">
          <BrandLogo alt="" />
          <span>Tripfolio</span>
        </RouterLink>
        <nav class="desktop-shell__nav" aria-label="主要导航">
          <RouterLink
            v-if="session.isAuthenticated"
            :to="{ name: 'trips' }"
            class="desktop-shell__nav-item"
            :class="{ 'is-section-active': travelActive }"
          >
            <ActionIcon name="luggage" />
            <span>行程</span>
          </RouterLink>
          <RouterLink
            v-if="session.isAuthenticated"
            :to="{ name: 'recycle-bin' }"
            class="desktop-shell__nav-item"
          >
            <ActionIcon name="trash" />
            <span>回收站</span>
          </RouterLink>
          <RouterLink :to="{ name: 'themes' }" class="desktop-shell__nav-item">
            <ActionIcon name="theme" />
            <span>主题</span>
          </RouterLink>
        </nav>
        <div class="desktop-shell__sidebar-spacer"></div>
        <div v-if="session.isAuthenticated" class="desktop-shell__account-wrap">
          <details class="desktop-shell__account-menu">
            <summary class="desktop-shell__account" aria-label="打开个人设置菜单">
              <span class="desktop-shell__avatar">
                <img
                  v-if="avatarUrl"
                  :src="avatarUrl"
                  :alt="session.account?.nickname ?? '账号头像'"
                />
                <span v-else>{{ avatarInitial }}</span>
              </span>
              <span class="desktop-shell__account-name">{{
                session.account?.nickname ?? '账号'
              }}</span>
              <span class="desktop-shell__account-chevron" aria-hidden="true">⌄</span>
            </summary>
            <div class="desktop-shell__account-panel" role="menu">
              <RouterLink :to="{ name: 'personal-settings' }" role="menuitem">
                个人设置
              </RouterLink>
              <RouterLink :to="{ name: 'change-password' }" role="menuitem">
                修改密码
              </RouterLink>
              <RouterLink :to="{ name: 'login-devices' }" role="menuitem">
                登录设备管理
              </RouterLink>
              <button type="button" role="menuitem" @click="signOut">退出</button>
            </div>
          </details>
        </div>
      </aside>
      <main class="desktop-shell__main">
        <!-- 按旅行 id 作 key：从一趟旅行的详情直接跳到另一趟时整页重新挂载，上下文不串。 -->
        <RouterView :key="String($route.params.tripId ?? '')" />
      </main>
    </div>
  </ElConfigProvider>
</template>

<style scoped>
.desktop-shell {
  min-height: 100dvh;
}

.desktop-shell__sidebar {
  position: fixed;
  top: 20px;
  bottom: 20px;
  left: 20px;
  z-index: 10;
  display: flex;
  box-sizing: border-box;
  width: 210px;
  padding: 18px 14px 14px;
  flex-direction: column;
  overflow: hidden;
}

.desktop-shell__brand {
  --tf-brand-logo-size: 44px;
  display: inline-flex;
  align-items: center;
  gap: 11px;
  padding: 0 8px 18px;
  border-bottom: 1px solid var(--tf-line-soft);
  color: var(--tf-text-1);
  font-family: var(--tf-font-display);
  font-size: 22px;
  font-weight: 700;
  text-decoration: none;
}

.desktop-shell__nav {
  display: flex;
  padding-top: 18px;
  flex-direction: column;
  gap: 7px;
}

.desktop-shell__nav-item {
  display: flex;
  min-height: 46px;
  padding: 0 14px;
  align-items: center;
  gap: 12px;
  border: 1px solid transparent;
  border-radius: var(--tf-radius-control);
  text-decoration: none;
  color: var(--tf-text-2);
  font-size: 14px;
  font-weight: 650;
  transition:
    background-color var(--tf-duration-fast) var(--tf-ease),
    border-color var(--tf-duration-fast) var(--tf-ease),
    color var(--tf-duration-fast) var(--tf-ease),
    transform var(--tf-duration-fast) var(--tf-ease);
}

.desktop-shell__nav-item:hover {
  border-color: var(--tf-line-soft);
  background: var(--tf-surface-inset);
  color: var(--tf-text-1);
}

.desktop-shell__nav-item:focus-visible,
.desktop-shell__account:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 2px;
}

.desktop-shell__nav-item:active {
  transform: scale(0.98);
}

.desktop-shell__nav-item.router-link-active {
  border-color: color-mix(in srgb, var(--tf-accent) 36%, transparent);
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  box-shadow: var(--tf-shadow-1);
}

.desktop-shell__nav-item.is-section-active {
  border-color: color-mix(in srgb, var(--tf-accent) 36%, transparent);
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  box-shadow: var(--tf-shadow-1);
}

.desktop-shell__sidebar-spacer {
  flex: 1;
}

.desktop-shell__account-wrap {
  padding-top: 14px;
  border-top: 1px solid var(--tf-line-soft);
}

.desktop-shell__account {
  display: flex;
  min-width: 0;
  padding: 7px 8px;
  align-items: center;
  gap: 10px;
  border-radius: var(--tf-radius-control);
  color: var(--tf-text-2);
  text-decoration: none;
  cursor: pointer;
  list-style: none;
}

.desktop-shell__account::-webkit-details-marker {
  display: none;
}

.desktop-shell__account:hover,
.desktop-shell__account-menu[open] .desktop-shell__account {
  background: var(--tf-surface-inset);
  color: var(--tf-accent);
}

.desktop-shell__account-menu {
  position: relative;
}

.desktop-shell__account-chevron {
  margin-left: auto;
  color: var(--tf-text-3);
  font-size: 17px;
  line-height: 1;
  transform: translateY(-2px);
}

.desktop-shell__account-panel {
  position: absolute;
  right: 0;
  bottom: calc(100% + 10px);
  display: flex;
  min-width: 170px;
  padding: 8px;
  flex-direction: column;
  gap: 3px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-card);
  background: var(--tf-surface-raised);
  box-shadow: var(--tf-shadow-2);
}

.desktop-shell__account-panel a,
.desktop-shell__account-panel button {
  display: block;
  padding: 9px 10px;
  border: 0;
  border-radius: var(--tf-radius-control);
  background: transparent;
  color: var(--tf-text-2);
  font: inherit;
  font-size: 13px;
  text-align: left;
  text-decoration: none;
  cursor: pointer;
}

.desktop-shell__account-panel a:hover,
.desktop-shell__account-panel button:hover {
  background: var(--tf-surface-inset);
  color: var(--tf-accent);
}

.desktop-shell__account-panel button {
  border-top: 1px solid var(--tf-line-soft);
  border-radius: 0;
  color: var(--tf-danger);
  margin-top: 3px;
  padding-top: 11px;
}

.desktop-shell__avatar {
  box-sizing: border-box;
  display: grid;
  width: 42px;
  height: 42px;
  flex: 0 0 42px;
  place-items: center;
  overflow: hidden;
  border: 1px solid var(--tf-line-soft);
  border-radius: 50%;
  background: var(--tf-accent-soft);
  color: var(--tf-accent);
  font-family: var(--tf-font-display);
  font-size: 17px;
  font-weight: 700;
  box-shadow: var(--tf-shadow-1);
}

.desktop-shell__avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.desktop-shell__account-name {
  min-width: 0;
  overflow: hidden;
  font-size: 13px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.desktop-shell__main {
  box-sizing: border-box;
  min-width: 0;
  min-height: 100dvh;
  margin-left: 250px;
  padding: 24px 32px 48px 0;
}
</style>
