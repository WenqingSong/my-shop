<script setup lang="ts">
import { reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { FormInstance, FormRules } from "element-plus";
import { getConfig } from "@/config";
import { useUserStoreHook } from "@/store/modules/user";
import { BusinessError } from "@/api/types/envelope";

defineOptions({
  name: "Login"
});

const router = useRouter();
const route = useRoute();
const userStore = useUserStoreHook();

const formRef = ref<FormInstance>();
const loading = ref(false);
const errorText = ref("");

const form = reactive({
  username: "",
  password: ""
});

const rules: FormRules = {
  username: [
    { required: true, message: "请输入管理员用户名", trigger: "blur" }
  ],
  password: [{ required: true, message: "请输入密码", trigger: "blur" }]
};

/**
 * 登录失败文案归一化。
 *
 * - 业务错误（HTTP 200 + code !== 0）：优先展示后端 message，便于排查
 * - HTTP 错误：401 视为账号密码错误，其余给出通用提示
 * - 其它：网络或未知异常
 */
function resolveLoginMessage(error: unknown): string {
  if (error instanceof BusinessError) {
    return error.serverMessage || "登录失败，请稍后重试";
  }
  const status = (error as { response?: { status?: number } })?.response
    ?.status;
  if (status === 401) return "用户名或密码错误";
  if (status === 400) return "请求参数有误，请检查输入";
  if (status) return `登录失败（HTTP ${status}）`;
  return "网络异常，请检查网络连接后重试";
}

/** 只允许站内跳转，避免 redirect 参数被用于开放重定向 */
function resolveRedirectTarget(): string {
  const redirect = route.query.redirect;
  if (typeof redirect !== "string") return "/dashboard";
  if (!redirect.startsWith("/") || redirect.startsWith("//"))
    return "/dashboard";
  if (redirect.startsWith("/login")) return "/dashboard";
  return redirect;
}

async function handleSubmit() {
  if (loading.value) return;

  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;

  errorText.value = "";
  loading.value = true;
  try {
    await userStore.login({
      username: form.username.trim(),
      password: form.password
    });
    await router.replace(resolveRedirectTarget());
  } catch (error) {
    errorText.value = resolveLoginMessage(error);
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

      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-position="top"
        @submit.prevent="handleSubmit"
      >
        <el-form-item label="用户名" prop="username">
          <el-input
            v-model="form.username"
            placeholder="请输入管理员用户名"
            autocomplete="username"
            :disabled="loading"
          />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="请输入密码"
            show-password
            autocomplete="current-password"
            :disabled="loading"
            @keyup.enter="handleSubmit"
          />
        </el-form-item>

        <el-alert
          v-if="errorText"
          class="error"
          type="error"
          :closable="false"
          show-icon
          :title="errorText"
        />

        <el-button
          class="submit"
          type="primary"
          native-type="submit"
          :loading="loading"
          @click="handleSubmit"
        >
          登录
        </el-button>
      </el-form>
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

.error {
  margin-bottom: 16px;
}

.submit {
  width: 100%;
}
</style>
