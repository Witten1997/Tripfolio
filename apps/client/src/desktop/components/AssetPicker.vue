<script setup lang="ts">
import { ElAlert, ElButton, ElProgress } from 'element-plus'
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import AssetFile from '@/desktop/components/AssetFile.vue'
import type { Asset } from '@/shared/api/assets'
import { DOCUMENT_ACCEPT, IMAGE_ACCEPT, validateAssetFile } from '@/shared/travel/assetFiles'
import { useTripTransfers, type AssetTransfer } from '@/shared/travel/useTripTransfers'
const props = withDefaults(
  defineProps<{
    modelValue: string[]
    tripId: string
    max?: number
    allowPdf?: boolean
    disabled?: boolean
  }>(),
  { max: 10, allowPdf: false, disabled: false },
)
const emit = defineEmits<{
  'update:modelValue': [ids: string[]]
  busy: [value: boolean]
  loaded: [asset: Asset]
}>()
const transfers = useTripTransfers()
const pending = shallowRef<AssetTransfer[]>([])
const input = ref<HTMLInputElement>()
const error = ref('')
let alive = true
const unregistered = computed(() => pending.value.filter((item) => !item.upload.state.assetId))
const remaining = computed(() => props.max - props.modelValue.length - unregistered.value.length)
watch(
  () => unregistered.value.length > 0,
  (value) => emit('busy', value),
  { flush: 'sync' },
)
function select(event: Event) {
  const element = event.target as HTMLInputElement
  const files = Array.from(element.files ?? [])
  element.value = ''
  if (props.disabled) return
  error.value = ''
  if (files.length > remaining.value) {
    error.value = '最多可添加 ' + props.max + ' 个文件，请减少选择数量。'
    return
  }
  for (const file of files) {
    const validation = validateAssetFile(file, props.allowPdf)
    if (validation) {
      error.value = file.name + '：' + validation
      continue
    }
    const item = transfers.add(file, props.tripId, (id) => {
      if (alive && !props.modelValue.includes(id))
        emit('update:modelValue', [...props.modelValue, id])
    })
    pending.value = [...pending.value, item]
  }
}
function remove(id: string) {
  emit(
    'update:modelValue',
    props.modelValue.filter((value) => value !== id),
  )
}
function discard(item: AssetTransfer) {
  transfers.remove(item)
  pending.value = pending.value.filter((value) => value !== item)
}
onScopeDispose(() => {
  alive = false
  emit('busy', false)
})
</script>
<template>
  <div class="asset-picker">
    <p class="asset-picker-hint">
      {{
        allowPdf
          ? 'JPEG / PNG / WebP 每张不超过 20 MiB，PDF 不超过 50 MiB。'
          : 'JPEG / PNG / WebP，每张不超过 20 MiB。'
      }}
      {{ modelValue.length }} / {{ max }}
    </p>
    <AssetFile
      v-for="id in modelValue"
      :key="id"
      :asset-id="id"
      :trip-id="tripId"
      :allow-pdf="allowPdf"
      :disabled="disabled"
      @loaded="emit('loaded', $event)"
    >
      <ElButton :disabled="disabled" @click="remove(id)">移除引用</ElButton>
    </AssetFile>
    <div v-for="item in unregistered" :key="item.key" class="pending-file">
      <span>{{ item.name }}</span>
      <ElProgress v-if="item.upload.state.phase === 'preparing'" :percentage="0" />
      <ElAlert
        v-if="item.upload.state.error"
        :title="item.upload.state.error"
        type="error"
        :closable="false"
      />
      <div class="tf-actions">
        <ElButton
          v-if="item.upload.state.phase === 'failed'"
          :disabled="disabled"
          @click="item.upload.retry()"
          >重试上传</ElButton
        >
        <ElButton :disabled="disabled" @click="discard(item)">移除</ElButton>
      </div>
    </div>
    <ElAlert v-if="error" :title="error" type="error" :closable="false" />
    <div class="tf-actions">
      <ElButton :disabled="disabled || remaining <= 0" @click="input?.click()"
        >添加{{ allowPdf ? '文件' : '图片' }}</ElButton
      >
    </div>
    <input
      ref="input"
      class="file-input"
      type="file"
      :multiple="max > 1"
      :accept="allowPdf ? DOCUMENT_ACCEPT : IMAGE_ACCEPT"
      aria-label="选择上传文件"
      @change="select"
    />
  </div>
</template>
<style scoped>
.asset-picker {
  display: grid;
  gap: 16px;
  width: 100%;
}
.asset-picker-hint {
  margin: 0;
  color: var(--tf-text-2);
  font-size: 13px;
  line-height: 1.6;
}
.pending-file {
  display: grid;
  gap: 8px;
  overflow-wrap: anywhere;
}
.file-input {
  display: none;
}
</style>
