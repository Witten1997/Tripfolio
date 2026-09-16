<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElDatePicker,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
} from 'element-plus'
import { ChevronDown, MapPin } from '@lucide/vue'
import { computed, onMounted, onUnmounted, ref } from 'vue'

import DestinationPickerDialog from '@/desktop/components/DestinationPickerDialog.vue'
import type { Trip } from '@/shared/api/trips'
import type { WriteOutcome } from '@/shared/api/writes'
import { useMetadataStore } from '@/shared/stores/metadata'
import { draftFromTrip, tripFieldLabels, type TripField } from '@/shared/travel/tripDraft'
import { useTripEditor } from '@/shared/travel/useTripEditor'

const emit = defineEmits<{ saved: [outcome: WriteOutcome<Trip>] }>()
const metadata = useMetadataStore()
const editor = useTripEditor()
const destinationPicker = ref<InstanceType<typeof DestinationPickerDialog>>()
const isMobile = ref(false)
let mobileMediaQuery: MediaQueryList | undefined

function syncMobileState() {
  isMobile.value = mobileMediaQuery?.matches ?? false
}
const {
  opened,
  loading,
  saving,
  uncertainCreate,
  loadingLatest,
  baseline,
  latest,
  conflict,
  error,
  latestError,
  errors,
  draft,
  dirty,
  isEditing,
} = editor

const conflictRows = computed(() => {
  if (!baseline.value || !latest.value) return []
  const original = draftFromTrip(baseline.value)
  return (Object.keys(tripFieldLabels) as TripField[])
    .filter((field) => draft[field] !== original[field])
    .map((field) => ({
      field,
      label: tripFieldLabels[field],
      mine: draft[field] || (field === 'budget_amount' ? '未设置' : '（空）'),
      theirs: latest.value?.[field] ?? (field === 'budget_amount' ? '未设置' : '（空）'),
    }))
})

const dateRange = computed<[string, string] | null>({
  get: () =>
    draft.start_date && draft.end_date
      ? ([draft.start_date, draft.end_date] as [string, string])
      : null,
  set: (value: [string, string] | null) => {
    draft.start_date = value?.[0] ?? ''
    draft.end_date = value?.[1] ?? ''
  },
})
const dateError = computed(() => errors.value.start_date || errors.value.end_date)
const budgetError = computed(() => errors.value.budget_amount || errors.value.currency_code)

function setDestination(value: string) {
  draft.destination = value
}

async function requestClose(done?: () => void) {
  if (saving.value) return
  if (dirty.value || uncertainCreate.value) {
    try {
      await ElMessageBox.confirm(
        uncertainCreate.value
          ? '创建结果尚未确认，这趟旅行可能已经保存。关闭后请先刷新旅行列表核对，避免重复创建。'
          : '尚有未保存的输入，关闭后将放弃这些输入。',
        '关闭旅行编辑',
        {
          confirmButtonText: uncertainCreate.value ? '关闭并核对列表' : '放弃输入并关闭',
          cancelButtonText: uncertainCreate.value ? '返回重试' : '继续编辑',
          type: 'warning',
        },
      )
    } catch {
      return
    }
  }
  editor.close(true)
  done?.()
}

async function adoptLatest() {
  try {
    await ElMessageBox.confirm('本次输入将被最新保存的内容替换。', '载入最新版本', {
      confirmButtonText: '放弃输入并载入',
      cancelButtonText: '保留输入',
      type: 'warning',
    })
    editor.adoptLatest()
  } catch {
    /* 用户保留草稿。 */
  }
}

async function save(againstLatest = false) {
  const outcome = await editor.save(againstLatest)
  if (outcome) emit('saved', outcome)
}

onMounted(() => {
  if (typeof window === 'undefined' || !window.matchMedia) return
  mobileMediaQuery = window.matchMedia('(max-width: 767px)')
  syncMobileState()
  mobileMediaQuery.addEventListener('change', syncMobileState)
})

onUnmounted(() => {
  mobileMediaQuery?.removeEventListener('change', syncMobileState)
})

defineExpose({ open: editor.open })
</script>

<template>
  <ElDialog
    :model-value="opened"
    :title="isEditing ? '编辑旅行' : '新建旅行'"
    width="min(680px, calc(100vw - 32px))"
    class="trip-editor"
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :before-close="requestClose"
    align-center
    destroy-on-close
  >
    <ElSkeleton v-if="loading" :rows="7" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertainCreate ? 'warning' : 'error'"
        :closable="false"
        show-icon
        class="editor-alert"
      />
      <ElButton v-if="isEditing && !baseline" @click="editor.load">重新加载旅行</ElButton>
      <template v-else>
        <ElAlert
          v-if="metadata.status !== 'ready' && !uncertainCreate"
          type="warning"
          :closable="false"
          class="editor-alert"
        >
          <template #title>正在等待币种信息，加载完成后才能保存。</template>
          <p v-if="metadata.error">{{ metadata.error }}</p>
          <ElButton :loading="metadata.status === 'loading'" @click="metadata.load"
            >重新加载币种</ElButton
          >
        </ElAlert>
        <ElAlert
          v-if="baseline?.archived_at"
          title="这趟旅行已归档，仍可编辑和修正记录。"
          type="info"
          :closable="false"
          class="editor-alert"
        />
        <ElForm label-position="top" :disabled="saving || uncertainCreate" @submit.prevent="save()">
          <ElFormItem label="旅行名称" required :error="errors.name">
            <ElInput
              v-model="draft.name"
              maxlength="120"
              autofocus
              placeholder="例如：秋日京都之旅"
            />
          </ElFormItem>
          <ElFormItem label="游玩时间" required :error="dateError">
            <ElDatePicker
              v-model="dateRange"
              class="trip-date-range"
              type="daterange"
              value-format="YYYY-MM-DD"
              format="YYYY年M月D日"
              range-separator="-"
              start-placeholder="设置时间范围"
              end-placeholder=""
              :single-panel="isMobile"
              unlink-panels
            />
          </ElFormItem>
          <p v-if="isEditing" class="editor-hint">
            修改日期会保留已有行程和记录，超出新日期的安排将在保存后提示。
          </p>
          <ElFormItem label="目的地" :error="errors.destination">
            <ElInput
              :model-value="draft.destination"
              readonly
              role="button"
              placeholder="搜索并选择一个或多个城市"
              class="destination-input"
              @click="destinationPicker?.open()"
              @keydown.enter.prevent="destinationPicker?.open()"
              @keydown.space.prevent="destinationPicker?.open()"
            >
              <template #prefix><MapPin aria-hidden="true" /></template>
              <template #suffix><ChevronDown aria-hidden="true" /></template>
            </ElInput>
          </ElFormItem>
          <ElFormItem label="总预算" :error="budgetError">
            <ElInput
              v-model="draft.budget_amount"
              inputmode="decimal"
              placeholder="留空表示未设置"
              clearable
            >
              <template #append>
                <ElSelect
                  v-model="draft.currency_code"
                  filterable
                  :disabled="!!baseline?.currency_locked_at"
                  aria-label="总预算币种"
                  class="budget-currency"
                >
                  <ElOption
                    v-for="currency in metadata.metadata?.currencies ?? []"
                    :key="currency.code"
                    :label="currency.code"
                    :value="currency.code"
                  />
                </ElSelect>
              </template>
            </ElInput>
          </ElFormItem>
          <p class="editor-hint">预算留空表示未设置，输入 0 表示零预算。金额不会随币种自动换算。</p>
          <ElAlert
            v-if="baseline?.currency_locked_at"
            title="已有有效账目，币种已锁定；删除全部有效账目后可更改。"
            type="info"
            :closable="false"
            class="editor-alert"
          />
          <ElFormItem label="备注" :error="errors.notes">
            <ElInput
              v-model="draft.notes"
              type="textarea"
              :rows="3"
              maxlength="10000"
              show-word-limit
              placeholder="记录这趟旅行的想法或注意事项"
            />
          </ElFormItem>
          <button type="submit" class="visually-hidden" tabindex="-1" aria-hidden="true">
            保存
          </button>
        </ElForm>
        <section v-if="conflict" class="conflict-panel" aria-live="polite">
          <h3>检查版本冲突</h3>
          <p>你的输入已保留。下方对比仅列出你修改过的字段；重新提交时只保存这些改动。</p>
          <ElSkeleton v-if="loadingLatest" :rows="2" animated />
          <ElAlert v-if="latestError" :title="latestError" type="error" :closable="false" />
          <table v-if="latest" class="conflict-table">
            <thead>
              <tr>
                <th>字段</th>
                <th>我的输入</th>
                <th>最新保存内容</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in conflictRows" :key="row.field">
                <th>{{ row.label }}</th>
                <td>{{ row.mine }}</td>
                <td>{{ row.theirs }}</td>
              </tr>
            </tbody>
          </table>
          <div class="conflict-actions tf-actions">
            <ElButton :disabled="!latest || saving" @click="adoptLatest"
              >放弃输入，载入最新版本</ElButton
            >
          </div>
        </section>
      </template>
    </template>
    <template #footer>
      <ElButton :disabled="saving" @click="requestClose()">取消</ElButton>
      <ElButton
        v-if="conflict"
        type="primary"
        :loading="saving"
        :disabled="!latest || loadingLatest || metadata.status !== 'ready'"
        @click="save(true)"
        >确认用我的改动更新最新版本</ElButton
      >
      <ElButton
        v-else
        type="primary"
        :loading="saving"
        :disabled="
          loading ||
          (!uncertainCreate && metadata.status !== 'ready') ||
          (isEditing && (!baseline || !dirty))
        "
        @click="save()"
        >{{ uncertainCreate ? '重试创建' : isEditing ? '保存修改' : '创建旅行' }}</ElButton
      >
    </template>
  </ElDialog>
  <DestinationPickerDialog
    ref="destinationPicker"
    :model-value="draft.destination"
    @update:model-value="setDestination"
  />
</template>

<style scoped>
.editor-alert {
  margin-bottom: 16px;
}
.trip-date-range {
  width: 100%;
}
.destination-input {
  cursor: pointer;
}
.destination-input :deep(input) {
  cursor: pointer;
}
.destination-input :deep(svg) {
  width: 17px;
  height: 17px;
}
.budget-currency {
  width: 108px;
}
.budget-currency :deep(.el-select__wrapper) {
  min-height: 30px;
  background: transparent;
  box-shadow: none;
}
.editor-hint {
  color: var(--tf-text-3);
  font-size: 12px;
  line-height: 1.6;
  margin: 0 0 14px;
}
.conflict-panel {
  background: var(--tf-warning-soft);
  border: 1px solid var(--tf-warning);
  border-radius: var(--tf-radius-control);
  padding: 16px;
}
.conflict-panel h3 {
  margin: 0 0 8px;
  font-size: 15px;
}
.conflict-panel p {
  line-height: 1.6;
  margin: 0 0 12px;
}
.conflict-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: 12px;
}
.conflict-table th,
.conflict-table td {
  text-align: left;
  vertical-align: top;
  border-bottom: 1px solid var(--tf-line);
  padding: 8px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.conflict-table th:first-child {
  width: 68px;
}
.conflict-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}
.conflict-actions .el-button {
  margin-left: 0;
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}

@media (max-width: 767px) {
  :global(.trip-editor) {
    display: flex;
    max-height: calc(100dvh - 32px);
    flex-direction: column;
  }

  :global(.trip-editor .el-dialog__body) {
    min-height: 0;
    overflow-y: auto;
  }
}
</style>
