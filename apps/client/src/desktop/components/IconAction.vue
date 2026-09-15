<script setup lang="ts">
import { ElButton, ElTooltip } from 'element-plus'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

import ActionIcon from './ActionIcon.vue'
import type { ActionIconName } from './actionIcons'

defineOptions({ inheritAttrs: false })
withDefaults(
  defineProps<{
    label: string
    icon: ActionIconName
    to?: RouteLocationRaw
    variant?: 'button' | 'navigation'
    loading?: boolean
    disabled?: boolean
    static?: boolean
  }>(),
  { variant: 'button', loading: false, disabled: false, static: false },
)
</script>

<template>
  <ElTooltip
    :content="label"
    :show-after="400"
    :disabled="disabled || loading"
    :trigger="['hover', 'focus']"
    :trigger-keys="[]"
  >
    <RouterLink
      v-if="to"
      v-bind="$attrs"
      :to="to"
      :aria-label="label"
      class="icon-action icon-action--link"
      :class="{ 'is-static': static, 'icon-action--navigation': variant === 'navigation' }"
    >
      <ActionIcon :name="icon" />
    </RouterLink>
    <ElButton
      v-else
      v-bind="$attrs"
      native-type="button"
      :aria-label="label"
      :aria-busy="loading || undefined"
      :disabled="disabled || loading"
      :loading="loading"
      class="icon-action"
      :class="{ 'is-static': static }"
    >
      <template #loading>
        <ActionIcon name="loading" class="icon-action__spinner" />
      </template>
      <ActionIcon v-if="!loading" :name="icon" />
    </ElButton>
  </ElTooltip>
</template>

<style scoped>
.icon-action {
  box-sizing: border-box;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: var(--tf-control-size);
  height: var(--tf-control-size);
  min-height: var(--tf-control-size);
  margin-inline-start: 0;
  padding: 0;
  border-radius: var(--tf-radius-control);
  touch-action: manipulation;
}
.icon-action--link {
  border: 1px solid var(--tf-line);
  background: var(--tf-surface-sunken);
  color: var(--tf-text-2);
  text-decoration: none;
}
.icon-action--navigation {
  border-color: transparent;
  background: transparent;
}
.icon-action--navigation.router-link-active {
  color: var(--tf-accent);
  box-shadow: inset 0 -2px 0 var(--tf-accent);
}
.icon-action.is-loading :deep(.icon-action__spinner + span) {
  display: none;
}
.icon-action:focus-visible {
  outline: revert;
  outline-offset: 2px;
}
@media (hover: hover) {
  .icon-action--link:hover {
    border-color: var(--tf-accent);
    background: var(--tf-accent-soft);
    color: var(--tf-accent);
  }
  .icon-action--navigation:hover {
    border-color: transparent;
  }
}
@media (prefers-reduced-motion: no-preference) {
  .icon-action__spinner {
    animation: icon-action-spin 1s linear infinite;
  }
  .icon-action:not(.is-static) {
    transition:
      scale min(var(--tf-duration-fast), 150ms) var(--tf-ease),
      color min(var(--tf-duration-fast), 150ms) var(--tf-ease),
      background-color min(var(--tf-duration-fast), 150ms) var(--tf-ease),
      border-color min(var(--tf-duration-fast), 150ms) var(--tf-ease);
  }
  .icon-action:not(.is-static):not(:disabled):active {
    scale: 0.96;
  }
}
@media (prefers-reduced-motion: reduce) {
  .icon-action {
    transition: none;
  }
}
@keyframes icon-action-spin {
  to {
    transform: rotate(1turn);
  }
}
</style>
