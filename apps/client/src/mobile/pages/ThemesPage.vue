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
  <div class="mobile-themes-page">
    <RouterLink class="mobile-themes-page__back" :to="{ name: 'account' }">
      <span aria-hidden="true">←</span>
      <span>返回我的</span>
    </RouterLink>
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
  </div>
</template>

<style scoped>
.mobile-themes-page {
  padding: 20px 16px 32px;
}

.mobile-themes-page__back {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  margin: 0 0 12px 2px;
  color: var(--tf-text-2);
  font-size: 14px;
  font-weight: 600;
  text-decoration: none;
}

.mobile-themes-page__back span:first-child {
  font-size: 20px;
  line-height: 1;
}

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
