<template>
  <footer class="layout-footer">
    <span>Framework <span class="footer-divider">/</span> SurgeCart</span
    ><span>运营管理工作台</span>
  </footer>
</template>
<style scoped>
.layout-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin: 0 28px;
  padding: 18px 0;
  font-size: 11px;
  letter-spacing: 0.02em;
  color: var(--el-text-color-secondary);
  border-top: 1px solid var(--el-border-color);
}
.footer-divider {
  margin: 0 10px;
  color: var(--el-text-color-placeholder);
}
</style>
