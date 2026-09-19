<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElSkeleton,
} from 'element-plus'

import CategoryIconPicker from '@/desktop/components/CategoryIconPicker.vue'
import ResponsiveEditorShell from '@/desktop/components/ResponsiveEditorShell.vue'
import type { ExpenseCategory } from '@/shared/api/categories'
import { useMetadataStore } from '@/shared/stores/metadata'
import { useCategoryManager } from '@/shared/travel/useCategoryManager'

const emit = defineEmits<{
  saved: [categories: ExpenseCategory[], selectedId: string | undefined]
}>()
const metadata = useMetadataStore()
const manager = useCategoryManager()
const { opened, loading, saving, uncertainCreate, active, draft, dirty, errors, error, loadError } =
  manager

async function open() {
  await manager.open()
  if (opened.value) manager.start()
}

async function requestClose(done?: () => void) {
  if (saving.value) return
  if (dirty.value || uncertainCreate.value) {
    try {
      await ElMessageBox.confirm(
        uncertainCreate.value
          ? '创建结果尚未确认，分类可能已经保存。关闭后请先核对分类列表，避免重复创建。'
          : '尚有未保存的分类输入，是否放弃？',
        '关闭新增分类',
        {
          confirmButtonText: uncertainCreate.value ? '关闭并核对' : '放弃输入',
          cancelButtonText: '继续编辑',
          type: 'warning',
        },
      )
    } catch {
      return
    }
  }
  manager.close(true)
  done?.()
}

function finish() {
  const created = manager.items.value.find((category) => category.name === draft.name.trim())
  emit('saved', manager.items.value, created?.id)
  manager.close()
}

async function save() {
  if (await manager.save()) {
    if (!loadError.value) finish()
  }
}

async function reloadCategories() {
  await manager.load()
  if (!active.value && !loadError.value) finish()
}

defineExpose({ open })
</script>

<template>
  <ResponsiveEditorShell
    :model-value="opened"
    title="新增分类"
    :before-close="requestClose"
    :close-on-press-escape="!saving"
    class="category-create-shell"
  >
    <p class="category-hint">分类在本账号的所有旅行中共用，新分类会排在最后。</p>
    <ElSkeleton v-if="loading && !active" :rows="5" animated />
    <ElAlert
      v-if="error"
      :title="error"
      :type="uncertainCreate ? 'warning' : 'error'"
      :closable="false"
      show-icon
      class="category-alert"
    />
    <ElAlert v-if="loadError" :closable="false" type="warning" class="category-alert">
      <template #title>{{ active ? loadError : '分类已保存，刷新列表后即可使用。' }}</template>
      <ElButton :loading="loading" :disabled="saving || uncertainCreate" @click="reloadCategories">
        刷新分类
      </ElButton>
    </ElAlert>
    <template v-if="active">
      <ElAlert
        v-if="metadata.status !== 'ready' && !uncertainCreate"
        type="warning"
        :closable="false"
        class="category-alert"
      >
        <template #title>分类图标尚未加载</template>
        <ElButton :loading="metadata.status === 'loading'" @click="metadata.load"
          >加载图标</ElButton
        >
      </ElAlert>
      <ElForm label-position="top" :disabled="saving || uncertainCreate" @submit.prevent="save">
        <ElFormItem label="分类名称" required :error="errors.name">
          <ElInput v-model="draft.name" maxlength="40" placeholder="例如：咖啡" />
        </ElFormItem>
        <ElFormItem label="分类图标" :error="errors.icon">
          <CategoryIconPicker
            v-model="draft.icon"
            :icons="metadata.metadata?.expense_category_icons ?? []"
            :disabled="saving || uncertainCreate"
          />
        </ElFormItem>
      </ElForm>
    </template>
    <template #footer>
      <ElButton
        v-if="active"
        type="primary"
        :loading="saving"
        :disabled="loading || (!uncertainCreate && metadata.status !== 'ready')"
        @click="save"
        >{{ uncertainCreate ? '重试创建' : '保存分类' }}</ElButton
      >
    </template>
  </ResponsiveEditorShell>
</template>

<style scoped>
.category-hint {
  margin: 0 0 18px;
  color: var(--tf-text-3);
  font-size: 12px;
  line-height: 1.7;
}
.category-alert {
  margin-bottom: 16px;
}
</style>

<style>
.tf-editor-shell.category-create-shell.el-drawer {
  height: auto !important;
  max-height: min(90dvh, 700px);
}
</style>
