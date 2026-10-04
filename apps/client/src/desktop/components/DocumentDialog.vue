<script setup lang="ts">
import { ElAlert, ElButton, ElFormItem, ElInput, ElOption, ElSelect } from 'element-plus'
import { computed, ref, shallowRef } from 'vue'
import AssetPicker from '@/desktop/components/AssetPicker.vue'
import ContentEditor from '@/desktop/components/ContentEditor.vue'
import { createDocument, getDocument, updateDocument, type Document } from '@/shared/api/documents'
import { listReservations, type Reservation } from '@/shared/api/reservations'
import { actionError, type WriteOutcome } from '@/shared/api/writes'
import {
  changedContent,
  emptyDocumentDraft,
  documentDraftFrom,
  validateDocument,
} from '@/shared/travel/contentDrafts'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItemEditor } from '@/shared/travel/useItemEditor'
const context = useTripContext()
const emit = defineEmits<{ saved: [outcome: WriteOutcome<Document>] }>()
const editor = useItemEditor({
  emptyDraft: emptyDocumentDraft,
  draftFrom: documentDraftFrom,
  validate: validateDocument,
  diff: changedContent,
  get: (id: string) => getDocument(context.tripId, id),
  create: (body, key) => createDocument(context.tripId, body, key),
  update: (id, version, body, key) => updateDocument(context.tripId, id, version, body, key),
})
const { draft, errors, isEditing } = editor
const preparing = ref(false)
const reservations = shallowRef<Reservation[]>([])
const loading = ref(false)
const error = ref('')
let generation = 0
const ids = computed({
  get: () => (draft.asset_id ? [draft.asset_id] : []),
  set: (v: string[]) => {
    draft.asset_id = v[0] ?? ''
  },
})
async function loadReservations() {
  const run = ++generation
  loading.value = true
  error.value = ''
  try {
    const items: Reservation[] = []
    let cursor: string | undefined
    do {
      const page = await listReservations(context.tripId, { limit: 100, cursor })
      items.push(...page.items)
      cursor = page.next_cursor ?? undefined
    } while (cursor && run === generation)
    if (run === generation) reservations.value = items
  } catch (cause) {
    if (run === generation) error.value = actionError(cause, '无法加载预订，请重试。')
  } finally {
    if (run === generation) loading.value = false
  }
}
defineExpose({
  open: async (item?: Document, reservationId?: string) => {
    void loadReservations()
    await editor.open(item, { reservation_id: reservationId ?? null })
  },
})
</script>
<template>
  <ContentEditor
    :editor="editor"
    :title="isEditing ? '编辑资料' : '添加资料'"
    :busy="preparing"
    @saved="emit('saved', $event)"
  >
    <template #default="{ disabled }">
      <ElFormItem label="资料名称" required :error="errors.title"
        ><ElInput v-model="draft.title" maxlength="200"
      /></ElFormItem>
      <ElFormItem label="图片或 PDF" required :error="errors.asset_id"
        ><AssetPicker
          v-model="ids"
          :trip-id="context.tripId"
          :max="1"
          allow-pdf
          :disabled="disabled"
          @busy="preparing = $event"
      /></ElFormItem>
      <ElFormItem label="关联预订（可留空）" :error="errors.reservation_id">
        <ElSelect
          v-model="draft.reservation_id"
          clearable
          filterable
          :loading="loading"
          placeholder="独立资料"
        >
          <ElOption
            v-for="reservation in reservations"
            :key="reservation.id"
            :label="reservation.title"
            :value="reservation.id"
          />
        </ElSelect>
      </ElFormItem>
      <ElAlert v-if="error" :title="error" type="error" :closable="false" />
      <ElButton v-if="error" @click="loadReservations">重新加载预订</ElButton>
      <ElFormItem label="备注" :error="errors.notes"
        ><ElInput v-model="draft.notes" type="textarea" :rows="3" maxlength="4000" show-word-limit
      /></ElFormItem>
    </template>
  </ContentEditor>
</template>
