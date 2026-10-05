<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElDatePicker,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessageBox,
  ElRadioButton,
  ElRadioGroup,
  ElSkeleton,
} from 'element-plus'
import { computed, ref, shallowRef, watch } from 'vue'
import AssetPicker from '@/desktop/components/AssetPicker.vue'
import ResponsiveEditorShell from '@/desktop/components/ResponsiveEditorShell.vue'
import PlacePicker from '@/desktop/components/PlacePicker.vue'
import type { Asset } from '@/shared/api/assets'
import { reverseGeocode, type GeoPlace } from '@/shared/api/geo'
import type { Photo } from '@/shared/api/photos'
import type { WriteOutcome } from '@/shared/api/writes'
import { wgs84ToGcj02 } from '@/shared/geo/wgs84'
import { useTripContext } from '@/shared/travel/tripContext'
import { usePhotoEditor } from '@/shared/travel/usePhotoEditor'
const context = useTripContext()
const emit = defineEmits<{ saved: [outcome: WriteOutcome<Photo>] }>()
const editor = usePhotoEditor(context.tripId, () => context.today.value)
const {
  draft,
  errors,
  isEditing,
  opened,
  loading,
  saving,
  error,
  dateError,
  candidate,
  rejected,
  uncertain,
  locked,
  sensitiveReady,
  orderMode,
  checking,
} = editor
const disabled = computed(() => locked.value || rejected.value || checking.value)
const dateBusy = computed(() => editor.preparing.value)
const canSave = computed(
  () =>
    !loading.value &&
    !saving.value &&
    !preparing.value &&
    !locating.value &&
    !adopting.value &&
    !dateBusy.value &&
    !checking.value &&
    (!rejected.value || uncertain.value) &&
    (!isEditing.value || (!!editor.baseline.value && (editor.dirty.value || uncertain.value))),
)
async function save() {
  if (!canSave.value) return
  const outcome = await editor.save()
  if (outcome) emit('saved', outcome)
}
async function close(done?: () => void) {
  if (locked.value || preparing.value || locating.value || adopting.value) return
  if (editor.dirty.value) {
    try {
      await ElMessageBox.confirm('关闭后将放弃尚未保存的输入。', '关闭编辑', {
        confirmButtonText: '放弃输入并关闭',
        cancelButtonText: '继续编辑',
        type: 'warning',
      })
    } catch {
      return
    }
  }
  editor.close()
  done?.()
}
async function adopt() {
  if (!candidate.value || locked.value) return
  try {
    await ElMessageBox.confirm(
      '本次输入（含照片选择、日期和说明）将替换为核对到的最新内容。',
      '采用最新照片',
      {
        confirmButtonText: '放弃输入并采用',
        cancelButtonText: '保留输入',
        type: 'warning',
      },
    )
    editor.adoptCandidate()
  } catch {
    /* 保留原输入及基线 */
  }
}
const preparing = ref(false)
const locating = ref(false)
const adopting = ref(false)
const asset = shallowRef<Asset | null>(null)
const exifNotice = ref('')
const ids = computed({
  get: () => (draft.asset_id ? [draft.asset_id] : []),
  set: (v: string[]) => {
    draft.asset_id = v[0] ?? ''
  },
})
watch(
  () => draft.asset_id,
  () => {
    asset.value = null
    exifNotice.value = ''
  },
)
const hasGps = computed(
  () => asset.value?.exif_latitude != null && asset.value?.exif_longitude != null,
)
function selected(place: GeoPlace) {
  Object.assign(draft, {
    place_name: place.name,
    address: place.address,
    latitude: place.latitude,
    longitude: place.longitude,
  })
}
function clearPlace() {
  Object.assign(draft, { place_name: '', address: '', latitude: null, longitude: null })
}
const setTaken = editor.setTaken
function useExifTime() {
  if (!asset.value?.exif_taken_at_local) return
  setTaken(asset.value.exif_taken_at_local)
}
async function useExifPlace() {
  if (!hasGps.value || !asset.value) return
  const assetId = draft.asset_id
  const original = JSON.stringify([
    draft.latitude,
    draft.longitude,
    draft.address,
    draft.place_name,
  ])
  const point = wgs84ToGcj02({
    latitude: asset.value.exif_latitude!,
    longitude: asset.value.exif_longitude!,
  })
  adopting.value = true
  exifNotice.value = ''
  let place: GeoPlace | null = null
  try {
    place = await reverseGeocode(point)
  } catch {
    exifNotice.value = '已采用照片坐标，地址暂未查到，可手动填写。'
  } finally {
    adopting.value = false
  }
  if (
    !opened.value ||
    disabled.value ||
    assetId !== draft.asset_id ||
    original !== JSON.stringify([draft.latitude, draft.longitude, draft.address, draft.place_name])
  )
    return
  Object.assign(draft, point)
  draft.place_name = place?.name ?? ''
  draft.address = place?.address ?? ''
}
defineExpose({ open: (photo?: Photo) => editor.open(photo) })
</script>
<template>
  <ResponsiveEditorShell
    :model-value="opened"
    :title="isEditing ? '编辑照片' : '添加照片'"
    :before-close="close"
    :close-on-press-escape="!locked"
  >
    <ElSkeleton v-if="loading" :rows="4" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertain ? 'warning' : 'error'"
        :closable="false"
      />
      <ElAlert v-if="dateError" :title="dateError" type="warning" :closable="false" />
      <ElButton v-if="isEditing && !editor.baseline.value" @click="editor.load()"
        >重新加载</ElButton
      >
      <ElForm v-else label-position="top" :disabled="disabled" @submit.prevent="save">
        <ElFormItem label="照片" required :error="errors.asset_id">
          <AssetPicker
            v-model="ids"
            :trip-id="context.tripId"
            :max="1"
            :disabled="disabled"
            @busy="preparing = $event"
            @loaded="asset = $event"
          />
        </ElFormItem>
        <div v-if="asset?.exif_taken_at_local || hasGps" class="exif-suggestions">
          <p>照片自带信息仅在点击后采用，会替换当前对应字段。</p>
          <div class="tf-actions">
            <ElButton
              v-if="asset?.exif_taken_at_local"
              :disabled="disabled || dateBusy || !sensitiveReady"
              @click="useExifTime"
              >使用照片时间 {{ asset.exif_taken_at_local.replace('T', ' ') }}</ElButton
            >
            <ElButton v-if="hasGps" :disabled="disabled" :loading="adopting" @click="useExifPlace"
              >使用照片位置</ElButton
            >
          </div>
          <ElAlert v-if="exifNotice" :title="exifNotice" type="warning" :closable="false" />
        </div>
        <div v-if="isEditing && !sensitiveReady && !rejected" class="tf-actions">
          <ElButton :disabled="disabled" :loading="dateBusy" @click="editor.prepareSensitive()"
            >准备日期与排序编辑</ElButton
          >
          <p>完整读取相关日期后可修改；照片说明和地点可单独保存。</p>
        </div>
        <div class="photo-columns">
          <ElFormItem
            :label="'拍摄时刻（' + (context.trip.value?.timezone ?? '') + '）'"
            :error="errors.taken_at_local"
          >
            <ElDatePicker
              :model-value="draft.taken_at_local"
              :disabled="disabled || dateBusy || !sensitiveReady"
              type="datetime"
              value-format="YYYY-MM-DDTHH:mm:ss"
              format="YYYY-MM-DD HH:mm:ss"
              clearable
              @update:model-value="setTaken"
            />
          </ElFormItem>
          <ElFormItem label="分组日期" required :error="errors.recorded_on">
            <ElDatePicker
              :model-value="draft.recorded_on"
              :disabled="disabled || dateBusy || !sensitiveReady"
              @update:model-value="editor.chooseDay"
              type="date"
              value-format="YYYY-MM-DD"
              :clearable="false"
            />
          </ElFormItem>
        </div>
        <ElFormItem label="照片说明" :error="errors.caption"
          ><ElInput
            v-model="draft.caption"
            type="textarea"
            :rows="3"
            maxlength="2000"
            show-word-limit
        /></ElFormItem>
        <PlacePicker
          :value="draft"
          :city="context.trip.value?.destination"
          :disabled="disabled || adopting"
          :validation-error="errors.latitude || errors.longitude"
          @select="selected"
          @clear="clearPlace"
          @locating="locating = $event"
        />
        <ElFormItem label="地点名称"
          ><ElInput v-model="draft.place_name" maxlength="200"
        /></ElFormItem>
        <ElFormItem label="地址"><ElInput v-model="draft.address" maxlength="500" /></ElFormItem>
        <p v-if="dateBusy" role="status">正在完整读取日期资料，原输入仍保留。</p>
        <ElFormItem v-if="!isEditing" label="照片顺序">
          <ElRadioGroup v-model="orderMode" :disabled="disabled">
            <ElRadioButton value="append">追加到末尾</ElRadioButton>
            <ElRadioButton value="explicit">指定顺序</ElRadioButton>
          </ElRadioGroup>
        </ElFormItem>
        <ElFormItem v-if="isEditing || orderMode === 'explicit'" label="同一拍摄时刻下的排序"
          ><ElInputNumber
            :disabled="disabled || dateBusy || !sensitiveReady"
            v-model="draft.sort_order"
            :min="0"
            :max="2147483647"
            :precision="0"
        /></ElFormItem>
      </ElForm>
      <section v-if="isEditing && editor.baseline.value" class="photo-reconcile" aria-live="polite">
        <p v-if="rejected">输入及原日期资料已保留。核对最新内容后，可选择放弃本次输入并采用。</p>
        <ElButton :disabled="locked || dateBusy" :loading="checking" @click="editor.checkLatest()"
          >核对最新照片与日期</ElButton
        >
        <div v-if="candidate">
          <p>
            最新分组日期：{{ candidate.photo.recorded_on }}；最新排序：{{
              candidate.photo.sort_order
            }}
          </p>
          <p>最新拍摄时刻：{{ candidate.photo.taken_at_local ?? '未填写' }}</p>
          <p>最新照片说明：{{ candidate.photo.caption || '（空）' }}</p>
          <p>
            最新地点：{{ candidate.photo.place_name || '（空）' }} {{ candidate.photo.address }}
          </p>
          <p v-if="candidate.photo.asset_id !== draft.asset_id">最新保存的照片与当前选择不同。</p>
          <p v-for="day in candidate.days" :key="day.scope_id">
            {{ day.scope_id.split('/')[1] }}：已完整核对 {{ day.items.length }} 张照片
          </p>
          <div class="tf-actions">
            <ElButton :disabled="locked" @click="editor.cancelCandidate()">保留当前输入</ElButton>
            <ElButton :disabled="locked || checking" @click="adopt">放弃输入并采用最新</ElButton>
          </div>
        </div>
      </section>
    </template>
    <template #footer>
      <ElButton :disabled="locked || preparing || locating || adopting" @click="close()"
        >取消</ElButton
      >
      <ElButton type="primary" :loading="saving" :disabled="!canSave" @click="save">
        {{ uncertain ? '原样重试' : '保存' }}
      </ElButton>
    </template>
  </ResponsiveEditorShell>
</template>
<style scoped>
.photo-columns {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.photo-columns :deep(.el-date-editor) {
  width: 100%;
}
.exif-suggestions {
  padding: 12px;
  margin-bottom: 16px;
  background: var(--tf-accent-soft);
  border-radius: var(--tf-radius-control);
}
.exif-suggestions p {
  margin: 0 0 8px;
  color: var(--tf-text-2);
}
@media (max-width: 600px) {
  .photo-columns {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
