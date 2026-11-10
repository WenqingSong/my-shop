<script setup lang="ts">
import { reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { message } from "@/utils/message";
import { getConfig } from "@/config";

defineOptions({
  name: "Login"
});

const router = useRouter();

const form = reactive({
  username: "",
  password: ""
});

const loading = ref(false);

/**
 * A0 阶段不实现管理员登录：不调用任何后端接口、不写入任何 Token。
 * 登录契约（请求/响应 envelope、Token 字段）待后端 B0 交付 OpenAPI 后，
 * 于 A1 阶段在此接入 `src/api/modules/admin` 的请求方法。
 */
async function handleSubmit() {
  loading.value = true;
  try {
    message("管理员登录将在 A1 阶段接入后端契约后启用", { type: "info" });
    await router.push("/dashboard");
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <div class="login-wrapper">
    <div class="login-card">
      <div class="brand">
        <img src="/logo.svg" alt="SurgeCart" class="brand-logo" />
        <div>
          <h1 class="brand-title">{{ getConfig().Title || "SurgeCart" }}</h1>
          <p class="brand-subtitle">管理后台</p>
        </div>
      </div>

      <el-form :model="form" label-position="top" @submit.prevent>
        <el-form-item label="用户名">
          <el-input
            v-model="form.username"
            placeholder="请输入用户名"
            autocomplete="username"
          />
        </el-form-item>
        <el-form-item label="密码">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="请输入密码"
            show-password
            autocomplete="current-password"
          />
        </el-form-item>
        <el-button
          class="submit"
          type="primary"
          :loading="loading"
          @click="handleSubmit"
        >
          进入控制台
        </el-button>
      </el-form>

      <el-alert
        class="hint"
        type="info"
        :closable="false"
        show-icon
        title="当前为 A0 工程初始化阶段，未接入登录认证与权限控制（A1 实现）。"
      />
    </div>
  </div>
</template>

<style lang="scss" scoped>
.login-wrapper {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100vh;
  background: var(--el-bg-color-page);
}

.login-card {
  width: 380px;
  padding: 32px;
  background: var(--el-bg-color);
  border-radius: 8px;
  box-shadow: 0 2px 12px rgb(0 0 0 / 8%);
}

.brand {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-bottom: 24px;

  .brand-logo {
    width: 40px;
    height: 40px;
  }

  .brand-title {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
  }

  .brand-subtitle {
    margin: 2px 0 0;
    font-size: 13px;
    color: var(--el-text-color-secondary);
  }
}

.submit {
  width: 100%;
}

.hint {
  margin-top: 20px;
}
</style>
