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
  gap: 24px;
  padding: 30px 0 40px;
}

.mobile-trip-list :deep(.trip-list-page) {
  gap: 20px;
  padding: 0 18px;
}

.mobile-trip-list :deep(.page-heading) {
  padding: 2px 2px 6px;
}

.mobile-trip-list :deep(.page-heading h1) {
  font-size: 34px;
  line-height: 1.08;
}

.mobile-trip-list :deep(.page-heading p) {
  margin-top: 9px;
  font-size: 14px;
}

.mobile-trip-list :deep(.heading-actions) {
  display: flex;
  gap: 0;
}

.mobile-trip-list :deep(.heading-primary-actions) {
  display: none;
}

.mobile-trip-list :deep(.heading-tool-actions) {
  gap: 2px;
}

.mobile-trip-list :deep(.heading-tool-actions .icon-action) {
  width: 42px;
  height: 42px;
  min-height: 42px;
}

.mobile-trip-list :deep(.trip-tool-panel) {
  gap: 8px;
  padding: 10px;
}

.mobile-trip-list :deep(.filter-panel) {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.mobile-trip-list :deep(.trip-group h2) {
  margin-bottom: 14px;
}

.mobile-trip-list :deep(.trip-card) {
  overflow: hidden;
  border-radius: var(--tf-radius-card);
}

.mobile-trip-list :deep(.trip-card .el-card__body) {
  padding: 0;
}

.mobile-trip-list :deep(.trip-card h3) {
  font-size: 20px;
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
  .mobile-trip-list {
    padding-top: 24px;
  }

  .mobile-trip-list :deep(.page-heading h1) {
    font-size: 27px;
  }

  .mobile-trip-list :deep(.page-heading) {
    gap: 10px;
  }

  .mobile-trip-list :deep(.page-title-row) {
    gap: 6px;
  }

  .mobile-trip-list :deep(.page-title-plane) {
    width: 22px;
    height: 22px;
  }

  .mobile-trip-list :deep(.heading-tool-actions .icon-action) {
    width: 36px;
    height: 36px;
    min-height: 36px;
  }
}
</style>
