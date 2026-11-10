<script setup lang="ts">
import { computed } from "vue";
import { version } from "../../../package.json";

defineOptions({
  name: "Dashboard"
});

const env = import.meta.env;

const runtimeInfo = computed(() => [
  {
    label: "运行模式",
    value: env.MODE
  },
  {
    label: "API 基础地址",
    value: env.VITE_API_BASE_URL
  },
  {
    label: "部署基础路径",
    value: env.VITE_PUBLIC_PATH
  },
  {
    label: "Mock",
    value: env.VITE_MOCK_ENABLED === "true" ? "已启用" : "未启用"
  }
]);
</script>

<template>
  <div class="dashboard-container">
    <el-card shadow="never">
      <template #header>
        <div class="card-header">
          <span class="title">SurgeCart 管理后台</span>
          <el-tag type="success" effect="plain">v{{ version }}</el-tag>
        </div>
      </template>
      <p class="desc">
        当前为 A0
        工程初始化阶段，仅包含基础设施：布局、路由、请求层、类型生成工具与 Mock
        能力。业务模块将在后续阶段按契约逐步接入。
      </p>
    </el-card>

    <el-card shadow="never" class="info-card">
      <template #header>
        <span class="title">运行环境</span>
      </template>
      <el-descriptions :column="2" border>
        <el-descriptions-item
          v-for="item in runtimeInfo"
          :key="item.label"
          :label="item.label"
        >
          {{ item.value }}
        </el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>

<style lang="scss" scoped>
.dashboard-container {
  padding: 16px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.title {
  font-size: 16px;
  font-weight: 600;
}

.desc {
  margin: 0;
  line-height: 1.8;
  color: var(--el-text-color-regular);
}

.info-card {
  margin-top: 16px;
}
</style>
