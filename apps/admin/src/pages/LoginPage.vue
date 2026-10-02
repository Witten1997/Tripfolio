<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ShieldCheck, ArrowRight } from '@lucide/vue'
import { connectionError, login } from '../api'

const router = useRouter()
const route = useRoute()
const email = ref('')
const password = ref('')
const busy = ref(false)
const errorMessage = ref('')
async function submit() {
  if (busy.value) return
  busy.value = true
  errorMessage.value = ''
  try {
    await login(email.value.trim(), password.value)
    password.value = ''
    await router.replace({ name: 'overview' })
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '登录失败，请重试。'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <section class="login-brand" aria-label="Tripfolio">
      <a href="/" class="brand"
        ><ShieldCheck :size="32" aria-hidden="true" /><span>Tripfolio</span></a
      >
      <div class="login-intro">
        <p class="eyebrow">ADMINISTRATION</p>
        <h1>管理每一段<br />旅程的背后。</h1>
        <p>Tripfolio 管理后台</p>
      </div>
      <span class="login-brand-footer">让每段旅行，妥善珍藏。</span>
    </section>
    <section class="login-form-area">
      <form class="login-form" @submit.prevent="submit">
        <span class="form-icon"><ShieldCheck :size="24" aria-hidden="true" /></span>
        <h2>登录管理后台</h2>
        <p class="muted">使用已授权的超级管理员账号登录。</p>
        <p v-if="route.query.setup === 'complete'" class="notice-strip" role="status">
          管理员已设置，初始化入口已关闭。请使用刚设置的账号登录。
        </p>
        <div v-if="errorMessage || connectionError" class="form-error" role="alert">
          {{ errorMessage || connectionError }}
        </div>
        <label for="admin-email">邮箱</label>
        <el-input
          id="admin-email"
          v-model="email"
          type="email"
          autocomplete="username"
          placeholder="请输入账号邮箱"
          size="large"
          required
          :maxlength="254"
          :disabled="busy"
        />
        <label for="admin-password">密码</label>
        <el-input
          id="admin-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          placeholder="请输入密码"
          show-password
          size="large"
          required
          :maxlength="128"
          :disabled="busy"
        />
        <el-button
          class="login-submit"
          type="primary"
          size="large"
          native-type="submit"
          :loading="busy"
          >登录后台<ArrowRight v-if="!busy" :size="17" aria-hidden="true"
        /></el-button>
        <a class="back-link" href="/">返回 Tripfolio</a>
      </form>
    </section>
  </main>
</template>
