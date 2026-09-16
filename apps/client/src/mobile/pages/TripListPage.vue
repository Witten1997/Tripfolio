<script setup lang="ts">
// 移动壳的旅行列表复用桌面业务页（两个壳共用 Element Plus 实现，见
// docs/architecture/2026-09-14-高德地图与行程路线.md 的「前端与安全代理」一节）。
// 主题中心、回收站与账号入口在移动壳顶栏（MobileShell），本文件只内嵌桌面列表，
// 并保留平台自检入口——此前该文件是只显示「服务端可用」与「主题中心」的自检占位页，
// 窄屏与安卓用户因此进不去任何旅行。
import { Cell as VanCell, CellGroup as VanCellGroup } from 'vant'

import DesktopTripList from '@/desktop/pages/TripListPage.vue'
import { platform } from '@/platform'

// 自检入口只在原生应用或开发模式显示
const showDevTools = platform.isNative || import.meta.env.DEV
</script>

<template>
  <div class="mobile-trip-list">
    <DesktopTripList />
    <VanCellGroup v-if="showDevTools" inset title="开发工具">
      <VanCell title="本地数据库自检" is-link :to="{ name: 'dev-local-db' }" />
    </VanCellGroup>
  </div>
</template>

<style scoped>
.mobile-trip-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px 0 24px;
}
/* 桌面列表自身不带外边距（桌面壳由 ElMain 提供），移动壳在这里补上与下方分组一致的 16px 边距。 */
.mobile-trip-list :deep(.trip-list-page) {
  padding: 0 16px;
}
</style>
