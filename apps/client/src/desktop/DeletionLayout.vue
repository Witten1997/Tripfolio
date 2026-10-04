<script setup lang="ts">
import 'element-plus/dist/index.css'
import '@/styles/bridge/element-plus.css'
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { onMounted, onBeforeUnmount } from 'vue'
import { installNoIndex } from '@/share/robots'

let uninstall: (() => void) | undefined
onMounted(() => {
  uninstall = installNoIndex(document)
})
onBeforeUnmount(() => uninstall?.())
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <div class="tf-backdrop" aria-hidden="true"></div>
    <main class="deletion-layout"><RouterView /></main>
  </ElConfigProvider>
</template>

<style scoped>
.deletion-layout {
  position: relative;
  box-sizing: border-box;
  width: min(100%, 800px);
  min-height: 100dvh;
  margin: 0 auto;
  padding: 32px clamp(16px, 4vw, 32px);
  color: var(--tf-text-1);
}
</style>
