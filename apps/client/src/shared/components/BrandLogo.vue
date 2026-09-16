<script setup lang="ts">
import logoUrl from '@/assets/logo.png'

withDefaults(defineProps<{ alt?: string }>(), {
  alt: 'Tripfolio',
})
</script>

<template>
  <img
    class="brand-logo"
    :src="logoUrl"
    :alt="alt"
    width="256"
    height="256"
    decoding="async"
    fetchpriority="high"
    draggable="false"
  />
</template>

<style scoped>
.brand-logo {
  display: block;
  width: var(--tf-brand-logo-size, 40px);
  height: var(--tf-brand-logo-size, 40px);
  object-fit: contain;
  user-select: none;
}
</style>
