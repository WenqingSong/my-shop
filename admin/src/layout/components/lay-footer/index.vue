<template>
  <footer class="layout-footer">
    <span>Framework <span class="footer-divider">/</span> SurgeCart</span
    ><span>运营管理工作台</span>
  </footer>
</template>
<style scoped>
.layout-footer {
  display: flex;
  gap: 16px;
  align-items: center;
  justify-content: space-between;
  padding: 18px 0;
  margin: 0 28px;
  font-size: 11px;
  color: var(--el-text-color-secondary);
  letter-spacing: 0.02em;
  border-top: 1px solid var(--el-border-color);
}

.footer-divider {
  margin: 0 10px;
  color: var(--el-text-color-placeholder);
}
</style>
