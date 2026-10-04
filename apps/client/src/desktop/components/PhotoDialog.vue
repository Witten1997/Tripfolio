<script setup lang="ts">
import { ElAlert, ElButton, ElDatePicker, ElFormItem, ElInput, ElInputNumber } from 'element-plus'
import { computed, ref, shallowRef, watch } from 'vue'
import AssetPicker from '@/desktop/components/AssetPicker.vue'
import ContentEditor from '@/desktop/components/ContentEditor.vue'
import PlacePicker from '@/desktop/components/PlacePicker.vue'
import type { Asset } from '@/shared/api/assets'
import { reverseGeocode, type GeoPlace } from '@/shared/api/geo'
import { createPhoto, getPhoto, updatePhoto, type Photo } from '@/shared/api/photos'
import type { WriteOutcome } from '@/shared/api/writes'
import { wgs84ToGcj02 } from '@/shared/geo/wgs84'
import {
  changedContent,
  emptyPhotoDraft,
  photoDraftFrom,
  validatePhoto,
} from '@/shared/travel/contentDrafts'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItemEditor } from '@/shared/travel/useItemEditor'
const context = useTripContext()
const emit = defineEmits<{ saved: [outcome: WriteOutcome<Photo>] }>()
const editor = useItemEditor({
  emptyDraft: () => emptyPhotoDraft(context.today.value),
  draftFrom: photoDraftFrom,
  validate: validatePhoto,
  diff: changedContent,
  get: (id: string) => getPhoto(context.tripId, id),
  create: (body, key) => createPhoto(context.tripId, body, key),
  update: (id, version, body, key) => updatePhoto(context.tripId, id, version, body, key),
})
const { draft, errors, isEditing, opened } = editor
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
function setTaken(value: unknown) {
  draft.taken_at_local = typeof value === 'string' && value ? value : null
  if (draft.taken_at_local) draft.recorded_on = draft.taken_at_local.slice(0, 10)
}
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
  <ContentEditor
    :editor="editor"
    :title="isEditing ? '编辑照片' : '添加照片'"
    :busy="preparing || locating || adopting"
    @saved="emit('saved', $event)"
  >
    <template #default="{ disabled }">
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
          <ElButton v-if="asset?.exif_taken_at_local" :disabled="disabled" @click="useExifTime"
            >使用照片时间 {{ asset.exif_taken_at_local.replace('T', ' ') }}</ElButton
          >
          <ElButton v-if="hasGps" :disabled="disabled" :loading="adopting" @click="useExifPlace"
            >使用照片位置</ElButton
          >
        </div>
        <ElAlert v-if="exifNotice" :title="exifNotice" type="warning" :closable="false" />
      </div>
      <div class="photo-columns">
        <ElFormItem
          :label="'拍摄时刻（' + (context.trip.value?.timezone ?? '') + '）'"
          :error="errors.taken_at_local"
        >
          <ElDatePicker
            :model-value="draft.taken_at_local"
            type="datetime"
            value-format="YYYY-MM-DDTHH:mm:ss"
            format="YYYY-MM-DD HH:mm:ss"
            clearable
            @update:model-value="setTaken"
          />
        </ElFormItem>
        <ElFormItem label="分组日期" required :error="errors.recorded_on">
          <ElDatePicker
            v-model="draft.recorded_on"
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
      <ElFormItem label="同一拍摄时刻下的排序"
        ><ElInputNumber v-model="draft.sort_order" :min="0" :max="2147483647" :precision="0"
      /></ElFormItem>
    </template>
  </ContentEditor>
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
