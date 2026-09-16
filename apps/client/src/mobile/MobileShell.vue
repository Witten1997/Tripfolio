<script setup lang="ts">
// 网页优先阶段，旅行详情等业务页暂复用 Element Plus；冷启动也须加载样式与中文配置。
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import '@/styles/bridge/element-plus.css'
import { NavBar as VanNavBar } from 'vant'
import 'vant/lib/index.css'
import '@/styles/bridge/vant.css'
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import IconAction from '@/desktop/components/IconAction.vue'
import BrandLogo from '@/shared/components/BrandLogo.vue'
import { useSessionStore } from '@/shared/stores/session'

const route = useRoute()
const router = useRouter()
const session = useSessionStore()

// 顶栏右上角的三个入口与桌面壳一致（主题／回收站／账号），区别是移动壳没有昵称文字，
// 账号也做成图标。旅行列表是移动壳的根页面，没有上一页可回；登录与注册页自带互跳链接，
// 因此只在这两者之外、且已登录时显示返回。
const showBack = computed(() => session.isAuthenticated && route.name !== 'trips')

/** 从链接直接进入（新标签、分享或刷新后前进）时没有可回退的历史，退回旅行列表而不是留在原地。 */
function goBack() {
  if (router.options.history.state?.back) router.back()
  else void router.push({ name: 'trips' })
}
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div class="mobile-shell">
      <div class="tf-backdrop" aria-hidden="true"></div>
      <VanNavBar
        fixed
        placeholder
        safe-area-inset-top
        :left-arrow="showBack"
        @click-left="goBack"
      >
        <template #title>
          <BrandLogo class="mobile-shell__brand" />
        </template>
        <template #right>
          <span class="mobile-shell__nav">
            <IconAction :to="{ name: 'themes' }" icon="theme" label="主题中心" variant="navigation" />
            <IconAction
              v-if="session.isAuthenticated"
              :to="{ name: 'recycle-bin' }"
              icon="trash"
              label="回收站"
              variant="navigation"
            />
            <IconAction
              v-if="session.isAuthenticated"
              :to="{ name: 'account' }"
              icon="user"
              label="账号"
              variant="navigation"
            />
          </span>
        </template>
      </VanNavBar>
      <main class="mobile-shell__main">
        <RouterView :key="String($route.params.tripId ?? '')" />
      </main>
    </div>
  </ElConfigProvider>
</template>

<style scoped>
.mobile-shell {
  min-height: 100vh;
  background: transparent;
}

.mobile-shell__nav {
  display: flex;
  align-items: center;
  gap: 4px;
}

.mobile-shell__brand {
  --tf-brand-logo-size: 34px;
}

.mobile-shell__main {
  padding-bottom: env(safe-area-inset-bottom);
}

.mobile-shell__main :deep(.trip-detail) {
  padding: 16px;
}
</style>
