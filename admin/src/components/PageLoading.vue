<script setup lang="ts">
import BrandLoadingIcon from "./BrandLoadingIcon.vue";
withDefaults(defineProps<{ text?: string }>(), { text: "正在加载详情…" });
</script>
<template>
  <div class="page-loading" role="status" aria-live="polite">
    <div class="page-loading__label">
      <BrandLoadingIcon /><span>{{ text }}</span>
    </div>
    <el-skeleton :rows="6" animated />
  </div>
</template>
<style scoped>
.page-loading {
  padding: 24px;
  border: 1px solid var(--fw-line);
  border-radius: 6px;
  background: var(--fw-surface);
}
.page-loading__label {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 24px;
  color: var(--fw-muted);
  font-size: 13px;
}
</style>
