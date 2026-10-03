<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { request } from '../api'
import type { components } from '@tripfolio/contracts/openapi/admin'

type SiteSettings = components['schemas']['SiteSettings']
const settings = ref<SiteSettings | null>(null)
const address = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref('')

async function load() {
  if (loading.value || saving.value) return
  loading.value = true
  error.value = ''
  try {
    settings.value = (await request<{ data: SiteSettings }>('/site-settings')).data
    address.value = settings.value.share_base_url
  } catch (e) {
    error.value = e instanceof Error ? e.message : '站点设置加载失败'
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!settings.value || saving.value || loading.value) return
  saving.value = true
  error.value = ''
  try {
    settings.value = (
      await request<{ data: SiteSettings }>('/site-settings', {
        method: 'PUT',
        body: JSON.stringify({ share_base_url: address.value, version: settings.value.version }),
      })
    ).data
    address.value = settings.value.share_base_url
    ElMessage.success('分享站点地址已保存')
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="account-page site-settings-page" aria-labelledby="site-settings-title">
    <header class="page-heading">
      <div>
        <h1 id="site-settings-title">站点设置</h1>
        <p class="muted">设置旅行分享链接使用的用户端地址。</p>
      </div>
      <el-button :loading="loading" :disabled="saving" @click="load">刷新</el-button>
    </header>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon role="alert" />
    <p v-if="loading && !settings" role="status">正在加载站点设置…</p>
    <form v-if="settings" class="settings-form" @submit.prevent="save">
      <label for="share-base-url">旅行分享站点地址</label>
      <el-input
        id="share-base-url"
        v-model="address"
        type="url"
        maxlength="2048"
        required
        :disabled="saving || loading"
        placeholder="https://trip.example.com"
        aria-describedby="share-url-hint"
      />
      <p id="share-url-hint">
        填写用户端的协议、域名和端口，不包含路径。保存后立即生效，之后获取的分享链接使用此地址。
      </p>
      <p v-if="!settings.share_base_url" class="unconfigured">
        尚未配置，用户暂时无法生成分享链接。
      </p>
      <el-button
        type="primary"
        native-type="submit"
        :loading="saving"
        :disabled="loading || !address.trim()"
        >保存设置</el-button
      >
    </form>
  </section>
</template>

<style scoped>
.settings-form {
  max-width: 640px;
  display: grid;
  gap: 16px;
  margin-top: 24px;
}
.settings-form label {
  font-weight: 600;
}
.settings-form p {
  margin: 0;
  color: var(--el-text-color-secondary);
  line-height: 1.7;
}
.settings-form .el-button {
  justify-self: start;
}
.settings-form .unconfigured {
  color: var(--el-color-warning-dark-2);
}
</style>
