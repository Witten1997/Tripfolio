<script setup lang="ts">
import { ElAlert, ElButton, ElSkeleton } from 'element-plus'
import { onMounted } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import AccountDeletionConfirmation from '@/desktop/components/AccountDeletionConfirmation.vue'
import { useAccountDeletion } from '@/shared/deletion/useAccountDeletion'
import { readDeletion } from '@/shared/deletion/storage'
import { useSessionStore } from '@/shared/stores/session'

const flow = useAccountDeletion()
const { loading, busy, error, resetKey, pending, canRecover } = flow
const session = useSessionStore()
const router = useRouter()
onBeforeRouteLeave(() => !busy.value)
onMounted(async () => {
  if (readDeletion()) {
    await router.replace({ name: 'account-deletion-progress' })
    return
  }
  await flow.load()
})
async function submit(password: string) {
  const saved = await flow.submit(password)
  if (saved) await router.replace({ name: 'account-deletion-progress' })
}
async function recover() {
  if (await flow.recover()) await router.replace({ name: 'account-deletion-progress' })
}
</script>

<template>
  <div class="account-deletion-page">
    <header>
      <h1>注销账号</h1>
      <p>请先核对删除范围，再验证身份。</p>
    </header>
    <ElSkeleton v-if="loading" :rows="6" animated />
    <section v-else-if="pending" class="account-deletion-recovery tf-surface">
      <ElAlert :title="error || '正在保存查询凭证'" type="warning" :closable="false" show-icon />
      <p>申请可能已经受理。请保留此页面，使用原有效会话恢复任务并保存查询凭证。</p>
      <ElButton v-if="canRecover" type="primary" :loading="busy" @click="recover"
        >恢复任务并保存查询凭证</ElButton
      >
      <p v-else>原登录会话已失效，无法补领。已受理的注销申请仍会继续执行。</p>
    </section>
    <AccountDeletionConfirmation
      v-else-if="session.account"
      :email="session.account.email"
      :busy="busy"
      :error="error"
      :reset-key="resetKey"
      @submit="submit"
    />
    <ElAlert v-else title="无法加载账号信息，请重新登录后再操作。" type="error" :closable="false" />
  </div>
</template>

<style scoped>
.account-deletion-page {
  box-sizing: border-box;
  width: min(100%, 760px);
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 24px;
}
.account-deletion-page h1 {
  margin: 0;
  font-size: 28px;
}
.account-deletion-page header p {
  margin: 10px 0 0;
  color: var(--tf-text-3);
}
.account-deletion-recovery {
  padding: 24px;
  border-radius: var(--tf-radius-card);
  line-height: 1.8;
}
@media (max-width: 767px) {
  .account-deletion-page {
    padding: 20px 16px 32px;
  }
}
</style>
