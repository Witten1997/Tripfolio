<script setup lang="ts">
import { Button as VanButton, Cell as VanCell, CellGroup as VanCellGroup } from 'vant'
import { useRouter } from 'vue-router'

import { logout } from '@/shared/api/account'

const router = useRouter()

async function signOut() {
  await logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="mobile-account-page">
    <header class="mobile-account-page__heading">
      <h1>我的</h1>
    </header>
    <VanCellGroup inset title="旅行设置" class="mobile-account-page__links">
      <VanCell title="主题" label="切换界面外观" is-link :to="{ name: 'themes' }" />
      <VanCell title="回收站" label="恢复或清理已删除的旅行" is-link :to="{ name: 'recycle-bin' }" />
      <VanCell title="个人设置" label="管理头像、昵称和默认时区" is-link :to="{ name: 'personal-settings' }" />
      <VanCell title="修改密码" label="更新账号密码" is-link :to="{ name: 'change-password' }" />
      <VanCell title="登录设备管理" label="查看并撤销已登录的设备" is-link :to="{ name: 'login-devices' }" />
    </VanCellGroup>
    <div class="mobile-account-page__logout tf-surface">
      <VanButton type="danger" plain block @click="signOut">退出登录</VanButton>
    </div>
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

.mobile-account-page__logout {
  padding: 14px 16px;
  border-radius: var(--tf-radius-card);
}

</style>
