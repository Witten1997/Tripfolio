<script setup lang="ts">
import { ElConfigProvider, ElContainer, ElHeader, ElMain } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import '@/styles/bridge/element-plus.css'

import { useSessionStore } from '@/shared/stores/session'

const session = useSessionStore()
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div class="tf-backdrop" aria-hidden="true"></div>
    <ElContainer class="desktop-shell">
      <ElHeader class="desktop-shell__header tf-bar">
        <RouterLink to="/trips" class="desktop-shell__brand">Tripfolio</RouterLink>
        <nav class="desktop-shell__nav">
          <template v-if="session.isAuthenticated">
            <RouterLink :to="{ name: 'trips' }">旅行</RouterLink>
            <RouterLink :to="{ name: 'recycle-bin' }">回收站</RouterLink>
          </template>
          <RouterLink :to="{ name: 'themes' }">主题</RouterLink>
          <RouterLink
            v-if="session.isAuthenticated"
            :to="{ name: 'account' }"
            class="desktop-shell__account"
          >
            {{ session.account?.nickname ?? '账号' }}
          </RouterLink>
        </nav>
      </ElHeader>
      <ElMain>
        <RouterView />
      </ElMain>
    </ElContainer>
  </ElConfigProvider>
</template>

<style scoped>
.desktop-shell {
  min-height: 100vh;
}

.desktop-shell__header {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.desktop-shell__brand {
  font-weight: 600;
  text-decoration: none;
  color: var(--tf-text-1);
}

.desktop-shell__nav {
  display: flex;
  gap: 20px;
}

.desktop-shell__nav a {
  text-decoration: none;
  color: var(--tf-text-2);
  transition: color var(--tf-duration-fast) var(--tf-ease);
}

.desktop-shell__nav a.router-link-active {
  color: var(--tf-accent);
}
</style>
