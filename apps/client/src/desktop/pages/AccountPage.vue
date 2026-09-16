<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElPopconfirm,
  ElProgress,
  ElSkeleton,
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
import { authorizeAssetDownload } from '@/shared/api/assets'
import { ApiError } from '@/shared/api/auth'
import { actionError } from '@/shared/api/writes'
import { isValidPassword } from '@/shared/auth/useEmailChallenge'
import { useSessionStore } from '@/shared/stores/session'
import { useAssetUpload } from '@/shared/travel/useAssetUpload'

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

// 头像：上传走资产链路（登记 → 直传 → 确认 → 等 worker 校验），完成后 PATCH 绑定到账号。
const upload = useAssetUpload()
const avatarInputRef = ref<HTMLInputElement>()
const avatarUrl = ref<string | null>(null)
const avatarBinding = ref(false)
const avatarBusy = computed(
  () =>
    avatarBinding.value || ['preparing', 'uploading', 'processing'].includes(upload.state.phase),
)
const avatarInitial = computed(() => (account.value?.nickname ?? '').trim().slice(0, 1) || '·')
const avatarHint = computed(() => {
  if (upload.state.error) return upload.state.error
  switch (upload.state.phase) {
    case 'preparing':
      return '正在准备上传…'
    case 'processing':
      return '正在处理图片…'
    default:
      return avatarBinding.value ? '正在保存…' : ''
  }
})

/** 头像地址是短期签名，进入页面与更换后都要重新签发。 */
async function loadAvatarUrl() {
  const id = account.value?.avatar_asset_id
  if (!id) {
    avatarUrl.value = null
    return
  }
  try {
    const auth = await authorizeAssetDownload(id, 'thumbnail')
    avatarUrl.value = auth.url ?? null
  } catch {
    // 头像还在处理或缩略图未就绪：显示占位，不打扰用户。
    avatarUrl.value = null
  }
}

async function onAvatarPicked(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  // 清空 value，让同一文件可以再次选择触发 change。
  input.value = ''
  if (!file || !account.value) return

  const asset = await upload.start({ file, scope: 'avatar' })
  if (!asset || asset.status !== 'ready') return
  await bindAvatar(asset.id)
}

async function retryAvatar() {
  const asset = await upload.retry()
  if (!asset || asset.status !== 'ready') return
  await bindAvatar(asset.id)
}

async function bindAvatar(assetId: string | null) {
  if (!account.value) return
  avatarBinding.value = true
  try {
    await updateAccount(account.value.version, { avatar_asset_id: assetId })
    await loadAvatarUrl()
    upload.reset()
    ElMessage.success(assetId ? '头像已更新' : '头像已移除')
  } catch (cause) {
    if (cause instanceof ApiError && cause.code === 'VERSION_CONFLICT') {
      // 资料在别处改过：重新拉取后重试一次绑定，避免用户重传文件。
      await fetchAccount()
      try {
        await updateAccount(account.value.version, { avatar_asset_id: assetId })
        await loadAvatarUrl()
        upload.reset()
        ElMessage.success(assetId ? '头像已更新' : '头像已移除')
        return
      } catch (retryCause) {
        upload.state.error = actionError(retryCause, '头像保存失败，请重试')
      }
    } else {
      upload.state.error = actionError(cause, '头像保存失败，请重试')
    }
  } finally {
    avatarBinding.value = false
  }
}

async function removeAvatar() {
  await bindAvatar(null)
}

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
  next: [{ validator: check(isValidPassword, '密码长度须为 8–128 个字符'), trigger: 'blur' }],
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

async function revoke(s: Session) {
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
    await loadAvatarUrl()
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
        <ElForm
          ref="profileRef"
          :model="profile"
          :rules="profileRules"
          label-width="100px"
          class="account-form"
          @submit.prevent="saveProfile"
        >
          <ElFormItem label="邮箱">
            <ElInput :model-value="account.email" readonly />
          </ElFormItem>
          <ElFormItem label="注册时间">
            <ElInput :model-value="formatTime(account.created_at)" readonly />
          </ElFormItem>
          <ElFormItem label="头像">
            <div class="avatar-field">
              <div class="avatar-preview" :class="{ 'avatar-preview--empty': !avatarUrl }">
                <img v-if="avatarUrl" :src="avatarUrl" alt="当前头像" />
                <span v-else>{{ avatarInitial }}</span>
              </div>
              <div class="avatar-actions">
                <input
                  ref="avatarInputRef"
                  class="avatar-input"
                  type="file"
                  accept="image/jpeg,image/png,image/webp"
                  @change="onAvatarPicked"
                />
                <div class="avatar-buttons">
                  <ElButton :loading="avatarBusy" @click="avatarInputRef?.click()">
                    {{ account.avatar_asset_id ? '更换头像' : '上传头像' }}
                  </ElButton>
                  <ElButton
                    v-if="account.avatar_asset_id && !avatarBusy"
                    text
                    @click="removeAvatar"
                  >
                    移除
                  </ElButton>
                  <ElButton v-if="upload.state.phase === 'failed'" text @click="retryAvatar">
                    重试
                  </ElButton>
                </div>
                <ElProgress
                  v-if="upload.state.phase === 'uploading'"
                  :percentage="Math.round(upload.state.progress * 100)"
                  :stroke-width="6"
                />
                <span v-if="avatarHint" class="account-hint avatar-hint">{{ avatarHint }}</span>
              </div>
            </div>
          </ElFormItem>
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
          <ElFormItem label="新密码（8–128 个字符）" prop="next">
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
          </div>
        </template>
        <div :aria-busy="loadingSessions">
          <ElSkeleton v-if="loadingSessions && !sessions.length" :rows="3" animated />
          <ul v-else-if="sessions.length" class="session-list" aria-label="登录设备">
            <li v-for="device in sessions" :key="device.id" class="session-item">
              <span class="session-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                  <template v-if="device.client_kind === 'web'">
                    <rect x="3" y="4" width="18" height="13" rx="2" />
                    <path d="M8 21h8M12 17v4" />
                  </template>
                  <template v-else>
                    <rect x="6" y="2" width="12" height="20" rx="3" />
                    <path d="M10 18h4" />
                  </template>
                </svg>
              </span>
              <div class="session-details">
                <div class="session-heading">
                  <h3>{{ device.device_name || '未命名设备' }}</h3>
                  <ElTag v-if="device.is_current" size="small" type="success">当前设备</ElTag>
                  <span class="session-kind">{{ clientKindLabel[device.client_kind] }}</span>
                </div>
                <dl class="session-times">
                  <div>
                    <dt>最近活动</dt>
                    <dd>
                      <time :datetime="device.last_seen_at">{{
                        formatTime(device.last_seen_at)
                      }}</time>
                    </dd>
                  </div>
                  <div>
                    <dt>登录时间</dt>
                    <dd>
                      <time :datetime="device.created_at">{{ formatTime(device.created_at) }}</time>
                    </dd>
                  </div>
                </dl>
              </div>
              <ElPopconfirm
                v-if="!device.is_current"
                title="撤销该设备的登录？"
                @confirm="revoke(device)"
              >
                <template #reference>
                  <ElButton
                    class="session-revoke"
                    type="danger"
                    plain
                    :disabled="loadingSessions"
                    :aria-label="`撤销 ${device.device_name || '未命名设备'} 的登录`"
                    >撤销登录</ElButton
                  >
                </template>
              </ElPopconfirm>
            </li>
          </ul>
          <p v-else class="session-empty" role="status">暂无登录设备</p>
        </div>
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

.session-times time {
  font-variant-numeric: tabular-nums;
}

.account-hint {
  margin-left: 12px;
  font-size: 12px;
  color: var(--tf-text-3);
}

.avatar-field {
  display: flex;
  align-items: flex-start;
  gap: 16px;
}

.avatar-preview {
  flex: none;
  width: 72px;
  height: 72px;
  overflow: hidden;
  border-radius: 50%;
  background: var(--tf-surface-2);
  border: 1px solid var(--tf-line-soft);
}

.avatar-preview img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.avatar-preview--empty {
  display: grid;
  place-items: center;
  font-size: 28px;
  color: var(--tf-text-3);
}

.avatar-actions {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  flex: 1;
}

.avatar-buttons {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 原生文件输入不可见，由按钮触发；保留在 DOM 中以便键盘与辅助技术访问。 */
.avatar-input {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
  border: 0;
}

.avatar-hint {
  margin-left: 0;
}

.account-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.session-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.session-item {
  display: grid;
  grid-template-columns: 40px minmax(0, 1fr) auto;
  align-items: start;
  gap: 12px 16px;
  padding-block: 18px;
}

.session-item + .session-item {
  border-block-start: 1px solid var(--tf-line-soft);
}

.session-item:first-child {
  padding-block-start: 0;
}

.session-item:last-child {
  padding-block-end: 0;
}

.session-icon {
  display: grid;
  place-items: center;
  inline-size: 40px;
  block-size: 40px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-accent-soft);
  color: var(--tf-accent);
}

.session-icon svg {
  inline-size: 22px;
  block-size: 22px;
}

.session-details {
  min-width: 0;
}

.session-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
}

.session-heading h3 {
  margin: 0;
  min-width: 0;
  color: var(--tf-text-1);
  font-size: 14px;
  font-weight: 600;
  line-height: 1.6;
  overflow-wrap: anywhere;
}

.session-kind,
.session-times dt {
  color: var(--tf-text-3);
  font-size: 12px;
}

.session-times {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 24px;
  margin: 8px 0 0;
  line-height: 1.6;
}

.session-times > div {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 2px 8px;
}

.session-times dd {
  margin: 0;
  color: var(--tf-text-2);
  font-size: 13px;
  overflow-wrap: anywhere;
}

.session-revoke {
  align-self: center;
}

.session-empty {
  margin: 0;
  padding-block: 20px;
  color: var(--tf-text-3);
  font-size: 14px;
  text-align: center;
}

@media (max-width: 600px) {
  .session-item {
    grid-template-columns: 40px minmax(0, 1fr);
    column-gap: 12px;
  }

  .session-revoke {
    grid-column: 2;
    justify-self: start;
  }
}
</style>
