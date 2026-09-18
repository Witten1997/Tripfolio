<script setup lang="ts">
import AccountPage from './AccountPage.vue'

withDefaults(
  defineProps<{
    section?: 'profile' | 'password' | 'sessions'
  }>(),
  { section: 'profile' },
)
</script>

<template>
  <div class="account-section-page">
    <RouterLink class="account-section-page__back" :to="{ name: 'account' }">
      <span aria-hidden="true">←</span>
      <span>返回我的</span>
    </RouterLink>
    <AccountPage :section="section" />
  </div>
</template>

<style scoped>
.account-section-page {
  display: flex;
  box-sizing: border-box;
  width: min(100%, 900px);
  max-width: 900px;
  margin: 0 auto;
  flex-direction: column;
  gap: 12px;
}

.account-section-page__back {
  display: inline-flex;
  width: fit-content;
  align-items: center;
  gap: 7px;
  color: var(--tf-text-2);
  font-size: 13px;
  font-weight: 600;
  text-decoration: none;
  transition: color var(--tf-duration-fast) var(--tf-ease);
}

.account-section-page__back:hover {
  color: var(--tf-accent);
}

.account-section-page__back:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 3px;
}

.account-section-page__back span:first-child {
  font-size: 18px;
  line-height: 1;
}
</style>
