<script setup lang="ts">
import { getTopMenu } from "@/router/utils";
import BrandIdentity from "@/components/BrandIdentity.vue";
defineProps<{ collapse: boolean }>();
</script>

<template>
  <div class="sidebar-logo-container" :class="{ collapses: collapse }">
    <router-link
      class="sidebar-logo-link"
      :to="getTopMenu()?.path ?? '/'"
      aria-label="Framework · SurgeCart 工作台"
    >
      <BrandIdentity :compact="collapse" />
      <span v-if="!collapse" class="sidebar-product"
        >SurgeCart <span>管理后台</span></span
      >
    </router-link>
  </div>
</template>

<style scoped>
.sidebar-logo-container {
  height: 104px;
  overflow: hidden;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.sidebar-logo-link {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  justify-content: center;
  gap: 8px;
  height: 100%;
  padding: 16px 22px;
}
.sidebar-product {
  display: flex;
  gap: 9px;
  align-items: center;
  padding-left: 4px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--el-text-color-primary);
}
.sidebar-product > span {
  padding-left: 9px;
  border-left: 1px solid var(--el-border-color);
  color: var(--el-text-color-secondary);
  font-weight: 400;
}
.collapses .sidebar-logo-link {
  padding: 12px;
  align-items: center;
}
</style>
