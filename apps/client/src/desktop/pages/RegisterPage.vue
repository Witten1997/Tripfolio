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
import { useRouter } from 'vue-router'

import { ApiError, register } from '@/shared/api/auth'
import {
  isEmail,
  isSixDigits,
  isValidPassword,
  useEmailChallenge,
} from '@/shared/auth/useEmailChallenge'

const router = useRouter()
const formRef = ref<FormInstance>()
const form = reactive({ email: '', code: '', nickname: '', password: '', confirm: '' })
const submitting = ref(false)
const error = ref<string | null>(null)
const challenge = useEmailChallenge('register')

type Validator = (rule: unknown, value: string, cb: (e?: Error) => void) => void
const check =
  (ok: (v: string) => boolean, message: string): Validator =>
  (_, v, cb) =>
    cb(ok(v) ? undefined : new Error(message))

const rules = {
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    { validator: check(isEmail, '邮箱格式不正确'), trigger: 'blur' },
  ],
  code: [{ validator: check(isSixDigits, '请输入 6 位数字验证码'), trigger: 'blur' }],
  nickname: [
    { required: true, message: '请输入昵称', trigger: 'blur' },
    { max: 64, message: '昵称最多 64 个字符', trigger: 'blur' },
  ],
  password: [{ validator: check(isValidPassword, '密码长度须为 10–128 个字符'), trigger: 'blur' }],
  confirm: [
    { validator: check((v) => v === form.password, '两次输入的密码不一致'), trigger: 'blur' },
  ],
}

async function sendCode() {
  if (!formRef.value) return
  const ok = await formRef.value.validateField('email').catch(() => false)
  if (!ok) return
  await challenge.send(form.email.trim())
}

async function submit() {
  if (!formRef.value || !(await formRef.value.validate().catch(() => false))) return
  if (!challenge.challengeId.value) {
    error.value = '请先获取邮箱验证码'
    return
  }
  error.value = null
  submitting.value = true
  try {
    await register({
      challengeId: challenge.challengeId.value,
      email: form.email.trim(),
      code: form.code,
      password: form.password,
      nickname: form.nickname.trim(),
    })
    await router.replace({ name: 'trips' })
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
      <template #header>注册账号</template>
      <ElAlert
        v-if="error || challenge.error.value"
        type="error"
        :title="error ?? challenge.error.value ?? ''"
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
        <ElFormItem label="邮箱验证码" prop="code">
          <div class="auth-code-row">
            <ElInput
              v-model="form.code"
              maxlength="6"
              inputmode="numeric"
              autocomplete="one-time-code"
            />
            <ElButton
              :disabled="!challenge.canSend.value"
              :loading="challenge.sending.value"
              @click="sendCode"
            >
              {{
                challenge.secondsLeft.value > 0
                  ? `${challenge.secondsLeft.value} 秒后重发`
                  : '获取验证码'
              }}
            </ElButton>
          </div>
        </ElFormItem>
        <ElFormItem label="昵称" prop="nickname">
          <ElInput v-model="form.nickname" maxlength="64" autocomplete="nickname" />
        </ElFormItem>
        <ElFormItem label="密码（10–128 个字符）" prop="password">
          <ElInput
            v-model="form.password"
            type="password"
            show-password
            autocomplete="new-password"
          />
        </ElFormItem>
        <ElFormItem label="确认密码" prop="confirm">
          <ElInput
            v-model="form.confirm"
            type="password"
            show-password
            autocomplete="new-password"
          />
        </ElFormItem>
        <ElButton type="primary" native-type="submit" :loading="submitting" class="auth-submit">
          注册并登录
        </ElButton>
      </ElForm>
      <div class="auth-links">
        <RouterLink :to="{ name: 'login' }">已有账号，去登录</RouterLink>
      </div>
    </ElCard>
  </div>
</template>

<style scoped src="./auth.css"></style>
