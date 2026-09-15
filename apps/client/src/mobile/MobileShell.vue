<script setup lang="ts">
// 网页优先阶段，旅行详情等业务页暂复用 Element Plus；冷启动也须加载样式与中文配置。
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import '@/styles/bridge/element-plus.css'
import { NavBar as VanNavBar } from 'vant'
import 'vant/lib/index.css'
import '@/styles/bridge/vant.css'
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div class="mobile-shell">
      <div class="tf-backdrop" aria-hidden="true"></div>
      <VanNavBar title="Tripfolio" fixed placeholder safe-area-inset-top />
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

.mobile-shell__main {
  padding-bottom: env(safe-area-inset-bottom);
}

.mobile-shell__main :deep(.trip-detail) {
  padding: 16px;
}
</style>
