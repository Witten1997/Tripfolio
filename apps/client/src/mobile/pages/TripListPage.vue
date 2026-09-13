<script setup lang="ts">
import {
  Cell as VanCell,
  CellGroup as VanCellGroup,
  Loading as VanLoading,
  NoticeBar as VanNoticeBar,
} from 'vant'
import { onMounted } from 'vue'

import { platform } from '@/platform'
import { useMetadataStore } from '@/shared/stores/metadata'

const metadata = useMetadataStore()
// 自检入口只在原生应用或开发模式显示
const showDevTools = platform.isNative || import.meta.env.DEV

onMounted(() => {
  void metadata.load()
})
</script>

<template>
  <VanNoticeBar v-if="metadata.status === 'error'" :text="metadata.error ?? '无法连接服务端'" />
  <VanCellGroup inset title="旅行">
    <VanCell v-if="metadata.status === 'loading' || metadata.status === 'idle'">
      <VanLoading size="20" type="spinner">正在连接服务端…</VanLoading>
    </VanCell>
    <VanCell
      v-else-if="metadata.metadata"
      title="服务端可用"
      :label="`支持 ${metadata.metadata.currencies.length} 种币种，默认 ${metadata.metadata.default_currency_code}`"
    />
  </VanCellGroup>
  <VanCellGroup inset title="外观">
    <VanCell title="主题中心" is-link :to="{ name: 'themes' }" />
  </VanCellGroup>
  <VanCellGroup v-if="showDevTools" inset title="开发工具">
    <VanCell title="本地数据库自检" is-link :to="{ name: 'dev-local-db' }" />
  </VanCellGroup>
</template>
