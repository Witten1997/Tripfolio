<script setup lang="ts">
import { Cell as VanCell, CellGroup as VanCellGroup, Icon as VanIcon } from 'vant'

import { useThemeStore } from '@/shared/stores/theme'
import { themes, type ThemeId } from '@/shared/theme/registry'

const theme = useThemeStore()

function choose(id: ThemeId) {
  void theme.apply(id)
}
</script>

<template>
  <VanCellGroup inset title="主题中心">
    <VanCell
      v-for="item in themes"
      :key="item.id"
      :title="item.name"
      :label="item.description"
      clickable
      @click="choose(item.id)"
    >
      <template #icon>
        <span class="theme-swatch" aria-hidden="true">
          <i :style="{ background: item.preview.canvas }"></i>
          <i :style="{ background: item.preview.surface }"></i>
          <i :style="{ background: item.preview.accent }"></i>
        </span>
      </template>
      <template #right-icon>
        <VanIcon v-if="item.id === theme.current" name="success" class="theme-check" />
      </template>
    </VanCell>
  </VanCellGroup>
</template>

<style scoped>
.theme-swatch {
  display: inline-flex;
  gap: 4px;
  margin-right: 12px;
  align-self: center;
}
.theme-swatch i {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  border: 1px solid var(--tf-line);
}
.theme-check {
  color: var(--tf-accent);
  font-size: 18px;
  align-self: center;
}
</style>
