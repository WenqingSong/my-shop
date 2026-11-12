<script setup lang="ts">
import { computed, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { getUserStatus, updateUserStatus } from "@/api/modules/user";
import {
  parsePositiveId,
  toUserStatusView,
  validateUserStatusReason,
  type UserStatusView
} from "@/api/adapters/access-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";

/**
 * 用户账号状态管理。
 *
 * 后端真实能力（A5 只读审查结论）：
 * - 只有 `GET/PUT /admin/users/:id/status`；
 * - **没有**用户列表、用户详情、搜索、分页、删除接口；
 * - `users` 表没有昵称/手机号/最后登录时间字段。
 *
 * 因此本页不伪造用户列表，而是"按用户 ID 查询状态 → 禁用/启用"，
 * 变更原因必填（后端写入 user_status_audits）。
 */
defineOptions({ name: "UserStatus" });

const canChangeStatus = computed(() => hasPerms(PERMISSION.USER_STATUS));

const userIdInput = ref("");
const idError = ref("");
const loading = ref(false);
const error = ref("");
const userView = ref<UserStatusView | null>(null);
/** 最近一次尝试查询的 id：失败后仍可用于"重试" */
const lastAttemptId = ref<number | null>(null);

const { run: runSubmit, submitting } = useSubmitGuard();

/** 查询指定用户状态（成功后覆盖视图；失败展示可重试错误） */
async function load(id: number) {
  lastAttemptId.value = id;
  loading.value = true;
  error.value = "";
  try {
    const dto = await getUserStatus(id);
    userView.value = toUserStatusView(dto);
  } catch (cause) {
    userView.value = null;
    error.value = resolveErrorMessage(cause, "用户状态加载失败");
  } finally {
    loading.value = false;
  }
}

function handleQuery() {
  const id = parsePositiveId(userIdInput.value);
  if (id === null) {
    idError.value = "请输入正确的用户 ID（正整数）";
    return;
  }
  idError.value = "";
  void load(id);
}

function reset() {
  userIdInput.value = "";
  idError.value = "";
  error.value = "";
  userView.value = null;
  lastAttemptId.value = null;
}

/** 禁用/启用：二次确认 + 必填原因，成功后重新拉取状态 */
async function changeStatus(target: 0 | 1) {
  if (!userView.value) return;
  const id = userView.value.id;
  const actionText = target === 0 ? "禁用" : "启用";

  let reason: string | null = null;
  try {
    const result = await ElMessageBox.prompt(
      target === 0
        ? `确认禁用用户 ${id}？禁用后该用户所有登录会话与 refresh token 会立即失效。请输入变更原因（≤255 字符）。`
        : `确认启用用户 ${id}？启用不会恢复此前已失效的登录凭证。请输入变更原因（≤255 字符）。`,
      `${actionText}用户`,
      {
        type: "warning",
        confirmButtonText: actionText,
        cancelButtonText: "取消",
        inputType: "textarea",
        inputPlaceholder: "变更原因（必填）",
        inputValidator: (value: string) =>
          validateUserStatusReason(value ?? "") ?? true
      }
    );
    reason = result.value;
  } catch {
    return;
  }
  if (reason === null) return;

  await runSubmit(async () => {
    try {
      await updateUserStatus(id, { status: target, reason: reason!.trim() });
      message(`已${actionText}用户 ${id}`, { type: "success" });
      // 需求要求：状态变更成功后重新获取用户详情
      await load(id);
    } catch (cause) {
      message(resolveErrorMessage(cause, `${actionText}用户失败`), {
        type: "error"
      });
    }
  });
}

function retry() {
  if (lastAttemptId.value !== null) void load(lastAttemptId.value);
}
</script>

<template>
  <div class="user-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">用户状态管理</h2>
        <p class="page-desc">
          按用户 ID 查询账号状态并执行禁用/启用；不涉及用户密码与认证数据
        </p>
      </div>
      <el-button v-if="lastAttemptId !== null" :loading="loading" @click="retry"
        ><template #loading><BrandLoadingIcon /></template>
        刷新
      </el-button>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="使用说明"
      description="Go 后端只提供 /admin/users/:id/status 的查询与修改，没有用户列表/详情/搜索/分页/删除接口；users 表也没有昵称、手机号、最后登录时间字段。本页因此按 ID 查询，不提供列表。"
    />

    <el-card shadow="never">
      <el-form :inline="true" @submit.prevent>
        <el-form-item label="用户 ID" :error="idError">
          <el-input
            v-model="userIdInput"
            class="id-input"
            placeholder="请输入用户 ID"
            clearable
            @keyup.enter="handleQuery"
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" @click="handleQuery"
            ><template #loading><BrandLoadingIcon /></template>
            查询状态
          </el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-alert
      v-if="error"
      class="block-alert"
      type="error"
      :closable="false"
      show-icon
      :title="error"
    >
      <template #default>
        <el-button link type="primary" @click="retry">重试</el-button>
      </template>
    </el-alert>

    <el-card v-if="userView" v-loading="loading" shadow="never">
      <template #header>
        <div class="card-header">
          <span>用户 #{{ userView.id }}</span>
          <el-tag :type="userView.statusTagType" size="small">
            {{ userView.statusLabel }}
          </el-tag>
        </div>
      </template>

      <el-descriptions :column="2" border>
        <el-descriptions-item label="用户 ID">
          {{ userView.id }}
        </el-descriptions-item>
        <el-descriptions-item label="账号状态">
          <el-tag :type="userView.statusTagType" size="small">
            {{ userView.statusLabel }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="手机号" :span="2">
          <span class="muted">后端未提供该字段</span>
        </el-descriptions-item>
        <el-descriptions-item label="最后登录时间" :span="2">
          <span class="muted">后端未提供该字段</span>
        </el-descriptions-item>
      </el-descriptions>

      <div class="actions">
        <el-button
          v-if="canChangeStatus && userView.status === 1"
          type="danger"
          :loading="submitting"
          @click="changeStatus(0)"
          ><template #loading><BrandLoadingIcon /></template>
          禁用用户
        </el-button>
        <el-button
          v-else-if="canChangeStatus"
          type="success"
          :loading="submitting"
          @click="changeStatus(1)"
          ><template #loading><BrandLoadingIcon /></template>
          启用用户
        </el-button>
        <span v-else class="muted">
          当前账号没有 user:status 权限，仅可查看状态。
        </span>
      </div>
    </el-card>

    <el-card v-else-if="!loading && !error" shadow="never">
      <el-empty
        description="输入用户 ID 查询账号状态（后端没有用户列表接口）"
      />
    </el-card>
  </div>
</template>

<style lang="scss" scoped>
.user-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
}

.page-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.page-desc {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.id-input {
  width: 220px;
}

.block-alert {
  margin-bottom: 0;
}

.card-header {
  display: flex;
  gap: 12px;
  align-items: center;
}

.actions {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 16px;
}

.muted {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
</style>
