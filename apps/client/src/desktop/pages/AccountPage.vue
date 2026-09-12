<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElDescriptions,
  ElDescriptionsItem,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElPopconfirm,
  ElTable,
  ElTableColumn,
  ElTag,
  type FormInstance,
} from 'element-plus'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'

import {
  changePassword,
  fetchAccount,
  listSessions,
  logout,
  revokeSession,
  updateAccount,
  type Session,
} from '@/shared/api/account'
import { ApiError } from '@/shared/api/auth'
import { isValidPassword } from '@/shared/auth/useEmailChallenge'
import { useSessionStore } from '@/shared/stores/session'

const router = useRouter()
const session = useSessionStore()
const account = computed(() => session.account)

const loadError = ref<string | null>(null)

// 资料
const profileRef = ref<FormInstance>()
const profile = reactive({ nickname: '', default_timezone: '' })
const savingProfile = ref(false)
const profileRules = {
  nickname: [
    { required: true, message: '请输入昵称', trigger: 'blur' },
    { max: 64, message: '昵称最多 64 个字符', trigger: 'blur' },
  ],
  default_timezone: [
    { required: true, message: '请输入 IANA 时区，例如 Asia/Shanghai', trigger: 'blur' },
  ],
}

function fillProfile() {
  if (!account.value) return
  profile.nickname = account.value.nickname
  profile.default_timezone = account.value.default_timezone
}

const profileDirty = computed(
  () =>
    !!account.value &&
    (profile.nickname.trim() !== account.value.nickname ||
      profile.default_timezone.trim() !== account.value.default_timezone),
)

async function saveProfile() {
  if (!account.value || !profileRef.value) return
  if (!(await profileRef.value.validate().catch(() => false))) return
  savingProfile.value = true
  try {
    const patch: { nickname?: string; default_timezone?: string } = {}
    if (profile.nickname.trim() !== account.value.nickname) patch.nickname = profile.nickname.trim()
    if (profile.default_timezone.trim() !== account.value.default_timezone) {
      patch.default_timezone = profile.default_timezone.trim()
    }
    await updateAccount(account.value.version, patch)
    ElMessage.success('资料已保存')
  } catch (cause) {
    if (cause instanceof ApiError && cause.code === 'VERSION_CONFLICT') {
      ElMessage.warning('资料已在别处修改，已重新加载')
      await fetchAccount()
      fillProfile()
    } else {
      ElMessage.error(cause instanceof ApiError ? cause.message : '网络错误，请稍后再试')
    }
  } finally {
    savingProfile.value = false
  }
}

// 密码
const passwordRef = ref<FormInstance>()
const password = reactive({ current: '', next: '', confirm: '' })
const savingPassword = ref(false)
type Validator = (rule: unknown, value: string, cb: (e?: Error) => void) => void
const check =
  (ok: (v: string) => boolean, message: string): Validator =>
  (_, v, cb) =>
    cb(ok(v) ? undefined : new Error(message))
const passwordRules = {
  current: [{ required: true, message: '请输入当前密码', trigger: 'blur' }],
  next: [{ validator: check(isValidPassword, '密码长度须为 10–128 个字符'), trigger: 'blur' }],
  confirm: [
    { validator: check((v) => v === password.next, '两次输入的密码不一致'), trigger: 'blur' },
  ],
}

async function savePassword() {
  if (!passwordRef.value || !(await passwordRef.value.validate().catch(() => false))) return
  savingPassword.value = true
  try {
    await changePassword(password.current, password.next)
    password.current = password.next = password.confirm = ''
    passwordRef.value.resetFields()
    ElMessage.success('密码已修改，其他设备已退出登录')
    await loadSessions()
  } catch (cause) {
    ElMessage.error(cause instanceof ApiError ? cause.message : '网络错误，请稍后再试')
  } finally {
    savingPassword.value = false
  }
}

// 会话
const sessions = ref<Session[]>([])
const loadingSessions = ref(false)

async function loadSessions() {
  loadingSessions.value = true
  try {
    sessions.value = await listSessions()
  } catch (cause) {
    ElMessage.error(cause instanceof ApiError ? cause.message : '无法加载会话列表')
  } finally {
    loadingSessions.value = false
  }
}

async function revoke(row: unknown) {
  const s = row as Session
  try {
    await revokeSession(s.id)
    ElMessage.success('已撤销')
    await loadSessions()
  } catch (cause) {
    ElMessage.error(cause instanceof ApiError ? cause.message : '撤销失败')
  }
}

const clientKindLabel: Record<Session['client_kind'], string> = {
  web: '网页',
  android: '安卓',
  harmony: '鸿蒙',
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

async function signOut() {
  await logout()
  await router.replace({ name: 'login' })
}

onMounted(async () => {
  try {
    await fetchAccount()
    fillProfile()
    await loadSessions()
  } catch (cause) {
    loadError.value = cause instanceof ApiError ? cause.message : '无法加载账号信息'
  }
})
</script>

<template>
  <div class="account-page">
    <ElAlert v-if="loadError" type="error" :title="loadError" :closable="false" show-icon />
    <template v-else-if="account">
      <ElCard>
        <template #header>账号</template>
        <ElDescriptions :column="2" border>
          <ElDescriptionsItem label="邮箱">{{ account.email }}</ElDescriptionsItem>
          <ElDescriptionsItem label="注册时间">{{
            formatTime(account.created_at)
          }}</ElDescriptionsItem>
        </ElDescriptions>
        <ElForm
          ref="profileRef"
          :model="profile"
          :rules="profileRules"
          label-width="100px"
          class="account-form"
          @submit.prevent="saveProfile"
        >
          <ElFormItem label="昵称" prop="nickname">
            <ElInput v-model="profile.nickname" maxlength="64" />
          </ElFormItem>
          <ElFormItem label="默认时区" prop="default_timezone">
            <ElInput
              v-model="profile.default_timezone"
              maxlength="64"
              placeholder="Asia/Shanghai"
            />
          </ElFormItem>
          <ElFormItem>
            <ElButton
              type="primary"
              native-type="submit"
              :loading="savingProfile"
              :disabled="!profileDirty"
            >
              保存资料
            </ElButton>
            <ElButton :disabled="!profileDirty" @click="fillProfile">还原</ElButton>
          </ElFormItem>
        </ElForm>
      </ElCard>

      <ElCard>
        <template #header>修改密码</template>
        <ElForm
          ref="passwordRef"
          :model="password"
          :rules="passwordRules"
          label-width="100px"
          class="account-form"
          @submit.prevent="savePassword"
        >
          <ElFormItem label="当前密码" prop="current">
            <ElInput
              v-model="password.current"
              type="password"
              show-password
              autocomplete="current-password"
            />
          </ElFormItem>
          <ElFormItem label="新密码" prop="next">
            <ElInput
              v-model="password.next"
              type="password"
              show-password
              autocomplete="new-password"
            />
          </ElFormItem>
          <ElFormItem label="确认新密码" prop="confirm">
            <ElInput
              v-model="password.confirm"
              type="password"
              show-password
              autocomplete="new-password"
            />
          </ElFormItem>
          <ElFormItem>
            <ElButton type="primary" native-type="submit" :loading="savingPassword"
              >修改密码</ElButton
            >
            <span class="account-hint">修改后其他设备将退出登录，本设备保持登录。</span>
          </ElFormItem>
        </ElForm>
      </ElCard>

      <ElCard>
        <template #header>
          <div class="account-card-header">
            <span>登录设备</span>
            <ElButton size="small" :loading="loadingSessions" @click="loadSessions">刷新</ElButton>
          </div>
        </template>
        <ElTable :data="sessions" v-loading="loadingSessions" empty-text="暂无会话">
          <ElTableColumn label="设备" min-width="220">
            <template #default="{ row }">
              {{ row.device_name || '未命名设备' }}
              <ElTag v-if="row.is_current" size="small" type="success" class="account-tag"
                >当前</ElTag
              >
            </template>
          </ElTableColumn>
          <ElTableColumn label="类型" width="90">
            <template #default="{ row }">{{
              clientKindLabel[row.client_kind as Session['client_kind']]
            }}</template>
          </ElTableColumn>
          <ElTableColumn label="最近活动" width="180">
            <template #default="{ row }">{{ formatTime(row.last_seen_at) }}</template>
          </ElTableColumn>
          <ElTableColumn label="登录时间" width="180">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </ElTableColumn>
          <ElTableColumn width="100">
            <template #default="{ row }">
              <ElPopconfirm
                v-if="!row.is_current"
                title="撤销该设备的登录？"
                @confirm="revoke(row)"
              >
                <template #reference>
                  <ElButton size="small" type="danger" link>撤销</ElButton>
                </template>
              </ElPopconfirm>
            </template>
          </ElTableColumn>
        </ElTable>
      </ElCard>

      <ElCard>
        <ElButton type="danger" plain @click="signOut">退出登录</ElButton>
      </ElCard>
    </template>
  </div>
</template>

<style scoped>
.account-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 900px;
  margin: 0 auto;
}

.account-form {
  max-width: 480px;
  margin-top: 16px;
}

.account-hint {
  margin-left: 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.account-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.account-tag {
  margin-left: 8px;
}
</style>
