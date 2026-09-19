<script setup lang="ts">
import { Delete } from '@lucide/vue'

import { MAX_INTEGER_DIGITS } from '@/shared/money'

const props = defineProps<{
  modelValue: string
  minorUnits: number
  disabled: boolean
  saving: boolean
  submitDisabled: boolean
  submitLabel: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; submit: [] }>()

function input(key: string) {
  if (props.disabled) return
  let value = props.modelValue
  if (key === 'delete') value = value.slice(0, -1)
  else if (key === 'clear') value = ''
  else if (key === '.') {
    if (!props.minorUnits || value.includes('.')) return
    value = `${value || '0'}.`
  } else {
    const [integer = '', fraction] = value.split('.')
    if (fraction !== undefined && fraction.length >= props.minorUnits) return
    if (fraction === undefined && integer !== '0' && integer.length >= MAX_INTEGER_DIGITS) return
    value = value === '0' ? key : value + key
  }
  emit('update:modelValue', value)
}
</script>

<template>
  <div class="ledger-keypad" role="group" aria-label="金额数字键盘">
    <button
      v-for="digit in ['1', '2', '3', '4', '5', '6', '7', '8', '9']"
      :key="digit"
      type="button"
      :disabled="disabled"
      :style="{ gridRow: Math.ceil(Number(digit) / 3), gridColumn: ((Number(digit) - 1) % 3) + 1 }"
      @pointerdown.prevent
      @click="input(digit)"
    >
      {{ digit }}
    </button>
    <button
      type="button"
      class="key-delete"
      aria-label="删除末位金额"
      :disabled="disabled"
      @pointerdown.prevent
      @click="input('delete')"
    >
      <Delete aria-hidden="true" />
    </button>
    <button
      type="button"
      class="key-clear"
      :disabled="disabled"
      @pointerdown.prevent
      @click="input('clear')"
    >
      清空
    </button>
    <button
      type="button"
      class="key-zero"
      :disabled="disabled"
      @pointerdown.prevent
      @click="input('0')"
    >
      0
    </button>
    <button
      type="button"
      class="key-decimal"
      aria-label="小数点"
      :disabled="disabled || !minorUnits"
      @pointerdown.prevent
      @click="input('.')"
    >
      .
    </button>
    <button
      type="button"
      class="key-save"
      :disabled="submitDisabled || saving"
      :aria-busy="saving"
      @pointerdown.prevent
      @click="emit('submit')"
    >
      {{ saving ? '保存中' : submitLabel }}
    </button>
  </div>
</template>

<style scoped>
.ledger-keypad {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  grid-template-rows: repeat(4, clamp(44px, 6.5dvh, 58px));
  gap: 6px;
}
.ledger-keypad button {
  display: grid;
  place-items: center;
  min-width: 0;
  padding: 0 4px;
  border: 0;
  border-radius: 12px;
  background: var(--tf-surface-raised);
  color: var(--tf-text-1);
  font: inherit;
  font-size: 25px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  touch-action: manipulation;
  cursor: pointer;
}
.ledger-keypad button:active:not(:disabled) {
  background: var(--tf-accent-soft);
}
.ledger-keypad button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: -3px;
}
.ledger-keypad button:disabled {
  opacity: 0.45;
  cursor: default;
}
.ledger-keypad .key-delete {
  grid-area: 1 / 4 / 2 / 5;
  background: var(--tf-surface-sunken);
}
.key-delete svg {
  width: 24px;
  height: 24px;
}
.ledger-keypad .key-clear {
  grid-area: 2 / 4 / 3 / 5;
  background: var(--tf-surface-sunken);
  font-size: 14px;
}
.key-zero {
  grid-area: 4 / 1 / 5 / 3;
}
.key-decimal {
  grid-area: 4 / 3 / 5 / 4;
}
.ledger-keypad .key-save {
  grid-area: 3 / 4 / 5 / 5;
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  font-size: 16px;
}
</style>
