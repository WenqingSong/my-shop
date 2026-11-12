<script setup lang="ts">
import symbol from "@/assets/brand/symbol.svg?url";
import wordmark from "@/assets/brand/wordmark.svg?url";
withDefaults(defineProps<{ compact?: boolean }>(), { compact: false });
</script>

<template>
  <span
    class="brand-identity"
    :class="{ 'brand-identity--compact': compact }"
    role="img"
    aria-label="Framework"
  >
    <img
      :src="symbol"
      alt=""
      class="brand-identity__symbol"
      width="38"
      height="38"
    />
    <img
      v-if="!compact"
      :src="wordmark"
      alt=""
      class="brand-identity__wordmark"
      width="138"
      height="24"
    />
  </span>
</template>

<style scoped>
.brand-identity {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}
.brand-identity__symbol {
  width: 38px;
  height: 38px;
}
.brand-identity__wordmark {
  width: 138px;
  height: auto;
}
:global(.dark .brand-identity) {
  filter: invert(1);
}
</style>
