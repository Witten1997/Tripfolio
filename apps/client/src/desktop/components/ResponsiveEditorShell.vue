<script setup lang="ts">
import { Sparkles, X } from '@lucide/vue'
import { ElDialog, ElDrawer } from 'element-plus'
import { computed, onMounted, onUnmounted, ref, type PropType } from 'vue'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  title: { type: String, required: true },
  desktopWidth: { type: String, default: 'min(680px, calc(100vw - 32px))' },
  closeOnPressEscape: { type: Boolean, default: true },
  beforeClose: {
    type: Function as PropType<(done?: () => void) => void | Promise<void>>,
    required: true,
  },
})

const isMobile = ref(false)
let mobileMediaQuery: MediaQueryList | undefined
const shell = computed(() => (isMobile.value ? ElDrawer : ElDialog))

function syncMobileState() {
  isMobile.value = mobileMediaQuery?.matches ?? false
}

onMounted(() => {
  if (typeof window === 'undefined' || !window.matchMedia) return
  mobileMediaQuery = window.matchMedia('(max-width: 767px)')
  syncMobileState()
  mobileMediaQuery.addEventListener('change', syncMobileState)
})

onUnmounted(() => mobileMediaQuery?.removeEventListener('change', syncMobileState))
</script>

<template>
  <component
    :is="shell"
    :model-value="modelValue"
    :title="title"
    :width="isMobile ? undefined : desktopWidth"
    :direction="isMobile ? 'btt' : undefined"
    :size="isMobile ? 'min(94dvh, 780px)' : undefined"
    :align-center="!isMobile"
    append-to-body
    :show-close="false"
    :close-on-click-modal="false"
    :close-on-press-escape="closeOnPressEscape"
    :before-close="beforeClose"
    class="tf-editor-shell"
    destroy-on-close
  >
    <template #header="{ titleId, titleClass }">
      <div class="tf-editor-shell__header">
        <h2 :id="titleId" :class="titleClass">
          <Sparkles aria-hidden="true" />
          <span>{{ title }}</span>
        </h2>
        <button type="button" aria-label="关闭" @click="beforeClose()">
          <X aria-hidden="true" />
        </button>
      </div>
    </template>
    <slot />
    <template v-if="$slots.footer" #footer><slot name="footer" /></template>
  </component>
</template>

<style>
.tf-editor-shell {
  --tf-editor-input: color-mix(in srgb, var(--tf-surface-sunken) 82%, transparent);
  box-sizing: border-box;
  border: 1px solid color-mix(in srgb, white 66%, var(--tf-line-soft));
  background: color-mix(in srgb, var(--tf-surface-raised) 84%, transparent);
  box-shadow:
    var(--tf-shadow-3),
    inset 0 1px 0 color-mix(in srgb, var(--tf-surface-raised) 62%, transparent);
  font-family: Inter, 'PingFang SC', 'Source Han Sans SC', 'Microsoft YaHei', sans-serif;
  -webkit-backdrop-filter: blur(22px) saturate(130%);
  backdrop-filter: blur(22px) saturate(130%);
}

.tf-editor-shell.el-dialog {
  display: flex;
  max-height: min(88dvh, 860px);
  flex-direction: column;
  overflow: hidden;
  border-radius: 28px;
}

.tf-editor-shell.el-drawer {
  max-height: calc(100dvh - 8px);
  border-right: 0;
  border-bottom: 0;
  border-left: 0;
  border-radius: 26px 26px 0 0;
}

.tf-editor-shell .el-dialog__header,
.tf-editor-shell .el-drawer__header {
  flex: 0 0 auto;
  margin: 0;
  padding: 20px 24px 14px;
}

.tf-editor-shell.el-drawer .el-drawer__header {
  position: relative;
  padding-top: 28px;
}

.tf-editor-shell.el-drawer .el-drawer__header::before {
  position: absolute;
  top: 9px;
  left: 50%;
  width: 42px;
  height: 4px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--tf-text-3) 35%, transparent);
  content: '';
  transform: translateX(-50%);
}

.tf-editor-shell__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.tf-editor-shell__header h2 {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 9px;
  margin: 0;
  color: var(--tf-text-1);
  font-size: 20px;
  font-weight: 600;
  line-height: 1.35;
  letter-spacing: 0;
}

.tf-editor-shell__header h2 > svg {
  width: 21px;
  height: 21px;
  flex: 0 0 auto;
  color: var(--tf-accent);
  stroke-width: 1.6;
}

.tf-editor-shell__header > button {
  display: grid;
  width: 34px;
  height: 34px;
  flex: 0 0 auto;
  place-items: center;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: color-mix(in srgb, var(--tf-surface-sunken) 78%, transparent);
  color: var(--tf-text-2);
  cursor: pointer;
}

.tf-editor-shell__header > button svg {
  width: 18px;
  height: 18px;
  stroke-width: 1.5;
}

.tf-editor-shell__header > button:hover {
  background: var(--tf-accent-soft);
  color: var(--tf-accent);
}

.tf-editor-shell__header > button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 2px;
}

.tf-editor-shell .el-dialog__body,
.tf-editor-shell .el-drawer__body {
  min-height: 0;
  flex: 1 1 auto;
  padding: 4px 24px 20px;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.tf-editor-shell .el-dialog__footer,
.tf-editor-shell .el-drawer__footer {
  flex: 0 0 auto;
  padding: 0 24px 18px;
  border: 0;
  background: transparent;
  text-align: right;
}

.tf-editor-shell .el-form-item__label,
.tf-editor-shell legend {
  color: var(--tf-text-1);
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0;
}

.tf-editor-shell .el-input__wrapper,
.tf-editor-shell .el-select__wrapper,
.tf-editor-shell .el-textarea__inner,
.tf-editor-shell .el-input-number .el-input__wrapper,
.tf-editor-shell .el-input-group__append,
.tf-editor-shell .el-input-group__prepend {
  border: 0;
  border-radius: var(--tf-radius-control);
  background: var(--tf-editor-input);
  box-shadow: none;
}

.tf-editor-shell .el-input__wrapper:hover,
.tf-editor-shell .el-select__wrapper:hover,
.tf-editor-shell .el-textarea__inner:hover,
.tf-editor-shell .el-input-number .el-input__wrapper:hover {
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--tf-accent) 28%, transparent);
}

.tf-editor-shell .el-input-group__append,
.tf-editor-shell .el-input-group__prepend {
  padding-inline: 14px;
}

.tf-editor-shell .el-form-item {
  margin-bottom: 18px;
}

.tf-editor-shell .el-input__wrapper.is-focus,
.tf-editor-shell .el-select__wrapper.is-focused,
.tf-editor-shell .el-textarea__inner:focus,
.tf-editor-shell .el-input-number .el-input__wrapper.is-focus {
  box-shadow: inset 0 0 0 2px var(--tf-accent);
}

.tf-editor-shell .el-button {
  border-radius: 999px;
}

.tf-editor-shell .el-radio-group {
  max-width: 100%;
}

.tf-editor-shell .el-radio-button__inner {
  min-height: 36px;
  border-color: var(--tf-line-soft);
}

@media (max-width: 767px) {
  .tf-editor-shell .el-drawer__body {
    padding-right: 16px;
    padding-left: 16px;
  }

  .tf-editor-shell .el-drawer__footer {
    padding: 8px 16px max(14px, env(safe-area-inset-bottom));
  }

  .tf-editor-shell .el-drawer__footer > div {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
  }

  .tf-editor-shell .el-drawer__footer .el-button {
    width: auto;
    min-width: 88px;
    min-height: 44px;
    margin: 0;
  }
}
</style>
