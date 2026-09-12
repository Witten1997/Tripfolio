<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElForm,
  ElFormItem,
  ElInput,
  type FormInstance,
} from 'element-plus'
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { ApiError, login } from '@/shared/api/auth'
import { isEmail } from '@/shared/auth/useEmailChallenge'

const router = useRouter()
const route = useRoute()
const formRef = ref<FormInstance>()
const form = reactive({ email: '', password: '' })
const submitting = ref(false)
const error = ref<string | null>(null)

const rules = {
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    {
      validator: (_: unknown, v: string, cb: (e?: Error) => void) =>
        cb(isEmail(v) ? undefined : new Error('邮箱格式不正确')),
      trigger: 'blur',
    },
  ],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function submit() {
  if (!formRef.value || !(await formRef.value.validate().catch(() => false))) return
  error.value = null
  submitting.value = true
  try {
    await login(form.email.trim(), form.password)
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : null
    await router.replace(redirect && redirect.startsWith('/') ? redirect : { name: 'trips' })
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '网络错误，请稍后再试'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="auth-page">
    <ElCard class="auth-card">
      <template #header>登录 Tripfolio</template>
      <ElAlert
        v-if="error"
        type="error"
        :title="error"
        :closable="false"
        show-icon
        class="auth-alert"
      />
      <ElForm
        ref="formRef"
        :model="form"
        :rules="rules"
        label-position="top"
        @submit.prevent="submit"
      >
        <ElFormItem label="邮箱" prop="email">
          <ElInput v-model="form.email" type="email" autocomplete="username" />
        </ElFormItem>
        <ElFormItem label="密码" prop="password">
          <ElInput
            v-model="form.password"
            type="password"
            show-password
            autocomplete="current-password"
            @keyup.enter="submit"
          />
        </ElFormItem>
        <ElButton type="primary" native-type="submit" :loading="submitting" class="auth-submit">
          登录
        </ElButton>
      </ElForm>
      <div class="auth-links">
        <RouterLink :to="{ name: 'register' }">注册账号</RouterLink>
        <RouterLink :to="{ name: 'reset-password' }">忘记密码</RouterLink>
      </div>
    </ElCard>
  </div>
</template>

<style scoped src="./auth.css"></style>
