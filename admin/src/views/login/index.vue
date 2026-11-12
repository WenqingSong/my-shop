<script setup lang="ts">
import { reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { FormInstance, FormRules } from "element-plus";
import { useColorMode } from "@/composables/useColorMode";
import BrandIdentity from "@/components/BrandIdentity.vue";
import brandSymbol from "@/assets/brand/symbol.svg?url";
import { useUserStoreHook } from "@/store/modules/user";
import { BusinessError } from "@/api/types/envelope";

defineOptions({
  name: "Login"
});

const { mode: colorMode, setMode } = useColorMode();
const themeOptions = [
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
  { value: "system", label: "跟随系统" }
] as const;
const demoMode = import.meta.env.VITE_MOCK_ENABLED === "true";
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

  loading.value = true;
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) {
    loading.value = false;
    return;
  }

  errorText.value = "";
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
  <main class="login-page">
    <section class="login-story" aria-label="Framework 品牌介绍">
      <BrandIdentity />
      <div class="login-story__content">
        <p class="brand-eyebrow">FRAMEWORK / COMMERCE OPERATIONS</p>
        <h1>让运营，<br />井然有序。</h1>
        <p class="login-story__intro">
          从每一件商品，到每一笔订单。<br />在清晰、有序的工作台，掌握业务全貌。
        </p>
        <div class="login-geometry" aria-hidden="true">
          <span class="geometry-axis geometry-axis--x" /><span
            class="geometry-axis geometry-axis--y"
          /><img :src="brandSymbol" alt="" /><span class="geometry-caption"
            >STRUCTURE. CLARITY. CONTROL.</span
          >
        </div>
      </div>
      <div class="login-story__footer">
        <span>SurgeCart</span><span>电商运营管理平台</span>
      </div>
    </section>
    <section class="login-access" aria-labelledby="login-title">
      <span class="login-access__edition"
        >SURGECART CONSOLE
        <div class="login-theme-switch" role="group" aria-label="外观模式">
          <button
            v-for="option in themeOptions"
            :key="option.value"
            type="button"
            :aria-pressed="colorMode === option.value"
            @click="setMode(option.value)"
          >
            {{ option.label }}
          </button>
        </div></span
      >
      <div class="login-form-wrap">
        <p class="brand-eyebrow">WELCOME BACK</p>
        <h2 id="login-title">登录工作台</h2>
        <p class="login-description">使用管理员账号，开始今天的工作。</p>
        <el-alert
          v-if="demoMode"
          class="demo-login-note"
          type="info"
          :closable="false"
          title="演示预览 · 使用模拟数据"
          description="账号 admin / 密码 mock-password"
        />
        <el-form
          ref="formRef"
          :model="form"
          :rules="rules"
          label-position="top"
          @submit.prevent="handleSubmit"
        >
          <el-form-item label="管理员账号" prop="username"
            ><el-input
              v-model="form.username"
              placeholder="请输入管理员用户名"
              autocomplete="username"
              :disabled="loading"
              size="large"
          /></el-form-item>
          <el-form-item label="密码" prop="password"
            ><el-input
              v-model="form.password"
              type="password"
              placeholder="请输入密码"
              show-password
              autocomplete="current-password"
              :disabled="loading"
              size="large"
          /></el-form-item>
          <el-alert
            v-if="errorText"
            role="alert"
            class="login-error"
            type="error"
            :closable="false"
            show-icon
            :title="errorText"
          />
          <el-button
            class="login-submit"
            type="primary"
            native-type="submit"
            :loading="loading"
            size="large"
            ><template #loading><BrandLoadingIcon /></template
            >{{ loading ? "正在登录" : "登录工作台"
            }}<span v-if="!loading" aria-hidden="true">↗</span></el-button
          >
        </el-form>
        <p class="login-help">账号访问遇到问题？请联系系统管理员。</p>
      </div>
      <p class="login-access__footer">
        Framework <span>精密 · 清晰 · 有序</span>
      </p>
    </section>
  </main>
</template>

<style scoped lang="scss">
.login-page {
  display: grid;
  grid-template-columns: 1.08fr 1fr;
  min-height: 100svh;
  background: var(--el-bg-color);
}
.login-story {
  position: relative;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 40px 56px 28px;
  background: var(--fw-paper);
  border-right: 1px solid var(--fw-line);
  overflow: hidden;
}
.login-story__content {
  position: relative;
  margin: 70px 0 35px;
}
.brand-eyebrow {
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.16em;
  color: var(--el-text-color-secondary);
}
.login-story h1 {
  margin: 24px 0;
  font-size: clamp(42px, 4.6vw, 72px);
  font-weight: 550;
  line-height: 1.22;
  letter-spacing: -0.05em;
}
.login-story__intro {
  font-size: 14px;
  line-height: 1.9;
  color: var(--el-text-color-secondary);
}
.login-geometry {
  position: relative;
  display: grid;
  place-items: center;
  width: 100%;
  height: 240px;
  margin-top: 40px;
  border: 1px solid var(--fw-line);
}
.login-geometry img {
  width: 170px;
  height: 170px;
  position: relative;
  background: var(--fw-paper);
  padding: 15px;
}
.geometry-axis {
  position: absolute;
  background: var(--fw-line);
}
.geometry-axis--x {
  height: 1px;
  width: 100%;
}
.geometry-axis--y {
  width: 1px;
  height: 100%;
}
.geometry-caption {
  position: absolute;
  left: 16px;
  bottom: 12px;
  font-size: 9px;
  letter-spacing: 0.12em;
  color: var(--el-text-color-secondary);
}
.login-story__footer {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--el-text-color-secondary);
}
.login-story__footer > span:first-child {
  font-weight: 600;
  color: var(--el-text-color-primary);
}
.login-access {
  position: relative;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  padding: 90px 48px;
}
.login-theme-switch {
  display: flex;
  gap: 4px;
}
.login-theme-switch button {
  padding: 3px 6px;
  border-radius: 3px;
  font-size: 11px;
  letter-spacing: 0;
  border: 1px solid transparent;
  background: transparent;
  color: var(--fw-muted);
}
.login-theme-switch button[aria-pressed="true"] {
  border-color: var(--fw-line);
  color: var(--fw-ink);
  background: var(--fw-soft);
}
.login-access__edition {
  position: absolute;
  top: 44px;
  left: 48px;
  right: 48px;
  display: flex;
  justify-content: space-between;
  font-size: 10px;
  letter-spacing: 0.12em;
  color: var(--el-text-color-secondary);
}
.login-form-wrap {
  width: 100%;
  max-width: 360px;
}
.login-form-wrap h2 {
  margin-top: 18px;
  font-size: 30px;
  font-weight: 600;
  letter-spacing: -0.04em;
}
.login-description {
  margin: 12px 0 36px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.login-form-wrap :deep(.el-form-item) {
  margin-bottom: 24px;
}
.login-form-wrap :deep(.el-form-item__label) {
  font-weight: 500;
  padding-bottom: 10px;
}
.login-form-wrap :deep(.el-input__wrapper) {
  min-height: 46px;
}
.login-submit {
  display: flex;
  justify-content: space-between;
  width: 100%;
  height: 46px;
  margin-top: 10px;
  padding: 0 18px;
}
.login-submit :deep(> span) {
  display: flex;
  justify-content: space-between;
  width: 100%;
}
.login-help {
  margin-top: 24px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--el-text-color-secondary);
}
.login-access__footer {
  position: absolute;
  bottom: 28px;
  left: 48px;
  right: 48px;
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--el-text-color-secondary);
}
.login-error,
.demo-login-note {
  margin-bottom: 20px;
}
:global(.dark .login-geometry img) {
  filter: invert(1);
  background: transparent;
}
@media (width <= 960px) {
  .login-story {
    padding: 32px;
  }
  .login-story__content {
    margin-top: 50px;
  }
  .login-geometry {
    height: 210px;
  }
  .login-access {
    padding-right: 32px;
    padding-left: 32px;
  }
}
@media (width <= 680px) {
  .login-page {
    grid-template-columns: 1fr;
  }
  .login-story {
    padding: 24px;
    border-right: 0;
    border-bottom: 1px solid var(--fw-line);
  }
  .login-story__content,
  .login-story__footer {
    display: none;
  }
  .login-access {
    min-height: calc(100svh - 87px);
    padding: 64px 24px 80px;
  }
  .login-access__edition {
    top: 22px;
    left: 24px;
    right: 24px;
  }
  .login-access__footer {
    left: 24px;
    right: 24px;
    bottom: 24px;
  }
}
</style>
