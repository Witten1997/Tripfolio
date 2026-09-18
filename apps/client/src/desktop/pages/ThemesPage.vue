<script setup lang="ts">
import { ElButton, ElTag } from 'element-plus'
import { ref } from 'vue'

import { useThemeStore } from '@/shared/stores/theme'
import { themes, type ThemeId } from '@/shared/theme/registry'

const theme = useThemeStore()
const switching = ref<ThemeId | null>(null)

async function choose(id: ThemeId) {
  if (switching.value) return
  switching.value = id
  try {
    await theme.apply(id)
  } finally {
    switching.value = null
  }
}
</script>

<template>
  <div class="themes-page">
    <header class="page-heading">
      <RouterLink class="page-back" :to="{ name: 'account' }">
        <span aria-hidden="true">←</span>
        <span>返回我的</span>
      </RouterLink>
      <div>
        <h1>主题中心</h1>
        <p>选择一套界面风格，网页与手机端都会记住你的选择。</p>
      </div>
    </header>
    <div class="theme-grid">
      <article
        v-for="item in themes"
        :key="item.id"
        class="theme-card tf-surface"
        :class="{ active: item.id === theme.current }"
      >
        <div class="theme-preview" :style="{ background: item.preview.canvas }" aria-hidden="true">
          <div class="theme-preview__card" :style="{ background: item.preview.surface }">
            <span class="theme-preview__accent" :style="{ background: item.preview.accent }"></span>
            <span class="theme-preview__line"></span>
            <span class="theme-preview__line short"></span>
          </div>
        </div>
        <div class="theme-copy">
          <div class="theme-title">
            <h2>{{ item.name }}</h2>
            <ElTag v-if="item.id === theme.current" type="success" size="small">当前使用</ElTag>
          </div>
          <p>{{ item.description }}</p>
        </div>
        <ElButton
          type="primary"
          :plain="item.id !== theme.current"
          :disabled="item.id === theme.current"
          :loading="switching === item.id"
          @click="choose(item.id)"
        >
          {{ item.id === theme.current ? '使用中' : '使用此主题' }}
        </ElButton>
      </article>
    </div>
  </div>
</template>

<style scoped>
.themes-page {
  max-width: 1240px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.page-heading {
  padding: 8px 0;
}
.page-back {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 12px;
  color: var(--tf-text-2);
  font-size: 13px;
  font-weight: 600;
  text-decoration: none;
}
.page-back:hover {
  color: var(--tf-accent);
}
.page-back:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 3px;
}
.page-back span:first-child {
  font-size: 18px;
  line-height: 1;
}
.page-heading h1 {
  margin: 0;
  font-size: 28px;
  color: var(--tf-text-1);
}
.page-heading p {
  margin: 8px 0 0;
  color: var(--tf-text-3);
}
.theme-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 20px;
}
.theme-card {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 20px;
  transition:
    transform var(--tf-duration) var(--tf-ease),
    box-shadow var(--tf-duration) var(--tf-ease);
}
.theme-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--tf-shadow-2), var(--tf-surface-highlight);
}
.theme-card.active {
  outline: 2px solid var(--tf-accent);
  outline-offset: 2px;
}
.theme-preview {
  height: 150px;
  border-radius: var(--tf-radius-control);
  padding: 22px;
  box-sizing: border-box;
}
.theme-preview__card {
  height: 100%;
  border-radius: var(--tf-radius-control);
  padding: 14px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.theme-preview__accent {
  width: 44px;
  height: 12px;
  border-radius: 999px;
}
.theme-preview__line {
  height: 8px;
  width: 70%;
  border-radius: 999px;
  background: var(--tf-line);
}
.theme-preview__line.short {
  width: 45%;
}
.theme-title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.theme-title h2 {
  margin: 0;
  font-size: 18px;
}
.theme-copy p {
  margin: 6px 0 0;
  color: var(--tf-text-2);
  line-height: 1.6;
}
</style>
