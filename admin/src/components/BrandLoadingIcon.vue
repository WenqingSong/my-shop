<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from "vue";
import symbol from "@/assets/brand/symbol.svg?raw";
const root = ref<HTMLElement>();
let player: FrameworkMotionPlayer | undefined;
onMounted(() => {
  const svg = root.value?.querySelector("svg");
  if (svg) player = window.FrameworkMotion?.create(svg, { loop: true });
});
onBeforeUnmount(() => player?.cancel());
</script>
<template>
  <span
    ref="root"
    class="brand-loading-icon"
    aria-hidden="true"
    v-html="symbol"
  />
</template>
<style scoped>
.brand-loading-icon {
  display: inline-flex;
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  color: currentColor;
}
.brand-loading-icon :deep(svg) {
  display: block;
  width: 100%;
  height: 100%;
  color: inherit;
}
</style>
