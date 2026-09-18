<script setup lang="ts">
import RecycleBinPage from '@/desktop/pages/RecycleBinPage.vue'
</script>

<template>
  <div class="mobile-recycle-bin-page">
    <RecycleBinPage />
  </div>
</template>

<style scoped>
.mobile-recycle-bin-page {
  min-height: 100dvh;
  box-sizing: border-box;
  padding: 20px 16px 32px;
}

.mobile-recycle-bin-page :deep(.recycle-page) {
  width: 100%;
  gap: 14px;
}

.mobile-recycle-bin-page :deep(.recycle-heading) {
  gap: 16px;
  padding: 0;
}

.mobile-recycle-bin-page :deep(.recycle-heading h1) {
  font-size: 25px;
}

.mobile-recycle-bin-page :deep(.el-card) {
  border-radius: var(--tf-radius-card);
}

.mobile-recycle-bin-page :deep(.el-card__body) {
  padding: 16px;
}

.mobile-recycle-bin-page :deep(.el-table) {
  width: 100%;
}

@media (max-width: 600px) {
  .mobile-recycle-bin-page :deep(.recycle-table) {
    overflow-x: auto;
  }
}
</style>
