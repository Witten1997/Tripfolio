<script setup lang="ts">
import { Cell as VanCell, CellGroup as VanCellGroup } from 'vant'

import DesktopAccountPage from '@/desktop/pages/AccountPage.vue'
</script>

<template>
  <div class="mobile-account-page">
    <header class="mobile-account-page__heading">
      <h1>我的</h1>
    </header>
    <VanCellGroup inset title="旅行设置" class="mobile-account-page__links">
      <VanCell title="主题" label="切换界面外观" is-link :to="{ name: 'themes' }" />
      <VanCell title="回收站" label="恢复或清理已删除的旅行" is-link :to="{ name: 'recycle-bin' }" />
    </VanCellGroup>
    <DesktopAccountPage />
  </div>
</template>

<style scoped>
.mobile-account-page {
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: 20px 16px 32px;
}

.mobile-account-page__heading {
  padding-inline: 2px;
}

.mobile-account-page__heading h1 {
  margin: 0;
  color: var(--tf-text-1);
  font-size: 27px;
  line-height: 1.2;
}

.mobile-account-page__links {
  margin: 0;
}

.mobile-account-page :deep(.account-page) {
  width: 100%;
  gap: 14px;
}

.mobile-account-page :deep(.el-card) {
  border-radius: var(--tf-radius-card);
}

.mobile-account-page :deep(.el-card__body) {
  padding: 16px;
}

.mobile-account-page :deep(.account-form) {
  max-width: none;
}

.mobile-account-page :deep(.account-form .el-form-item) {
  display: block;
}

.mobile-account-page :deep(.account-form .el-form-item__label) {
  display: block;
  width: auto !important;
  height: auto;
  margin-bottom: 6px;
  line-height: 1.5;
  text-align: left;
}

.mobile-account-page :deep(.account-form .el-form-item__content) {
  margin-left: 0 !important;
}

.mobile-account-page :deep(.avatar-field) {
  flex-direction: column;
}

.mobile-account-page :deep(.session-item) {
  grid-template-columns: 40px minmax(0, 1fr);
}

.mobile-account-page :deep(.session-revoke) {
  grid-column: 2;
  justify-self: start;
}
</style>
