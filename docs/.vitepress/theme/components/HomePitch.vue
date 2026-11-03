<script setup lang="ts">
import CostTimeline from './CostTimeline.vue'
import { warmHours } from '../cacheMath'
</script>

<template>
  <section class="home-pitch">
    <h2>Cheaper for {{ Math.floor(warmHours) }} hours straight</h2>
    <p class="lede">
      Every ping reads the cache at a twentieth of the input price and resets its timer.
      Letting it expire means writing the whole context again at twice the input price.
      So pings stay cheaper than one rebuild for a long time, and plans stop on their own when they no longer are.
    </p>
    <CostTimeline />
  </section>
</template>

<style scoped>
.home-pitch {
  max-width: 960px;
  margin: 0 auto;
  padding: 40px 24px 24px;
}
h2 {
  font-size: 28px;
  font-weight: 700;
  line-height: 1.25;
  letter-spacing: -0.01em;
  margin: 0 0 12px;
  text-align: center;
}
.lede {
  max-width: 680px;
  margin: 0 auto 8px;
  font-size: 16px;
  line-height: 1.6;
  color: var(--vp-c-text-2);
  text-align: center;
  text-wrap: balance;
}
@media (min-width: 768px) {
  .home-pitch { padding: 56px 48px 40px; }
  h2 { font-size: 32px; }
}
</style>
