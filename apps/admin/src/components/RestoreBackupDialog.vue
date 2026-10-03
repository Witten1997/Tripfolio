<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import type { components } from '@tripfolio/contracts/openapi/admin'
import { reauthenticate, request } from '../api'
import { saveRestore, type RestoreJob } from '../restore-state'

const props = defineProps<{
  modelValue: boolean
  run: components['schemas']['BackupRun'] | components['schemas']['RemoteBackup'] | null
  remote?: { destination: string; settingsVersion: number }
}>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const router = useRouter()
const confirmed = ref(false)
const confirmation = ref('')
const password = ref('')
const archivePassword = ref('')
const busy = ref(false)
const error = ref('')
watch(
  () => props.modelValue,
  () => {
    confirmed.value = false
    confirmation.value = password.value = archivePassword.value = error.value = ''
  },
)
function snapshot() {
  return props.run?.snapshot_at
    ? new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'medium',
        timeZone: 'Asia/Shanghai',
      }).format(new Date(props.run.snapshot_at))
    : '所选备份时间'
}
async function submit() {
  if (busy.value || !props.run) return
  if (!confirmed.value || confirmation.value !== '确认覆盖当前数据库' || !password.value) {
    error.value = '请勾选覆盖确认、输入“确认覆盖当前数据库”，并填写管理员密码。'
    return
  }
  busy.value = true
  error.value = ''
  try {
    await reauthenticate(password.value)
    password.value = ''
    const path = props.remote ? '/backup-remote/restore' : `/backups/${props.run.id}/restore`
    const { data } = await request<{ data: RestoreJob }>(path, {
      method: 'POST',
      body: JSON.stringify({
        confirmation: confirmation.value,
        password: archivePassword.value,
        reason: '确认使用备份覆盖当前数据库',
        ...(props.remote
          ? {
              id: props.run.id,
              destination: props.remote.destination,
              sha256: props.run.sha256,
              settings_version: props.remote.settingsVersion,
            }
          : {}),
      }),
    })
    archivePassword.value = ''
    if (!data.token) throw new Error('恢复已受理，但未返回进度查询凭证，请检查后台状态。')
    saveRestore({ id: data.id, token: data.token })
    emit('update:modelValue', false)
    await router.replace({ name: 'restore' })
  } catch (e) {
    error.value = e instanceof Error ? e.message : '恢复提交失败，请重试。'
  } finally {
    password.value = ''
    busy.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    title="恢复并覆盖当前数据库"
    width="min(560px, 94vw)"
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @update:model-value="!busy && emit('update:modelValue', $event)"
  >
    <el-alert
      title="当前数据库的现有数据将被所选备份替换"
      type="error"
      :closable="false"
      show-icon
    />
    <p class="restore-warning">
      数据将回到
      {{
        snapshot()
      }}（北京时间）。备份之后新增、修改或删除的数据都会被覆盖，无法自动找回。请先备份当前数据。
    </p>
    <p class="restore-note">
      恢复期间暂停业务操作，失败时回滚；成功后所有用户需要重新登录，请使用备份时的账号和密码。保留当前备份设置、备份历史及审计记录，自动备份会关闭。照片和附件等存储文件不会恢复。
    </p>
    <p v-if="remote" class="restore-warning">
      来源目录：tripfolio-{{
        remote.destination
      }}。当前系统新建的管理员账号也会被替换，请确认掌握原系统管理员的账号和密码。旧版备份未包含站点设置时，恢复后需重新设置分享站点地址。
    </p>
    <el-form label-position="top" @submit.prevent="submit">
      <el-form-item label="备份加密密码（可选）">
        <el-input
          v-model="archivePassword"
          type="password"
          autocomplete="new-password"
          show-password
          :maxlength="1024"
          :disabled="busy"
          placeholder="留空使用服务器当前配置；旧备份填写当时的密码"
          aria-label="备份加密密码"
        />
      </el-form-item>
      <el-form-item label="当前管理员密码">
        <el-input
          v-model="password"
          type="password"
          autocomplete="current-password"
          show-password
          :disabled="busy"
          aria-label="当前管理员密码"
        />
      </el-form-item>
      <el-form-item label="输入“确认覆盖当前数据库”">
        <el-input
          v-model="confirmation"
          :disabled="busy"
          autocomplete="off"
          aria-label="覆盖确认文字"
        />
      </el-form-item>
      <el-checkbox v-model="confirmed" :disabled="busy"
        >我已知晓：当前数据库的现有数据将被替换</el-checkbox
      >
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <div class="restore-actions">
        <el-button :disabled="busy" @click="emit('update:modelValue', false)">取消</el-button>
        <el-button
          type="danger"
          native-type="submit"
          :loading="busy"
          :disabled="!confirmed || confirmation !== '确认覆盖当前数据库' || !password"
          >确认覆盖并恢复</el-button
        >
      </div>
    </el-form>
  </el-dialog>
</template>

<style scoped>
.restore-warning,
.restore-note {
  line-height: 1.8;
  margin: 16px 0;
}
.restore-note {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.restore-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 24px;
}
:deep(.el-checkbox) {
  height: auto;
  white-space: normal;
}
:deep(.el-checkbox__label) {
  white-space: normal;
  line-height: 1.7;
}
</style>
