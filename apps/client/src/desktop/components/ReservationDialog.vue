<script setup lang="ts">
import { ElDatePicker, ElFormItem, ElInput, ElOption, ElSelect } from 'element-plus'
import { computed } from 'vue'
import ContentEditor from '@/desktop/components/ContentEditor.vue'
import {
  createReservation,
  getReservation,
  updateReservation,
  type Reservation,
} from '@/shared/api/reservations'
import type { WriteOutcome } from '@/shared/api/writes'
import {
  changedReservation,
  emptyReservationDraft,
  reservationDraftFrom,
  reservationLabels,
  validateReservation,
} from '@/shared/travel/contentDrafts'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItemEditor } from '@/shared/travel/useItemEditor'
const context = useTripContext()
const emit = defineEmits<{ saved: [outcome: WriteOutcome<Reservation>] }>()
const editor = useItemEditor({
  emptyDraft: emptyReservationDraft,
  draftFrom: reservationDraftFrom,
  validate: validateReservation,
  diff: changedReservation,
  get: (id: string) => getReservation(context.tripId, id),
  create: (body, key) => createReservation(context.tripId, body, key),
  update: (id, version, body, key) => updateReservation(context.tripId, id, version, body, key),
})
const { draft, errors, isEditing } = editor
const labels = computed(
  () =>
    ({
      transport: ['承运方', '出发时刻', '到达时刻'],
      lodging: ['酒店', '入住时刻', '退房时刻'],
      attraction: ['预约方', '开始时刻', '结束时刻'],
      other: ['提供方', '开始时刻', '结束时刻'],
    })[draft.kind],
)
defineExpose({ open: (item?: Reservation) => editor.open(item) })
</script>
<template>
  <ContentEditor
    :editor="editor"
    :title="isEditing ? '编辑预订' : '新建预订'"
    @saved="emit('saved', $event)"
  >
    <ElFormItem label="类型" required :error="errors.kind"
      ><ElSelect v-model="draft.kind"
        ><ElOption
          v-for="(label, kind) in reservationLabels"
          :key="kind"
          :label="label"
          :value="kind" /></ElSelect
    ></ElFormItem>
    <ElFormItem label="预订名称" required :error="errors.title"
      ><ElInput v-model="draft.title" maxlength="200"
    /></ElFormItem>
    <div class="reservation-columns">
      <ElFormItem label="预订编号" :error="errors.booking_reference"
        ><ElInput v-model="draft.booking_reference" maxlength="120"
      /></ElFormItem>
      <ElFormItem :label="labels[0]" :error="errors.provider_name"
        ><ElInput v-model="draft.provider_name" maxlength="200"
      /></ElFormItem>
      <ElFormItem :label="labels[1]" :error="errors.start_local"
        ><ElDatePicker
          v-model="draft.start_local"
          type="datetime"
          value-format="YYYY-MM-DDTHH:mm:ss"
          format="YYYY-MM-DD HH:mm:ss"
          clearable
      /></ElFormItem>
      <ElFormItem :label="labels[2]" :error="errors.end_local"
        ><ElDatePicker
          v-model="draft.end_local"
          type="datetime"
          value-format="YYYY-MM-DDTHH:mm:ss"
          format="YYYY-MM-DD HH:mm:ss"
          clearable
      /></ElFormItem>
    </div>
    <p class="reservation-hint">以上时刻按旅行时区 {{ context.trip.value?.timezone }} 记录。</p>
    <template v-if="draft.kind === 'transport'">
      <ElFormItem label="航班 / 车次" :error="errors.transport_number"
        ><ElInput v-model="draft.transport_number" maxlength="80"
      /></ElFormItem>
      <div class="reservation-columns">
        <ElFormItem label="出发地" :error="errors.origin"
          ><ElInput v-model="draft.origin" maxlength="300"
        /></ElFormItem>
        <ElFormItem label="到达地" :error="errors.destination"
          ><ElInput v-model="draft.destination" maxlength="300"
        /></ElFormItem>
      </div>
    </template>
    <ElFormItem label="地址" :error="errors.address"
      ><ElInput v-model="draft.address" maxlength="500"
    /></ElFormItem>
    <div class="reservation-columns">
      <ElFormItem label="联系人" :error="errors.contact_name"
        ><ElInput v-model="draft.contact_name" maxlength="120"
      /></ElFormItem>
      <ElFormItem label="联系电话" :error="errors.contact_phone"
        ><ElInput v-model="draft.contact_phone" maxlength="64"
      /></ElFormItem>
    </div>
    <ElFormItem label="备注" :error="errors.notes"
      ><ElInput v-model="draft.notes" type="textarea" :rows="3" maxlength="10000" show-word-limit
    /></ElFormItem>
  </ContentEditor>
</template>
<style scoped>
.reservation-columns {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 16px;
}
.reservation-columns :deep(.el-date-editor) {
  width: 100%;
}
.reservation-hint {
  color: var(--tf-text-2);
  font-size: 13px;
  margin: 0 0 20px;
}
@media (max-width: 600px) {
  .reservation-columns {
    grid-template-columns: 1fr;
  }
}
</style>
