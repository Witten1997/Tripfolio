<script setup lang="ts">
import { ElAlert, ElButton, ElDialog, ElProgress, ElTag } from 'element-plus'
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import {
  assetFailureMessage,
  assetStatusLabels,
  authorizeAssetDownload,
  getAsset,
  type Asset,
  type DownloadAuthorization,
} from '@/shared/api/assets'
import { actionError } from '@/shared/api/writes'
import { DOCUMENT_ACCEPT, IMAGE_ACCEPT, validateAssetFile } from '@/shared/travel/assetFiles'
import { useTripTransfers } from '@/shared/travel/useTripTransfers'

const props = withDefaults(
  defineProps<{
    assetId: string
    tripId: string
    allowPdf?: boolean
    disabled?: boolean
  }>(),
  { allowPdf: false, disabled: false },
)
const emit = defineEmits<{ loaded: [asset: Asset] }>()
const transfers = useTripTransfers()
const transfer = computed(() => transfers.find(props.assetId))
const stored = shallowRef<Asset | null>(null)
const asset = computed<Asset | null>(() => {
  const active = transfer.value?.upload.state.asset
  if (!active) return stored.value
  if (!stored.value) return active
  return BigInt(stored.value.version) > BigInt(active.version) ? stored.value : active
})
const uploadState = computed(() => transfer.value?.upload.state)
const thumbnail = shallowRef<DownloadAuthorization | null>(null)
const preview = shallowRef<DownloadAuthorization | null>(null)
const opened = ref(false)
const busy = ref(false)
const error = ref('')
const imageFailed = ref(false)
const originalInput = ref<HTMLInputElement>()
let generation = 0
let timer: ReturnType<typeof setTimeout> | undefined
let renewed = false
const uploading = computed(
  () => uploadState.value && ['preparing', 'uploading'].includes(uploadState.value.phase),
)
const issue = computed(
  () =>
    error.value ||
    uploadState.value?.error ||
    (asset.value?.status === 'failed' ? assetFailureMessage(asset.value) : ''),
)
const status = computed(() => {
  if (uploadState.value?.phase === 'preparing') return '准备上传'
  if (uploadState.value?.phase === 'failed') return '上传失败'
  return asset.value ? assetStatusLabels[asset.value.status] : '加载文件'
})
const name = computed(() => asset.value?.original_name ?? transfer.value?.name ?? '文件')
async function load() {
  const run = generation
  clearTimeout(timer)
  try {
    const current = await getAsset(props.assetId)
    if (run !== generation) return
    stored.value = current
    emit('loaded', current)
    error.value = ''
    if (current.status === 'ready' && current.media_type?.startsWith('image/')) {
      if (
        !thumbnail.value?.url ||
        Date.parse(thumbnail.value.expires_at ?? '') < Date.now() + 5000
      ) {
        const variant = current.thumbnail_status === 'ready' ? 'thumbnail' : 'original'
        const auth = await authorizeAssetDownload(props.assetId, variant)
        if (run !== generation) return
        thumbnail.value = auth
      }
    }
    if (
      current.status === 'uploading' ||
      current.status === 'processing' ||
      current.thumbnail_status === 'processing'
    )
      timer = setTimeout(() => void load(), 2000)
  } catch (cause) {
    if (run === generation) error.value = actionError(cause, '无法读取文件，请刷新重试。')
  }
}
async function refresh() {
  thumbnail.value = null
  imageFailed.value = false
  renewed = false
  if (uploadState.value?.phase === 'failed' && asset.value?.status === 'ready')
    await transfer.value?.upload.refresh()
  await load()
}
async function thumbnailError() {
  thumbnail.value = null
  if (!renewed) {
    renewed = true
    await load()
  } else imageFailed.value = true
}
async function view() {
  busy.value = true
  error.value = ''
  try {
    const auth = await authorizeAssetDownload(props.assetId)
    if (!auth.url) {
      await load()
      return
    }
    preview.value = auth
    opened.value = true
  } catch (cause) {
    error.value = actionError(cause, '无法获取文件授权，请重试。')
  } finally {
    busy.value = false
  }
}
async function download() {
  busy.value = true
  error.value = ''
  try {
    const auth = await authorizeAssetDownload(props.assetId, 'original', 'attachment')
    if (!auth.url) {
      await load()
      return
    }
    const link = document.createElement('a')
    link.href = auth.url
    link.download = auth.file_name
    link.rel = 'noreferrer'
    link.click()
  } catch (cause) {
    error.value = actionError(cause, '无法下载文件，请重试。')
  } finally {
    busy.value = false
  }
}
function retry() {
  if (transfer.value) void transfer.value.upload.retry()
  else originalInput.value?.click()
}
function resume(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !asset.value || props.disabled) return
  const validation = validateAssetFile(file, props.allowPdf)
  if (validation) {
    error.value = validation
    return
  }
  if (file.name !== asset.value.original_name) {
    error.value = '请选择同名原文件重试；替换文件请先移除引用，再添加新文件。'
    return
  }
  transfers.add(file, props.tripId, undefined, asset.value)
}
watch(
  () => props.assetId,
  () => {
    generation++
    stored.value = null
    thumbnail.value = null
    preview.value = null
    opened.value = false
    renewed = false
    imageFailed.value = false
    void load()
  },
  { immediate: true },
)
watch(
  () => uploadState.value?.phase,
  (phase) => {
    if (phase === 'ready' || phase === 'processing' || phase === 'failed') void load()
  },
)
onScopeDispose(() => {
  generation++
  clearTimeout(timer)
})
</script>

<template>
  <div class="asset-file" :aria-busy="uploading">
    <button
      class="asset-preview"
      type="button"
      :disabled="asset?.status !== 'ready' || busy"
      :aria-label="'查看' + name"
      @click="view"
    >
      <img
        v-if="thumbnail?.url && !imageFailed"
        :src="thumbnail.url"
        :alt="name"
        loading="lazy"
        @error="thumbnailError"
      />
      <span v-else>{{ asset?.media_type === 'application/pdf' ? 'PDF' : status }}</span>
    </button>
    <div class="asset-detail">
      <span class="asset-name">{{ name }}</span>
      <ElTag
        size="small"
        :type="status.includes('失败') ? 'danger' : asset?.status === 'ready' ? 'success' : 'info'"
        >{{ status }}</ElTag
      >
      <ElProgress v-if="uploading" :percentage="Math.round((uploadState?.progress ?? 0) * 100)" />
      <p v-if="asset?.thumbnail_status === 'failed'" class="asset-hint">
        缩略图生成失败，原图仍可查看。
      </p>
      <p v-if="imageFailed" class="asset-hint">预览加载失败，可刷新授权或查看原文件。</p>
      <ElAlert v-if="issue" :title="issue" type="error" :closable="false" />
      <div class="tf-actions">
        <ElButton v-if="asset?.status === 'ready'" :loading="busy" @click="view">查看</ElButton>
        <ElButton
          v-if="
            !uploading &&
            (uploadState?.phase === 'failed' ||
              asset?.status === 'failed' ||
              asset?.status === 'uploading')
          "
          :disabled="disabled"
          @click="retry"
          >{{ transfer ? '重试上传' : '选择原文件重试' }}</ElButton
        >
        <ElButton v-if="issue || asset?.status === 'processing' || imageFailed" @click="refresh"
          >刷新状态</ElButton
        >
        <slot />
      </div>
    </div>
    <input
      ref="originalInput"
      class="file-input"
      type="file"
      :accept="allowPdf ? DOCUMENT_ACCEPT : IMAGE_ACCEPT"
      aria-label="重新选择原文件"
      @change="resume"
    />
    <ElDialog
      v-model="opened"
      :title="name"
      width="min(1000px, 94vw)"
      append-to-body
      destroy-on-close
    >
      <ElAlert v-if="error" :title="error" type="error" :closable="false" />
      <p v-if="preview?.media_type === 'application/pdf'" class="asset-hint">
        浏览器无法预览 PDF 时，可下载文件查看。
      </p>
      <div class="original-preview">
        <img
          v-if="preview?.media_type?.startsWith('image/') && preview.url"
          :src="preview.url"
          :alt="name"
          @error="error = '文件授权可能已过期，请刷新授权重试。'"
        />
        <iframe
          v-else-if="preview?.url"
          :src="preview.url"
          :title="name"
          referrerpolicy="no-referrer"
        />
      </div>
      <template #footer>
        <ElButton :loading="busy" @click="download">下载文件</ElButton>
        <ElButton :loading="busy" @click="view">刷新授权</ElButton>
        <ElButton @click="opened = false">关闭</ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<style scoped>
.asset-file {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  min-width: 0;
}
.asset-preview {
  display: grid;
  place-items: center;
  flex: 0 0 96px;
  width: 96px;
  height: 96px;
  padding: 0;
  overflow: hidden;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-sunken);
  color: var(--tf-text-2);
  cursor: pointer;
}
.asset-preview img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.asset-detail {
  display: grid;
  flex: 1;
  gap: 8px;
  min-width: 0;
}
.asset-name {
  overflow-wrap: anywhere;
  color: var(--tf-text-1);
}
.asset-hint {
  margin: 0;
  color: var(--tf-text-2);
  font-size: 13px;
}
.file-input {
  display: none;
}
.original-preview img {
  display: block;
  max-width: 100%;
  max-height: 68dvh;
  margin: auto;
  object-fit: contain;
}
.original-preview iframe {
  width: 100%;
  height: 65dvh;
  border: 0;
}
@media (max-width: 480px) {
  .asset-preview {
    flex-basis: 72px;
    width: 72px;
    height: 72px;
  }
}
</style>
