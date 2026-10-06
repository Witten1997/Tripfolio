<script setup lang="ts">
import { Download, FileSpreadsheet, Upload } from '@lucide/vue'
import { ElAlert, ElButton, ElPagination, ElTable, ElTableColumn } from 'element-plus'
import { computed, onBeforeUnmount, ref, shallowRef } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'

import ResponsiveEditorShell from '@/desktop/components/ResponsiveEditorShell.vue'
import { saveLedgerTemplate } from '@/platform/ledgerTemplate'
import { ApiError } from '@/shared/api/auth'
import {
  commitLedgerImport,
  downloadLedgerTemplate,
  prepareLedgerImport,
  type PreparedLedgerImport,
} from '@/shared/api/ledgerImport'
import { actionError, type WriteResult } from '@/shared/api/writes'
import { randomId } from '@/shared/randomId'
import { splitModeLabels, type SplitMode } from '@/shared/travel/ledgerDraft'
import { useTripContext } from '@/shared/travel/tripContext'

const emit = defineEmits<{ saved: [result: WriteResult, count: number] }>()
const context = useTripContext()
const visible = ref(false)
const downloading = ref(false)
const previewing = ref(false)
const saving = ref(false)
const uncertain = ref(false)
const needsPreview = ref(false)
const failure = ref('')
function allowNavigation() {
  if (!saving.value && !uncertain.value) return true
  failure.value = saving.value
    ? '账单正在导入，请等待结果后再离开。'
    : '暂未确认导入结果，请原样「重试确认」后再离开。'
  return false
}
onBeforeRouteLeave(allowNavigation)
onBeforeRouteUpdate((to, from) =>
  to.params.tripId === from.params.tripId ? true : allowNavigation(),
)
const initialFile = shallowRef<File | null>(null)
const prepared = shallowRef<PreparedLedgerImport | null>(null)
const candidate = shallowRef<PreparedLedgerImport | null>(null)
type Pending = {
  readonly prepared: PreparedLedgerImport
  readonly operationId: string
  readonly count: number
}
const pending = shallowRef<Pending | null>(null)
const file = computed(() => candidate.value?.file ?? prepared.value?.file ?? initialFile.value)
const preview = computed(() => candidate.value?.preview ?? prepared.value?.preview ?? null)
const picker = ref<HTMLInputElement>()
const page = ref(1)
const errorPage = ref(1)
const rows = computed(() => preview.value?.rows.slice((page.value - 1) * 25, page.value * 25) ?? [])
const errors = computed(
  () => preview.value?.errors.slice((errorPage.value - 1) * 20, errorPage.value * 20) ?? [],
)
const locked = computed(() => saving.value || previewing.value || uncertain.value)
const canCommit = computed(
  () =>
    !saving.value &&
    !previewing.value &&
    (uncertain.value
      ? !!pending.value
      : !!prepared.value?.preview.count &&
        !prepared.value.preview.errors.length &&
        !needsPreview.value &&
        !candidate.value),
)
const errorRows = computed(() => new Set(preview.value?.errors.map((error) => error.row)))
const modeLabel = (value: string) => splitModeLabels[value as SplitMode] ?? value
let generation = 0
let controller: AbortController | null = null
let alive = true
let originalPage = 1
let originalErrorPage = 1

function cancelReading() {
  generation++
  controller?.abort()
  controller = null
  previewing.value = false
}
function open() {
  if (!alive || saving.value || uncertain.value || visible.value) return
  generation++
  visible.value = true
}
function dismissCandidate() {
  candidate.value = null
  page.value = originalPage
  errorPage.value = originalErrorPage
}
function close(done?: () => void) {
  if (saving.value || uncertain.value) return
  cancelReading()
  if (candidate.value) dismissCandidate()
  visible.value = false
  done?.()
}
onBeforeUnmount(() => {
  alive = false
  cancelReading()
})

async function download() {
  if (downloading.value || locked.value) return
  downloading.value = true
  const turn = generation
  failure.value = ''
  try {
    const blob = await downloadLedgerTemplate(context.tripId)
    if (turn === generation && visible.value && alive) await saveLedgerTemplate(blob)
  } catch (cause) {
    if (turn === generation && alive)
      failure.value = actionError(cause, '模板下载失败，请检查网络后重试')
  } finally {
    downloading.value = false
  }
}
async function choose(event: Event) {
  const input = event.target as HTMLInputElement
  const chosen = input.files?.[0]
  input.value = ''
  if (!chosen || locked.value || !visible.value) return
  if (
    !chosen.name.toLowerCase().endsWith('.xlsx') ||
    chosen.size === 0 ||
    chosen.size > 5 * 1024 * 1024
  ) {
    failure.value = '请选择不超过 5 MB 的 .xlsx 文件；原预览已保留。'
    return
  }
  if (!prepared.value) initialFile.value = chosen
  await readPreview(chosen)
}
async function readPreview(chosen: File) {
  if (locked.value || !visible.value) return
  if (candidate.value) dismissCandidate()
  cancelReading()
  const turn = generation
  controller = new AbortController()
  const signal = controller.signal
  previewing.value = true
  failure.value = ''
  try {
    const next = await prepareLedgerImport(context.tripId, chosen, signal)
    if (turn !== generation || !visible.value || !alive) return
    if (prepared.value) {
      originalPage = page.value
      originalErrorPage = errorPage.value
      candidate.value = next
    } else {
      prepared.value = next
      initialFile.value = null
      needsPreview.value = false
      pending.value = null
    }
    page.value = errorPage.value = 1
  } catch (cause) {
    if (turn === generation && alive)
      failure.value = actionError(
        cause,
        cause instanceof Error ? cause.message : '预览失败，请保留文件重试',
      )
  } finally {
    if (turn === generation) previewing.value = false
  }
}
async function refreshPreview() {
  const chosen = prepared.value?.file ?? initialFile.value
  if (chosen) await readPreview(chosen)
}
function adoptCandidate() {
  if (!candidate.value || locked.value) return
  prepared.value = candidate.value
  candidate.value = null
  pending.value = null
  initialFile.value = null
  needsPreview.value = false
  failure.value = ''
  page.value = errorPage.value = 1
}
function keepOriginal() {
  if (locked.value) return
  dismissCandidate()
}
async function commit() {
  if (!canCommit.value || !visible.value) return
  const request =
    pending.value ??
    (prepared.value
      ? Object.freeze({
          prepared: prepared.value,
          operationId: randomId(),
          count: prepared.value.preview.count,
        })
      : null)
  if (!request) return
  pending.value = request
  saving.value = true
  failure.value = ''
  let result: WriteResult
  try {
    const { tripId, file: originalFile, preview: originalPreview, baseline } = request.prepared
    result = await commitLedgerImport(
      tripId,
      originalFile,
      originalPreview.digest,
      request.operationId,
      baseline,
    )
  } catch (cause) {
    const status = cause instanceof ApiError ? cause.problem?.status : undefined
    const unknown =
      uncertain.value ||
      !(cause instanceof ApiError) ||
      !status ||
      status >= 500 ||
      status === 408 ||
      status === 429
    uncertain.value = unknown
    if (unknown) {
      failure.value = '暂未确认导入结果。请原样「重试确认」，确认前不能更换文件、重新预览或关闭。'
    } else {
      failure.value = actionError(cause, '导入失败，请重试')
      if (
        cause instanceof ApiError &&
        [
          'COLLECTION_CONFLICT',
          'COLLECTION_BASE_REQUIRED',
          'IMPORT_PREVIEW_CHANGED',
          'IMPORT_PREVIEW_REQUIRED',
          'VALIDATION_FAILED',
          'CURRENCY_MISMATCH',
        ].includes(cause.code ?? '')
      )
        needsPreview.value = true
    }
    return
  } finally {
    saving.value = false
  }
  // 收据已确认后再通知父页；父页刷新失败不改变本批写入结果。
  cancelReading()
  uncertain.value = false
  needsPreview.value = false
  pending.value = null
  prepared.value = candidate.value = null
  initialFile.value = null
  visible.value = false
  if (alive) emit('saved', result, request.count)
}
defineExpose({ open })
</script>

<template>
  <ResponsiveEditorShell
    v-model="visible"
    title="导入账单"
    desktop-width="min(1040px, calc(100vw - 32px))"
    :before-close="close"
    :close-on-press-escape="!saving && !uncertain"
  >
    <div class="ledger-import">
      <p class="import-intro">下载当前行程的模板，填写支出账单，上传后核对分摊结果再确认。</p>
      <div class="import-actions tf-actions">
        <ElButton :loading="downloading" :disabled="locked" @click="download">
          <Download :size="16" aria-hidden="true" />下载模板
        </ElButton>
        <ElButton type="primary" :loading="previewing" :disabled="locked" @click="picker?.click()">
          <Upload :size="16" aria-hidden="true" />{{ file ? '重新选择文件' : '选择 Excel 文件' }}
        </ElButton>
        <input
          ref="picker"
          class="import-picker"
          type="file"
          accept=".xlsx"
          aria-label="选择账单 Excel 文件"
          :disabled="locked"
          @change="choose"
        />
      </div>
      <p class="import-hint">
        仅支持 .xlsx，最大 5 MB、1000 笔。按比例使用成员管理中的比例；有错误时整批不导入。
      </p>
      <p v-if="file" class="import-file">
        <FileSpreadsheet :size="18" aria-hidden="true" />{{ file.name }}
      </p>
      <ElAlert
        v-if="failure"
        type="error"
        :closable="false"
        :title="failure"
        show-icon
        role="alert"
      />
      <ElButton
        v-if="file && !uncertain && !candidate"
        :disabled="saving || previewing"
        @click="refreshPreview"
        >重新预览</ElButton
      >
      <p v-if="previewing" role="status">正在读取新的导入预览；原文件、预览及提交信息已保留。</p>
      <p v-if="needsPreview && !candidate">
        原预览已保留。请重新预览，核对分摊结果后采用，再确认导入。
      </p>
      <section v-if="candidate" class="import-candidate" aria-label="待核对的新预览">
        <ElAlert
          type="warning"
          :closable="false"
          title="以下是新的预览，尚未替换原批次；请核对金额、成员分摊及提示。"
        />
        <p>
          原文件：{{ prepared?.file.name }} · {{ prepared?.preview.count }} 笔 ·
          {{ prepared?.preview.currency_code }} {{ prepared?.preview.total_amount }}
        </p>
        <div class="tf-actions">
          <ElButton :disabled="locked" @click="keepOriginal">保留原预览</ElButton>
          <ElButton type="primary" :disabled="locked" @click="adoptCandidate">采用新预览</ElButton>
        </div>
      </section>
      <template v-if="preview">
        <div class="import-summary" aria-live="polite">
          <span
            >共 <strong>{{ preview.count }}</strong> 笔</span
          >
          <span v-if="preview.errors.length"
            >有效 <strong>{{ preview.valid_count }}</strong> 笔</span
          >
          <span
            >{{ preview.errors.length ? '有效金额' : '合计' }}
            <strong>{{ preview.currency_code }} {{ preview.total_amount }}</strong></span
          >
        </div>
        <ElAlert
          v-if="preview.errors.length"
          type="error"
          :closable="false"
          title="请在 Excel 中修正以下错误后重新上传，本批次尚未导入。"
          show-icon
        />
        <div v-if="preview.errors.length" class="import-errors" role="region" aria-label="导入错误">
          <p v-for="(error, index) in errors" :key="`${error.row}-${error.column}-${index}`">
            第 {{ error.row }} 行 · {{ error.column }}：{{ error.message }}
          </p>
          <ElPagination
            v-if="preview.errors.length > 20"
            v-model:current-page="errorPage"
            :total="preview.errors.length"
            :page-size="20"
            layout="prev, pager, next"
            small
          />
        </div>
        <details v-if="preview.warnings.length" class="import-warnings" open>
          <summary>请核对 {{ preview.warnings.length }} 条提示</summary>
          <ul>
            <li v-for="warning in preview.warnings" :key="warning">{{ warning }}</li>
          </ul>
        </details>
        <ElTable
          :data="rows"
          :row-class-name="({ row }) => (errorRows.has(row.row) ? 'import-row-error' : '')"
          border
          max-height="360"
          aria-label="账单导入预览"
        >
          <ElTableColumn prop="row" label="行号" width="64" fixed />
          <ElTableColumn prop="amount" label="金额" min-width="110" />
          <ElTableColumn prop="category" label="分类" min-width="100" />
          <ElTableColumn label="分摊模式" min-width="100"
            ><template #default="{ row }">{{ modeLabel(row.split_mode) }}</template></ElTableColumn
          >
          <ElTableColumn prop="payer" label="付款人" min-width="100" />
          <ElTableColumn label="分摊结果" min-width="210">
            <template #default="{ row }"
              ><div v-for="share in row.splits" :key="share.member">
                {{ share.member }}：{{ share.amount }}
              </div></template
            >
          </ElTableColumn>
          <ElTableColumn prop="occurred_on" label="实际日期" width="120" />
          <ElTableColumn prop="notes" label="备注" min-width="160" show-overflow-tooltip />
        </ElTable>
        <p class="import-scroll-hint">左右滑动表格可查看付款人、分摊结果和备注。</p>
        <ElPagination
          v-if="preview.count > 25"
          v-model:current-page="page"
          :total="preview.count"
          :page-size="25"
          layout="total, prev, pager, next"
          :pager-count="5"
          small
        />
      </template>
    </div>
    <template #footer>
      <ElButton :disabled="saving || uncertain" @click="close()">关闭</ElButton>
      <ElButton type="primary" :disabled="!canCommit" :loading="saving" @click="commit">
        {{ uncertain ? '重试确认' : `确认导入${preview?.count ? ` ${preview.count} 笔` : ''}` }}
      </ElButton>
    </template>
  </ResponsiveEditorShell>
</template>

<style scoped>
.ledger-import {
  display: grid;
  gap: 14px;
}
.import-intro,
.import-hint,
.import-file {
  margin: 0;
}
.import-intro {
  color: var(--tf-text-2);
  line-height: 1.6;
}
.import-hint {
  color: var(--tf-text-3);
  font-size: 13px;
  line-height: 1.6;
}
.import-actions,
.import-file,
.import-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}
.import-actions :deep(.el-button) {
  margin-left: 0;
}
.import-actions svg {
  margin-right: 6px;
}
.import-file {
  color: var(--tf-text-2);
  overflow-wrap: anywhere;
}
.import-picker {
  display: none;
}
.import-scroll-hint {
  display: none;
  margin: 0;
  color: var(--tf-text-3);
  font-size: 12px;
}
@media (max-width: 767px) {
  .import-scroll-hint {
    display: block;
  }
}
.import-summary {
  gap: 12px 28px;
  padding: 12px 0;
  border-block: 1px solid var(--tf-line-soft);
}
.import-summary strong {
  color: var(--tf-text-1);
  font-variant-numeric: tabular-nums;
}
.import-errors {
  padding: 0 12px;
  border-left: 3px solid var(--tf-danger);
  font-size: 13px;
}
.import-errors p {
  margin: 0 0 8px;
}
.import-warnings {
  padding: 12px;
  border: 1px solid var(--tf-line-soft);
  border-radius: 8px;
  font-size: 13px;
}
.import-warnings summary {
  cursor: pointer;
  color: var(--tf-text-1);
}
.import-warnings ul {
  max-height: 140px;
  overflow: auto;
  margin-bottom: 0;
  padding-left: 20px;
  line-height: 1.7;
}
:deep(.import-row-error) {
  --el-table-tr-bg-color: var(--tf-danger-soft);
}
</style>
