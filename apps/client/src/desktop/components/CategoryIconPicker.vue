<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { categoryIconComponent, categoryIconLabel } from '@/shared/travel/categoryIconVisuals'

const props = withDefaults(
  defineProps<{
    modelValue: string | null
    icons: string[]
    disabled?: boolean
  }>(),
  { disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const grid = ref<HTMLElement>()

/** 打开编辑时把当前图标滚到可视区中间，只滚动选择器自身，不带动外层表单。 */
onMounted(() => {
  const container = grid.value
  const selected = container?.querySelector<HTMLElement>('[aria-pressed="true"]')
  if (!container || !selected) return
  container.scrollTop = Math.max(
    0,
    selected.offsetTop - container.clientHeight / 2 + selected.offsetHeight / 2,
  )
})
</script>

<template>
  <div ref="grid" class="category-icon-picker" role="group" aria-label="分类图标">
    <button
      v-for="icon in ['', ...icons]"
      :key="icon"
      type="button"
      :aria-pressed="(props.modelValue || '') === icon"
      :disabled="disabled"
      @click="emit('update:modelValue', icon)"
    >
      <span class="category-icon-picker__symbol">
        <component :is="categoryIconComponent(icon || null)" aria-hidden="true" />
      </span>
      <span>{{ categoryIconLabel(icon || null) }}</span>
    </button>
  </div>
</template>

<style scoped>
.category-icon-picker {
  position: relative;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(60px, 1fr));
  gap: 6px;
  width: 100%;
  max-height: 272px;
  padding: 4px;
  overflow-y: auto;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-sunken);
  overscroll-behavior: contain;
}
.category-icon-picker button {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 5px;
  min-width: 0;
  padding: 7px 2px 6px;
  border: 1px solid transparent;
  border-radius: var(--tf-radius-control);
  background: transparent;
  color: var(--tf-text-2);
  font: inherit;
  font-size: 11px;
  line-height: 1.3;
  cursor: pointer;
  touch-action: manipulation;
}
.category-icon-picker__symbol {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 13px;
  background: var(--tf-surface-raised);
  color: var(--tf-text-1);
}
.category-icon-picker__symbol svg {
  width: 20px;
  height: 20px;
  stroke-width: 1.6;
}
.category-icon-picker button[aria-pressed='true'] {
  border-color: var(--tf-accent);
  background: var(--tf-accent-soft);
  color: var(--tf-accent);
  font-weight: 600;
}
.category-icon-picker button[aria-pressed='true'] .category-icon-picker__symbol {
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
}
.category-icon-picker button:disabled {
  opacity: 0.5;
  cursor: default;
}
.category-icon-picker button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 1px;
}
@media (hover: hover) {
  .category-icon-picker button:hover:not(:disabled):not([aria-pressed='true']) {
    background: var(--tf-accent-soft);
    color: var(--tf-accent);
  }
}
</style>
