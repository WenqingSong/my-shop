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
  gap: 8px;
  align-items: flex-start;
  justify-content: center;
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
  color: var(--el-text-color-primary);
  letter-spacing: 0.02em;
}

.sidebar-product > span {
  padding-left: 9px;
  font-weight: 400;
  color: var(--el-text-color-secondary);
  border-left: 1px solid var(--el-border-color);
}

.collapses .sidebar-logo-link {
  align-items: center;
  padding: 12px;
}
</style>
