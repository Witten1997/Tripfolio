<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ShieldCheck, ArrowRight } from '@lucide/vue'
import { AdminApiError, checkSetup, initializeAdmin } from '../api'

const router = useRouter()
const mode = ref<'create' | 'existing'>('create')
const email = ref('')
const nickname = ref('')
const password = ref('')
const confirmation = ref('')
const busy = ref(false)
const errorMessage = ref('')

async function submit() {
  if (busy.value) return
  errorMessage.value = ''
  if (password.value !== confirmation.value) {
    errorMessage.value = '两次输入的密码不一致。'
    return
  }
  busy.value = true
  try {
    await initializeAdmin({
      mode: mode.value,
      email: email.value.trim(),
      password: password.value,
      ...(mode.value === 'create' ? { nickname: nickname.value.trim() } : {}),
    })
    password.value = confirmation.value = ''
    await router.replace({ name: 'login', query: { setup: 'complete' } })
  } catch (error) {
    password.value = confirmation.value = ''
    if (error instanceof AdminApiError && error.status === 404) {
      await checkSetup(true).catch(() => undefined)
      await router.replace({ name: 'login' })
    } else {
      errorMessage.value = error instanceof Error ? error.message : '初始化未完成，请重试。'
    }
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
        <p class="eyebrow">WELCOME</p>
        <h1>从这里，<br />开始管理。</h1>
        <p>Tripfolio 管理后台</p>
      </div>
      <span class="login-brand-footer">让每段旅行，妥善珍藏。</span>
    </section>
    <section class="login-form-area">
      <form class="login-form setup-form" @submit.prevent="submit">
        <span class="form-icon"><ShieldCheck :size="24" aria-hidden="true" /></span>
        <h2>设置首位管理员</h2>
        <p class="muted">设置一次，即可开始使用。完成后将永久关闭初始化入口。</p>
        <div v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</div>
        <el-radio-group v-model="mode" aria-label="管理员账号来源" :disabled="busy">
          <el-radio-button value="create">创建新账号</el-radio-button>
          <el-radio-button value="existing">使用已有账号</el-radio-button>
        </el-radio-group>
        <label for="setup-email">管理员邮箱</label>
        <el-input
          id="setup-email"
          v-model="email"
          type="email"
          autocomplete="username"
          placeholder="请输入邮箱"
          size="large"
          required
          :maxlength="254"
          :disabled="busy"
        />
        <template v-if="mode === 'create'">
          <label for="setup-nickname">昵称</label>
          <el-input
            id="setup-nickname"
            v-model="nickname"
            autocomplete="nickname"
            placeholder="请输入昵称"
            size="large"
            required
            :maxlength="64"
            :disabled="busy"
          />
        </template>
        <label for="setup-password">{{ mode === 'create' ? '设置密码' : '账号当前密码' }}</label>
        <el-input
          id="setup-password"
          v-model="password"
          type="password"
          :autocomplete="mode === 'create' ? 'new-password' : 'current-password'"
          placeholder="8–128 个字符"
          show-password
          size="large"
          required
          :minlength="8"
          :maxlength="128"
          :disabled="busy"
        />
        <label for="setup-confirmation">确认密码</label>
        <el-input
          id="setup-confirmation"
          v-model="confirmation"
          type="password"
          :autocomplete="mode === 'create' ? 'new-password' : 'current-password'"
          placeholder="请再次输入密码"
          show-password
          size="large"
          required
          :minlength="8"
          :maxlength="128"
          :disabled="busy"
        />
        <p v-if="mode === 'existing'" class="muted">
          验证当前密码后授予管理权限，保留已有资料和密码。
        </p>
        <el-button
          class="login-submit"
          type="primary"
          size="large"
          native-type="submit"
          :loading="busy"
        >
          完成初始化<ArrowRight v-if="!busy" :size="17" aria-hidden="true" />
        </el-button>
        <a class="back-link" href="/">返回 Tripfolio</a>
      </form>
    </section>
  </main>
</template>

<style scoped>
.setup-form {
  padding-block: 32px;
}
</style>
