<script setup lang="ts" generic="T extends { id: string; version: string }, D extends object">
import { ElAlert, ElButton, ElForm, ElMessageBox, ElSkeleton } from 'element-plus'
import { computed, reactive } from 'vue'
import ResponsiveEditorShell from '@/desktop/components/ResponsiveEditorShell.vue'
import type { ItemEditor } from '@/shared/travel/useItemEditor'
import type { WriteOutcome } from '@/shared/api/writes'
import { contentFieldLabels } from '@/shared/travel/contentDrafts'

const props = defineProps<{ editor: ItemEditor<T, D>; title: string; busy?: boolean }>()
const emit = defineEmits<{ saved: [outcome: WriteOutcome<T>] }>()
const state = reactive(props.editor)
const disabled = computed(() => state.saving || state.uncertainCreate || !!props.busy)
const rows = computed(() => {
  if (!state.baseline || !state.latest) return []
  return Object.entries(props.editor.draft)
    .filter(
      ([key, value]) => JSON.stringify(value) !== JSON.stringify(Reflect.get(state.baseline!, key)),
    )
    .map(([key, value]) => ({
      key,
      label: contentFieldLabels[key] ?? key,
      mine: String(value ?? '（空）'),
      theirs: String(Reflect.get(state.latest!, key) ?? '（空）'),
    }))
})
async function close(done?: () => void) {
  if (state.saving || props.busy) return
  if (state.dirty || state.uncertainCreate) {
    try {
      await ElMessageBox.confirm(
        state.uncertainCreate
          ? '创建结果尚未确认，记录可能已保存。关闭后请刷新列表核对。'
          : '尚有未保存的输入，关闭后将放弃这些输入。',
        '关闭编辑',
        {
          confirmButtonText: '放弃输入并关闭',
          cancelButtonText: '继续编辑',
          type: 'warning',
        },
      )
    } catch {
      return
    }
  }
  props.editor.close(true)
  done?.()
}
async function save(againstLatest = false) {
  if (props.busy) return
  const outcome = await props.editor.save(againstLatest)
  if (outcome) emit('saved', outcome)
}
async function adopt() {
  try {
    await ElMessageBox.confirm('本次输入将替换为最新保存的内容。', '载入最新版本', {
      confirmButtonText: '放弃输入并载入',
      cancelButtonText: '保留输入',
      type: 'warning',
    })
    props.editor.adoptLatest()
  } catch {
    /* 保留输入 */
  }
}
</script>
<template>
  <ResponsiveEditorShell
    :model-value="state.opened"
    :title="title"
    :before-close="close"
    :close-on-press-escape="!disabled"
  >
    <ElSkeleton v-if="state.loading" :rows="4" animated />
    <template v-else>
      <ElAlert
        v-if="state.error"
        :title="state.error"
        :type="state.uncertainCreate ? 'warning' : 'error'"
        :closable="false"
        class="content-alert"
      />
      <ElButton v-if="state.isEditing && !state.baseline" @click="editor.load()">重新加载</ElButton>
      <ElForm
        v-else
        label-position="top"
        :disabled="state.saving || state.uncertainCreate"
        @submit.prevent="save()"
      >
        <slot :disabled="state.saving || state.uncertainCreate" />
      </ElForm>
      <section v-if="state.conflict" class="content-conflict" aria-live="polite">
        <h3>检查版本冲突</h3>
        <p>输入已保留，请比较后选择如何保存。</p>
        <ElAlert
          v-if="state.latestError"
          :title="state.latestError"
          type="error"
          :closable="false"
        />
        <div class="conflict-scroll">
          <table v-if="state.latest">
            <thead>
              <tr>
                <th>字段</th>
                <th>我的输入</th>
                <th>最新保存内容</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.key">
                <th>{{ row.label }}</th>
                <td>{{ row.mine }}</td>
                <td>{{ row.theirs }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="tf-actions">
          <ElButton :loading="state.loadingLatest" @click="editor.loadLatest()"
            >刷新最新版本</ElButton
          >
          <ElButton :disabled="!state.latest || state.saving" @click="adopt"
            >放弃输入，载入最新版本</ElButton
          >
        </div>
      </section>
    </template>
    <template #footer>
      <ElButton :disabled="disabled" @click="close()">取消</ElButton>
      <ElButton
        v-if="state.conflict"
        type="primary"
        :loading="state.saving"
        :disabled="!state.latest || state.loadingLatest || busy"
        @click="save(true)"
        >确认用我的改动更新最新版本</ElButton
      >
      <ElButton
        v-else
        type="primary"
        :loading="state.saving"
        :disabled="state.loading || busy || (state.isEditing && (!state.baseline || !state.dirty))"
        @click="save()"
        >{{ state.uncertainCreate ? '原样重试' : '保存' }}</ElButton
      >
    </template>
  </ResponsiveEditorShell>
</template>
<style scoped>
.content-alert {
  margin-bottom: 16px;
}
.content-conflict {
  padding: 16px;
  background: var(--tf-warning-soft);
  border-radius: var(--tf-radius-control);
}
.conflict-scroll {
  overflow-x: auto;
  margin-bottom: 12px;
}
table {
  width: 100%;
  border-collapse: collapse;
}
th,
td {
  padding: 8px;
  text-align: left;
  overflow-wrap: anywhere;
  border-bottom: 1px solid var(--tf-line);
}
</style>
