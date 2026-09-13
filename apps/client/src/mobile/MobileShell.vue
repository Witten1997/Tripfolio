<script setup lang="ts">
import { NavBar as VanNavBar } from 'vant'
import 'vant/lib/index.css'
import '@/styles/bridge/vant.css'
</script>

<template>
  <div class="mobile-shell">
    <div class="tf-backdrop" aria-hidden="true"></div>
    <VanNavBar title="Tripfolio" fixed placeholder safe-area-inset-top />
    <main class="mobile-shell__main">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.mobile-shell {
  min-height: 100vh;
  background: transparent;
}

.mobile-shell__main {
  padding-bottom: env(safe-area-inset-bottom);
}
</style>
