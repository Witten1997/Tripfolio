<script setup lang="ts">
import { ElConfigProvider, ElContainer, ElHeader, ElMain } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import '@/styles/bridge/element-plus.css'

import IconAction from '@/desktop/components/IconAction.vue'
import BrandLogo from '@/shared/components/BrandLogo.vue'
import { useSessionStore } from '@/shared/stores/session'

const session = useSessionStore()
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div class="tf-backdrop" aria-hidden="true"></div>
    <ElContainer class="desktop-shell">
      <ElHeader class="desktop-shell__header tf-bar">
        <RouterLink to="/trips" class="desktop-shell__brand" aria-label="Tripfolio 旅行首页">
          <BrandLogo alt="" />
        </RouterLink>
        <nav class="desktop-shell__nav">
          <template v-if="session.isAuthenticated">
            <IconAction :to="{ name: 'trips' }" icon="home" label="旅行首页" variant="navigation" />
            <IconAction
              :to="{ name: 'recycle-bin' }"
              icon="trash"
              label="回收站"
              variant="navigation"
            />
          </template>
          <IconAction :to="{ name: 'themes' }" icon="theme" label="主题" variant="navigation" />
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
        <!-- 按旅行 id 作 key：从一趟旅行的详情直接跳到另一趟时整页重新挂载，上下文不串。 -->
        <RouterView :key="String($route.params.tripId ?? '')" />
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
  --tf-brand-logo-size: 46px;
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  text-decoration: none;
}

.desktop-shell__nav {
  display: flex;
  align-items: center;
  gap: 12px;
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
