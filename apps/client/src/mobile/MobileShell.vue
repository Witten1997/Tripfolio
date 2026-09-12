<script setup lang="ts">
import { NavBar as VanNavBar } from 'vant'
import 'vant/lib/index.css'
</script>

<template>
  <div class="mobile-shell">
    <VanNavBar title="Tripfolio" fixed placeholder safe-area-inset-top />
    <main class="mobile-shell__main">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.mobile-shell {
  min-height: 100vh;
  background: var(--van-background);
}

.mobile-shell__main {
  padding-bottom: env(safe-area-inset-bottom);
}
</style>
