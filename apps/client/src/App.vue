<script setup lang="ts">
import { defineAsyncComponent } from 'vue'

import type { Shell } from '@/shell/pickShell'

const props = defineProps<{ shell: Shell }>()

// 两个壳各自懒加载，桌面壳的 Element Plus 与移动壳的 Vant 不会进入同一个 chunk。
const ShellComponent = defineAsyncComponent(() =>
  props.shell === 'desktop'
    ? import('@/desktop/DesktopShell.vue')
    : import('@/mobile/MobileShell.vue'),
)
</script>

<template>
  <ShellComponent />
</template>
