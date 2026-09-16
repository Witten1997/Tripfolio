<script setup lang="ts">
// 移动壳的旅行列表复用桌面业务页（两个壳共用 Element Plus 实现，见
// docs/architecture/2026-09-14-高德地图与行程路线.md 的「前端与安全代理」一节）。
// 底栏与账号入口由 MobileShell 提供，本文件复用旅行列表业务并补齐移动端布局，
// 同时保留原生应用与开发环境的平台自检入口。
import { Cell as VanCell, CellGroup as VanCellGroup } from 'vant'
import { nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import DesktopTripList from '@/desktop/pages/TripListPage.vue'
import { platform } from '@/platform'

// 自检入口只在原生应用或开发模式显示
const showDevTools = platform.isNative || import.meta.env.DEV
const route = useRoute()
const router = useRouter()
const desktopList = ref<{ openCreate: () => void }>()

watch(
  () => route.query.create,
  async (create) => {
    if (!create || route.name !== 'trips') return
    await nextTick()
    desktopList.value?.openCreate()
    const query = { ...route.query }
    delete query.create
    await router.replace({ name: 'trips', query })
  },
  { immediate: true },
)
</script>

<template>
  <div class="mobile-trip-list">
    <DesktopTripList ref="desktopList" />
    <VanCellGroup v-if="showDevTools" inset title="开发工具">
      <VanCell title="本地数据库自检" is-link :to="{ name: 'dev-local-db' }" />
    </VanCellGroup>
  </div>
</template>

<style scoped>
.mobile-trip-list {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: 20px 0 32px;
}

.mobile-trip-list :deep(.trip-list-page) {
  gap: 16px;
  padding: 0 18px;
}

.mobile-trip-list :deep(.page-heading) {
  padding: 2px 2px 4px;
}

.mobile-trip-list :deep(.page-heading h1) {
  font-size: 27px;
  line-height: 1.2;
}

.mobile-trip-list :deep(.page-heading p) {
  margin-top: 6px;
  font-size: 13px;
}

.mobile-trip-list :deep(.heading-actions) {
  display: flex;
  gap: 0;
}

.mobile-trip-list :deep(.heading-primary-actions) {
  display: none;
}

.mobile-trip-list :deep(.heading-tool-actions) {
  gap: 3px;
}

.mobile-trip-list :deep(.heading-tool-actions .icon-action) {
  width: 40px;
  height: 40px;
  min-height: 40px;
}

.mobile-trip-list :deep(.trip-tool-panel) {
  gap: 8px;
  padding: 10px;
}

.mobile-trip-list :deep(.filter-panel) {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.mobile-trip-list :deep(.trip-group h2) {
  margin-bottom: 12px;
}

.mobile-trip-list :deep(.trip-card) {
  overflow: hidden;
  border-radius: var(--tf-radius-card);
  box-shadow: var(--tf-shadow-1);
}

.mobile-trip-list :deep(.trip-card .el-card__body) {
  padding: 0;
}

.mobile-trip-list :deep(.trip-card h3) {
  font-size: 19px;
}

.mobile-trip-list :deep(.trip-dates) {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.mobile-trip-list :deep(.trip-actions) {
  align-items: center;
  justify-content: flex-start;
  margin-top: auto;
}

.mobile-trip-list :deep(.el-dialog) {
  width: calc(100vw - 24px);
  max-width: none;
  margin: 12px auto;
}

@media (max-width: 380px) {
  .mobile-trip-list :deep(.page-heading p) {
    display: none;
  }

  .mobile-trip-list :deep(.heading-tool-actions .icon-action) {
    width: 38px;
    height: 38px;
    min-height: 38px;
  }
}
</style>
