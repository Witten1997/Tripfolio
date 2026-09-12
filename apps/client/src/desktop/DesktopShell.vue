<script setup lang="ts">
import { ElConfigProvider, ElContainer, ElHeader, ElMain } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'

import { useSessionStore } from '@/shared/stores/session'

const session = useSessionStore()
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <ElContainer class="desktop-shell">
      <ElHeader class="desktop-shell__header">
        <RouterLink to="/trips" class="desktop-shell__brand">Tripfolio</RouterLink>
        <nav v-if="session.isAuthenticated" class="desktop-shell__nav">
          <RouterLink :to="{ name: 'trips' }">旅行</RouterLink>
          <RouterLink :to="{ name: 'account' }" class="desktop-shell__account">
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
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--el-border-color);
}

.desktop-shell__brand {
  font-weight: 600;
  text-decoration: none;
  color: var(--el-text-color-primary);
}

.desktop-shell__nav {
  display: flex;
  gap: 20px;
}

.desktop-shell__nav a {
  text-decoration: none;
  color: var(--el-text-color-regular);
}

.desktop-shell__nav a.router-link-active {
  color: var(--el-color-primary);
}
</style>
