<script setup lang="ts">
import 'element-plus/dist/index.css'
import '@/styles/bridge/element-plus.css'

import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { onBeforeUnmount, onMounted } from 'vue'
import { RouterView, useRoute } from 'vue-router'

import { useThemeStore } from '@/shared/stores/theme'

import { installNoIndex } from './robots'

const route = useRoute()
const theme = useThemeStore()
let uninstall: (() => void) | null = null
onMounted(() => {
  uninstall = installNoIndex(document)
  void theme.restore({ defaultOnly: true })
})
onBeforeUnmount(() => {
  uninstall?.()
  uninstall = null
  void theme.restore()
})
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div class="tf-backdrop" aria-hidden="true"></div>
    <main class="share-layout">
      <RouterView :key="String(route.params.token)" />
      <footer class="share-footer">由 Tripfolio 分享 · 只读页面</footer>
    </main>
  </ElConfigProvider>
</template>

<style scoped>
.share-layout {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-height: 100dvh;
  max-width: 960px;
  margin: 0 auto;
  padding: 16px clamp(12px, 4vw, 32px) 32px;
  color: var(--tf-text-1);
}
.share-footer {
  margin-top: auto;
  padding-top: 24px;
  font-size: 12px;
  color: var(--tf-text-3);
  text-align: center;
}
</style>
