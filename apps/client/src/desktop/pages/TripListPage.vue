<script setup lang="ts">
import { ElAlert, ElCard, ElSkeleton, ElTag } from 'element-plus'
import { onMounted } from 'vue'

import { useMetadataStore } from '@/shared/stores/metadata'

const metadata = useMetadataStore()

onMounted(() => {
  void metadata.load()
})
</script>

<template>
  <ElCard>
    <template #header>旅行</template>
    <ElSkeleton
      v-if="metadata.status === 'loading' || metadata.status === 'idle'"
      :rows="2"
      animated
    />
    <ElAlert
      v-else-if="metadata.status === 'error'"
      type="error"
      :title="metadata.error ?? '无法连接服务端'"
      :closable="false"
      show-icon
    />
    <p v-else-if="metadata.metadata">
      服务端可用：支持 {{ metadata.metadata.currencies.length }} 种币种，默认
      <ElTag size="small">{{ metadata.metadata.default_currency_code }}</ElTag>
      。旅行列表将在旅行模块落地后显示。
    </p>
  </ElCard>
</template>
