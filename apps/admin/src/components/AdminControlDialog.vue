<script setup lang="ts">
import { ref, watch } from 'vue'
import { reauthenticate } from '../api'

const props = defineProps<{
  modelValue: boolean
  title: string
  description: string
  execute: (reason: string) => Promise<void>
}>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; completed: [] }>()
const reason = ref('')
const password = ref('')
const busy = ref(false)
const errorMessage = ref('')
watch(
  () => props.modelValue,
  () => {
    reason.value = ''
    password.value = ''
    errorMessage.value = ''
  },
)
async function submit() {
  if (busy.value) return
  if (!reason.value.trim() || !password.value) {
    errorMessage.value = '请填写操作原因和当前管理员密码。'
    return
  }
  busy.value = true
  errorMessage.value = ''
  try {
    await reauthenticate(password.value)
    password.value = ''
    await props.execute(reason.value.trim())
    emit('update:modelValue', false)
    emit('completed')
  } catch (error) {
    password.value = ''
    errorMessage.value = error instanceof Error ? error.message : '操作失败，请重试。'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    :title="title"
    width="min(480px, 94vw)"
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @update:model-value="!busy && emit('update:modelValue', $event)"
  >
    <el-form label-position="top" @submit.prevent="submit">
      <p class="muted">{{ description }}</p>

      <el-form-item label="操作原因（必填）">
        <el-input
          v-model="reason"
          type="textarea"
          :rows="3"
          :maxlength="500"
          show-word-limit
          :disabled="busy"
          aria-label="操作原因"
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

      <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
      <div class="dialog-actions">
        <el-button :disabled="busy" @click="emit('update:modelValue', false)">取消</el-button>
        <el-button type="primary" native-type="submit" :loading="busy">确认{{ title }}</el-button>
      </div>
    </el-form>
  </el-dialog>
</template>

<style scoped>
.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 24px;
}
</style>
