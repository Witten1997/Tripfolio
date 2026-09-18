<script setup lang="ts">
import { useId } from 'vue'

export interface SlidingSegmentOption {
  value: string
  label: string
}

const props = withDefaults(
  defineProps<{
    modelValue: string
    options: SlidingSegmentOption[]
    label: string
    disabled?: boolean
  }>(),
  { disabled: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const groupId = useId()

function select(value: string) {
  if (!props.disabled) emit('update:modelValue', value)
}

function optionIndex() {
  const index = props.options.findIndex((option) => option.value === props.modelValue)
  return index < 0 ? 0 : index
}
</script>

<template>
  <div
    class="sliding-segmented"
    :class="[`sliding-segmented--count-${options.length}`, { 'is-disabled': disabled }]"
    role="group"
    :aria-label="label"
  >
    <span
      class="sliding-segmented__thumb"
      aria-hidden="true"
      :style="{ transform: `translateX(${optionIndex() * 100}%)` }"
    />
    <button
      v-for="option in options"
      :key="option.value"
      type="button"
      class="sliding-segmented__option"
      :class="{ 'is-active': modelValue === option.value }"
      :aria-pressed="modelValue === option.value"
      :disabled="disabled"
      @click="select(option.value)"
    >
      {{ option.label }}
    </button>
    <input
      v-for="option in options"
      :key="`input-${option.value}`"
      class="sliding-segmented__input"
      type="radio"
      :name="groupId"
      :value="option.value"
      :checked="modelValue === option.value"
      :aria-label="option.label"
      :disabled="disabled"
      @change="select(option.value)"
    />
  </div>
</template>

<style scoped>
.sliding-segmented {
  position: relative;
  isolation: isolate;
  display: grid;
  grid-template-columns: repeat(var(--sliding-segment-count), minmax(0, 1fr));
  box-sizing: border-box;
  width: min(100%, 216px);
  min-height: var(--tf-control-size);
  padding: 3px;
  border: 1px solid var(--tf-line);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-sunken);
  box-shadow: inset 0 1px 2px color-mix(in srgb, var(--tf-text-1) 4%, transparent);
  --sliding-segment-count: 2;
}
.sliding-segmented--count-3 {
  --sliding-segment-count: 3;
}
.sliding-segmented--count-4 {
  --sliding-segment-count: 4;
}
.sliding-segmented__thumb {
  position: absolute;
  z-index: -1;
  top: 3px;
  bottom: 3px;
  left: 3px;
  width: calc((100% - 6px) / var(--sliding-segment-count));
  border-radius: calc(var(--tf-radius-control) - 3px);
  background: var(--tf-accent);
  box-shadow:
    var(--tf-shadow-1),
    inset 0 1px 0 color-mix(in srgb, var(--tf-accent-contrast) 34%, transparent);
  transition: transform 220ms var(--tf-ease);
}
.sliding-segmented__option {
  position: relative;
  z-index: 1;
  min-width: 0;
  min-height: calc(var(--tf-control-size) - 8px);
  padding: 0 6px;
  border: 0;
  border-radius: calc(var(--tf-radius-control) - 3px);
  background: transparent;
  color: var(--tf-text-2);
  font: inherit;
  font-size: 13px;
  font-weight: 500;
  letter-spacing: 0;
  white-space: nowrap;
  cursor: pointer;
  transition: color 160ms var(--tf-ease);
}
.sliding-segmented__option.is-active {
  color: var(--tf-accent-contrast);
}
.sliding-segmented__option:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 2px;
}
.sliding-segmented__input {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  clip-path: inset(50%);
  white-space: nowrap;
}
.sliding-segmented.is-disabled {
  opacity: 0.6;
}
.sliding-segmented.is-disabled .sliding-segmented__option {
  cursor: not-allowed;
}
:global([data-theme='glass']) .sliding-segmented {
  border-color: color-mix(in srgb, var(--tf-line) 72%, white 28%);
  border-radius: 18px 13px 20px 14px / 14px 19px 13px 18px;
  background-color: color-mix(in srgb, var(--tf-surface-raised) 42%, transparent);
  background-image: linear-gradient(
    135deg,
    color-mix(in srgb, var(--tf-accent-contrast) 14%, transparent),
    transparent 42%,
    color-mix(in srgb, var(--tf-accent-contrast) 4%, transparent)
  );
  -webkit-backdrop-filter: blur(8px) saturate(115%);
  backdrop-filter: blur(8px) saturate(115%);
  box-shadow:
    var(--tf-shadow-1),
    inset 0 1px 0 color-mix(in srgb, var(--tf-accent-contrast) 72%, transparent),
    inset 1px 0 0 color-mix(in srgb, var(--tf-accent-contrast) 38%, transparent),
    inset 0 -1px 0 color-mix(in srgb, var(--tf-text-1) 10%, transparent);
}
:global([data-theme='glass']) .sliding-segmented__thumb {
  border: 1px solid color-mix(in srgb, var(--tf-accent) 76%, white 24%);
  border-radius: 15px 11px 17px 10px / 11px 16px 10px 15px;
  background-color: var(--tf-accent);
  background-image: linear-gradient(
    135deg,
    color-mix(in srgb, var(--tf-accent-contrast) 20%, transparent),
    transparent 52%
  );
  box-shadow:
    0 5px 12px -8px color-mix(in srgb, var(--tf-accent) 76%, transparent),
    inset 0 1px 0 color-mix(in srgb, var(--tf-accent-contrast) 46%, transparent),
    inset 0 -1px 0 color-mix(in srgb, var(--tf-text-1) 12%, transparent);
}
@media (max-width: 600px) {
  .sliding-segmented {
    width: min(216px, 100%);
  }
  .sliding-segmented__option {
    padding-inline: 6px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .sliding-segmented__thumb,
  .sliding-segmented__option {
    transition: none;
  }
}
</style>
