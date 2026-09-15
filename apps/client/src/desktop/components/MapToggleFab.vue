<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

import ActionIcon from './ActionIcon.vue'

const props = defineProps<{
  /** 当前是否在地图视图：按钮显示相反方向的动作。 */
  onMap: boolean
  /** 主人侧传页签路由，渲染为真实链接；访客侧不传，渲染为按钮并抛 toggle。 */
  to?: RouteLocationRaw
}>()
const emit = defineEmits<{ toggle: [] }>()

const action = computed(() =>
  props.onMap
    ? { icon: 'list' as const, label: '看行程' }
    : { icon: 'map' as const, label: '看地图' },
)
</script>

<template>
  <div class="map-toggle-root">
    <!-- 固定按钮会盖住最后一屏内容；补一段流内空白，滚到底时仍能读完。 -->
    <span class="map-toggle__space" aria-hidden="true"></span>
    <RouterLink v-if="to" class="map-toggle" :to="to" :aria-label="action.label">
      <ActionIcon :name="action.icon" />
      <span class="map-toggle__text">{{ action.label }}</span>
    </RouterLink>
    <button
      v-else
      type="button"
      class="map-toggle"
      :aria-label="action.label"
      @click="emit('toggle')"
    >
      <ActionIcon :name="action.icon" />
      <span class="map-toggle__text">{{ action.label }}</span>
    </button>
  </div>
</template>

<style scoped>
.map-toggle-root {
  display: block;
}
.map-toggle__space {
  display: block;
  height: calc(var(--tf-control-size) + 24px);
}
/* 固定在右下角，滚动时保持可见；手机端按安全区上移，避免压住手势条。 */
.map-toggle {
  position: fixed;
  right: 16px;
  bottom: calc(16px + env(safe-area-inset-bottom, 0px));
  z-index: 20;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: var(--tf-control-size);
  padding: 0 16px;
  border: 1px solid var(--tf-line);
  border-radius: 999px;
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  box-shadow: var(--tf-shadow-2);
  font: inherit;
  font-weight: 600;
  text-decoration: none;
  cursor: pointer;
  touch-action: manipulation;
}
.map-toggle:focus-visible {
  outline: revert;
  outline-offset: 3px;
}
@media (hover: hover) {
  .map-toggle:hover {
    background: var(--tf-accent-soft);
    color: var(--tf-accent);
    border-color: var(--tf-accent);
  }
}
@media (prefers-reduced-motion: no-preference) {
  .map-toggle {
    transition:
      background-color min(var(--tf-duration-fast), 150ms) var(--tf-ease),
      color min(var(--tf-duration-fast), 150ms) var(--tf-ease),
      border-color min(var(--tf-duration-fast), 150ms) var(--tf-ease);
  }
}
@media print {
  .map-toggle {
    display: none;
  }
}
</style>
