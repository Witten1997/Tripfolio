<script setup lang="ts">
import { ElCard, ElEmpty } from 'element-plus'

defineProps<{ title: string; slice: string }>()
</script>

<template>
  <ElCard shadow="never">
    <ElEmpty :description="`${title}页签将在${slice}提供`" />
  </ElCard>
</template>
