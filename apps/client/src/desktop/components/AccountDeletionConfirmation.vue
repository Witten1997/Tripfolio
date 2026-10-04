<script setup lang="ts">
import { ElAlert, ElButton, ElCheckbox, ElForm, ElFormItem, ElInput } from 'element-plus'
import { ref, watch } from 'vue'

const props = defineProps<{
  email: string
  busy: boolean
  error: string | null
  resetKey: number
}>()
const emit = defineEmits<{ submit: [password: string] }>()
const confirmed = ref(false)
const password = ref('')

watch(
  () => props.resetKey,
  () => {
    confirmed.value = false
    password.value = ''
  },
)

function submit() {
  if (props.busy || !confirmed.value || !password.value) return
  emit('submit', password.value)
  password.value = ''
}
</script>

<template>
  <section class="deletion-confirmation tf-surface" aria-labelledby="deletion-scope-title">
    <h2 id="deletion-scope-title">注销后，以下内容将永久删除</h2>
    <p class="deletion-account">当前账号：{{ email }}</p>
    <ul class="deletion-scope">
      <li>账号资料、头像和全部登录会话。</li>
      <li>全部旅行，包括归档旅行、回收站及关联的行程、行李、待办和账目。</li>
      <li>旅行照片、账单票据、预订、资料及附件，已创建的分享也将失效。</li>
    </ul>
    <ElAlert
      title="申请一经受理就无法撤销，数据不能恢复。"
      type="warning"
      :closable="false"
      show-icon
    />
    <p class="deletion-hint">
      受理后将退出登录。此浏览器会保存只用于查询注销进度的凭证，请保留浏览器数据以便查看处理结果。
    </p>
    <ElAlert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <ElForm label-position="top" :disabled="busy" @submit.prevent="submit">
      <ElFormItem>
        <ElCheckbox v-model="confirmed" class="deletion-checkbox">
          我已了解删除范围，确认注销当前账号，并理解申请受理后无法恢复。
        </ElCheckbox>
      </ElFormItem>
      <ElFormItem label="当前账号密码" required>
        <ElInput
          v-model="password"
          type="password"
          show-password
          autocomplete="current-password"
          maxlength="128"
          placeholder="验证身份后提交注销申请"
        />
      </ElFormItem>
      <div class="deletion-actions tf-actions">
        <ElButton
          type="danger"
          native-type="submit"
          :loading="busy"
          :disabled="!confirmed || !password"
        >
          验证密码并申请注销
        </ElButton>
        <RouterLink v-if="!busy" :to="{ name: 'account' }">返回账号</RouterLink>
      </div>
    </ElForm>
  </section>
</template>

<style scoped>
.deletion-confirmation {
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: clamp(18px, 4vw, 28px);
  border-radius: var(--tf-radius-card);
}
.deletion-confirmation h2 {
  margin: 0;
  font-size: 19px;
}
.deletion-account {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--tf-text-2);
}
.deletion-scope {
  margin: 0;
  padding-left: 22px;
  line-height: 1.9;
  color: var(--tf-text-2);
}
.deletion-scope li + li {
  margin-top: 8px;
}
.deletion-hint {
  margin: 0;
  font-size: 13px;
  line-height: 1.8;
  color: var(--tf-text-3);
}
.deletion-checkbox {
  height: auto;
  align-items: flex-start;
}
.deletion-checkbox :deep(.el-checkbox__input) {
  margin-top: 5px;
}
.deletion-checkbox :deep(.el-checkbox__label) {
  white-space: normal;
  line-height: 1.8;
}
.deletion-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 16px;
}
.deletion-actions a {
  color: var(--tf-text-2);
}
.deletion-actions a:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 4px;
}
</style>
