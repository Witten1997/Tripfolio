<script setup lang="ts">
import { actionIcons, type ActionIconName } from './actionIcons'

defineProps<{ name: ActionIconName }>()
</script>

<template>
  <component
    :is="actionIcons[name]"
    :size="20"
    :stroke-width="1.5"
    aria-hidden="true"
    focusable="false"
  />
</template>

<style scoped>
svg {
  display: block;
  flex-shrink: 0;
  pointer-events: none;
}
</style>
