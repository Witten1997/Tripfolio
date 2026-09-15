<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useRoute } from 'vue-router'

import type { Shell } from '@/shell/pickShell'

const props = defineProps<{ shell: Shell; share: boolean }>()

// 访客分享页不挂载任何壳：导航栏、账号菜单与编辑组件根本不进入这个 chunk，权限边界靠"没加载"而不是"藏起来"。
// 两个壳各自懒加载，桌面壳的 Element Plus 与移动壳的 Vant 不会进入同一个 chunk。
const route = useRoute()
const ShareLayout = defineAsyncComponent(() => import('@/share/ShareLayout.vue'))
const DesktopShell = defineAsyncComponent(() => import('@/desktop/DesktopShell.vue'))
const MobileShell = defineAsyncComponent(() => import('@/mobile/MobileShell.vue'))
const RootComponent = computed(() =>
  (route.matched.length ? route.meta.share : props.share)
    ? ShareLayout
    : props.shell === 'desktop'
      ? DesktopShell
      : MobileShell,
)
</script>

<template>
  <component :is="RootComponent" />
</template>
