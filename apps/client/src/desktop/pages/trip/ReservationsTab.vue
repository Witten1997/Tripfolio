<script setup lang="ts">
import { ElAlert, ElButton, ElEmpty, ElOption, ElSelect, ElSkeleton, ElTag } from 'element-plus'
import { computed, onMounted, ref, watch } from 'vue'
import AssetFile from '@/desktop/components/AssetFile.vue'
import DocumentDialog from '@/desktop/components/DocumentDialog.vue'
import IconAction from '@/desktop/components/IconAction.vue'
import ReservationDialog from '@/desktop/components/ReservationDialog.vue'
import { listDocuments, deleteDocument, type Document } from '@/shared/api/documents'
import { listReservations, deleteReservation, type Reservation } from '@/shared/api/reservations'
import { contentFieldLabels, reservationLabels } from '@/shared/travel/contentDrafts'
import { useTripContext } from '@/shared/travel/tripContext'
import { useContentCollection } from '@/shared/travel/useContentCollection'
const context = useTripContext()
const reservationDialog = ref<InstanceType<typeof ReservationDialog>>()
const documentDialog = ref<InstanceType<typeof DocumentDialog>>()
const reservationId = ref('')
const reservations = useContentCollection(
  (cursor) => listReservations(context.tripId, { limit: 50, cursor }),
  (item: Reservation, key) => deleteReservation(context.tripId, item.id, item.version, key),
)
const documents = useContentCollection(
  (cursor) =>
    listDocuments(context.tripId, {
      limit: 24,
      cursor,
      reservation_id: reservationId.value || undefined,
    }),
  (item: Document, key) => deleteDocument(context.tripId, item.id, item.version, key),
)
const { items: bookings, loading, error, notice, cursor, deleting } = reservations
const {
  items: files,
  loading: filesLoading,
  error: filesError,
  notice: filesNotice,
  cursor: filesCursor,
  deleting: fileDeleting,
} = documents
const names = computed(() => new Map(bookings.value.map((v) => [v.id, v.title])))
const fields = [
  'booking_reference',
  'transport_number',
  'provider_name',
  'start_local',
  'end_local',
  'origin',
  'destination',
  'address',
  'contact_name',
  'contact_phone',
  'notes',
] as const
function details(item: Reservation) {
  return fields
    .filter((key) => item[key])
    .map((key) => ({
      key,
      label: contentFieldLabels[key],
      value: key.endsWith('_local') ? item[key]!.replace('T', ' ') : item[key],
    }))
}
async function removeReservation(item: Reservation) {
  if (
    await reservations.destroy(item, '删除预订“' + item.title + '”？关联资料会保留为独立资料。')
  ) {
    if (reservationId.value === item.id) reservationId.value = ''
    else await documents.reload()
  }
}
function reload() {
  void reservations.reload()
  void documents.reload()
}
watch(reservationId, () => void documents.reload())
onMounted(reload)
</script>
<template>
  <section class="reservations-tab">
    <div class="content-toolbar tf-actions">
      <h2>预订与资料</h2>
      <ElButton type="primary" @click="reservationDialog?.open()">新建预订</ElButton>
      <ElButton @click="documentDialog?.open(undefined, reservationId || undefined)"
        >添加资料</ElButton
      >
      <IconAction
        icon="refresh"
        label="刷新预订与资料"
        :loading="loading || filesLoading"
        @click="reload"
      />
    </div>
    <ElAlert v-if="error" :title="error" type="error" :closable="false" />
    <ElAlert v-if="notice" :title="notice" type="success" @close="notice = ''" />
    <ElSkeleton v-if="loading && !bookings.length" :rows="4" animated />
    <ElEmpty v-else-if="!bookings.length && !error" description="暂无预订，资料也可以单独添加。" />
    <article v-for="booking in bookings" :key="booking.id" class="content-card tf-surface">
      <header class="content-heading">
        <div>
          <ElTag size="small">{{ reservationLabels[booking.kind] }}</ElTag>
          <h3>{{ booking.title }}</h3>
        </div>
        <div class="tf-actions">
          <ElButton @click="reservationId = booking.id">查看资料</ElButton>
          <ElButton @click="documentDialog?.open(undefined, booking.id)">添加资料</ElButton>
          <IconAction
            icon="edit"
            :label="'编辑预订' + booking.title"
            @click="reservationDialog?.open(booking)"
          />
          <IconAction
            icon="trash"
            :label="'删除预订' + booking.title"
            :loading="deleting === booking.id"
            @click="removeReservation(booking)"
          />
        </div>
      </header>
      <p class="content-meta">
        {{ booking.start_local?.replace('T', ' ') ?? '未填写开始时刻'
        }}<template v-if="booking.end_local">
          至 {{ booking.end_local.replace('T', ' ') }}</template
        >
      </p>
      <details>
        <summary>查看预订详情</summary>
        <dl>
          <template v-for="field in details(booking)" :key="field.key"
            ><dt>{{ field.label }}</dt>
            <dd>{{ field.value }}</dd></template
          >
        </dl>
      </details>
    </article>
    <ElButton v-if="cursor" :loading="loading" @click="reservations.reload(true)"
      >加载更多预订</ElButton
    >
    <div class="content-toolbar tf-actions">
      <h2>资料</h2>
      <ElSelect
        v-model="reservationId"
        clearable
        aria-label="按预订筛选资料"
        placeholder="全部资料"
        class="document-filter"
      >
        <ElOption label="全部资料" value="" />
        <ElOption
          v-for="booking in bookings"
          :key="booking.id"
          :label="booking.title"
          :value="booking.id"
        />
      </ElSelect>
    </div>
    <ElAlert v-if="filesError" :title="filesError" type="error" :closable="false" />
    <ElAlert v-if="filesNotice" :title="filesNotice" type="success" @close="filesNotice = ''" />
    <ElSkeleton v-if="filesLoading && !files.length" :rows="4" animated />
    <ElEmpty v-else-if="!files.length && !filesError" description="暂无资料" />
    <div class="documents-grid">
      <article v-for="file in files" :key="file.id" class="content-card tf-surface">
        <h3>{{ file.title }}</h3>
        <p class="content-meta">
          {{ file.reservation_id ? (names.get(file.reservation_id) ?? '关联预订') : '独立资料' }}
        </p>
        <AssetFile :asset-id="file.asset_id" :trip-id="context.tripId" allow-pdf />
        <p v-if="file.notes" class="document-notes">{{ file.notes }}</p>
        <div class="tf-actions document-actions">
          <IconAction
            icon="edit"
            :label="'编辑资料' + file.title"
            @click="documentDialog?.open(file)"
          />
          <IconAction
            icon="trash"
            :label="'删除资料' + file.title"
            :loading="fileDeleting === file.id"
            @click="
              documents.destroy(file, '删除资料“' + file.title + '”？其他内容中的文件引用将保留。')
            "
          />
        </div>
      </article>
    </div>
    <ElButton v-if="filesCursor" :loading="filesLoading" @click="documents.reload(true)"
      >加载更多资料</ElButton
    >
    <ReservationDialog ref="reservationDialog" @saved="reservations.saved" />
    <DocumentDialog ref="documentDialog" @saved="documents.saved" />
  </section>
</template>
<style scoped>
.reservations-tab {
  display: grid;
  gap: 16px;
}
.content-toolbar h2 {
  margin: 0 auto 0 0;
  font-size: 20px;
}
.content-card {
  min-width: 0;
  padding: 20px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-card);
  background: var(--tf-surface);
}
.content-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: start;
  gap: 12px;
  justify-content: space-between;
}
.content-card h3 {
  margin: 8px 0;
  overflow-wrap: anywhere;
}
.content-meta {
  color: var(--tf-text-2);
  font-size: 13px;
}
summary {
  cursor: pointer;
  color: var(--tf-accent);
  padding: 8px 0;
}
dl {
  display: grid;
  grid-template-columns: 100px 1fr;
  gap: 12px;
}
dt {
  color: var(--tf-text-2);
}
dd {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.documents-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.document-notes {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.document-actions {
  margin-top: 16px;
}
.document-filter {
  max-width: 280px;
}
@media (max-width: 800px) {
  .documents-grid {
    grid-template-columns: 1fr;
  }
  .content-card {
    padding: 16px;
  }
}
</style>
