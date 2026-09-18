<script setup lang="ts">
import AccountPage from '@/desktop/pages/AccountPage.vue'

withDefaults(
  defineProps<{
    section?: 'profile' | 'password' | 'sessions'
  }>(),
  { section: 'profile' },
)
</script>

<template>
  <div class="mobile-account-section-page">
    <RouterLink class="mobile-account-section-page__back" :to="{ name: 'account' }">
      <span aria-hidden="true">←</span>
      <span>返回我的</span>
    </RouterLink>
    <AccountPage :section="section" />
  </div>
</template>

<style scoped>
.mobile-account-section-page {
  min-height: 100dvh;
  padding: 20px 16px 32px;
}

.mobile-account-section-page__back {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  margin: 0 0 12px 2px;
  color: var(--tf-text-2);
  font-size: 14px;
  font-weight: 600;
  text-decoration: none;
}

.mobile-account-section-page__back:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 3px;
}

.mobile-account-section-page__back span:first-child {
  font-size: 20px;
  line-height: 1;
}

.mobile-account-section-page :deep(.account-page) {
  width: 100%;
  gap: 14px;
}

.mobile-account-section-page :deep(.account-page__heading h1) {
  font-size: 25px;
}

.mobile-account-section-page :deep(.el-card) {
  border-radius: var(--tf-radius-card);
}

.mobile-account-section-page :deep(.el-card__body) {
  padding: 16px;
}

.mobile-account-section-page :deep(.account-form) {
  max-width: none;
}

.mobile-account-section-page :deep(.account-form .el-form-item) {
  display: block;
}

.mobile-account-section-page :deep(.account-form .el-form-item__label) {
  display: block;
  width: auto !important;
  height: auto;
  margin-bottom: 6px;
  line-height: 1.5;
  text-align: left;
}

.mobile-account-section-page :deep(.account-form .el-form-item__content) {
  margin-left: 0 !important;
}

.mobile-account-section-page :deep(.avatar-field) {
  flex-direction: column;
}

.mobile-account-section-page :deep(.session-item) {
  grid-template-columns: 40px minmax(0, 1fr);
}

.mobile-account-section-page :deep(.session-revoke) {
  grid-column: 2;
  justify-self: start;
}
</style>
