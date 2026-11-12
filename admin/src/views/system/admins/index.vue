<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import {
  assignAdminRole,
  createAdmin,
  deleteAdmin,
  removeAdminRole,
  updateAdminStatus
} from "@/api/modules/admin";
import { listRoles } from "@/api/modules/role";
import type { CreateAdminResult } from "@/api/types/admin";
import type { RoleDTO } from "@/api/types/role";
import {
  buildCreateAdminPayload,
  parsePositiveId,
  validateCreateAdminForm,
  type CreateAdminFormErrors
} from "@/api/adapters/access-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { refreshAccess } from "@/composables/useAccessRefresh";

/**
 * 管理员管理。
 *
 * 后端真实能力：创建 / 禁用启用 / 删除 / 角色分配（含移除）。
 * **没有**管理员列表与详情接口，因此本页不展示列表，也不伪造管理员数据；
 * 所有针对已有管理员的操作用"目标管理员 ID"定位，由后端校验存在性与保护规则：
 * - 目标为超级管理员 → 2005（后端保护所有超级管理员，不只是最后一个）；
 * - 目标为调用者自身 → 2006。
 */
defineOptions({ name: "AdminManagement" });

const canCreate = computed(() => hasPerms(PERMISSION.ADMIN_CREATE));
const canDisable = computed(() => hasPerms(PERMISSION.ADMIN_DISABLE));
const canDelete = computed(() => hasPerms(PERMISSION.ADMIN_DELETE));
const canAssignRole = computed(() => hasPerms(PERMISSION.ADMIN_ASSIGN_ROLE));
const canListRoles = computed(() => hasPerms(PERMISSION.ROLE_LIST));

/* ------------------------------- 创建管理员 ------------------------------- */

const createForm = reactive({ username: "", password: "" });
const createErrors = ref<CreateAdminFormErrors>({});
const createdAdmin = ref<CreateAdminResult | null>(null);
const { run: runCreate, submitting: creating } = useSubmitGuard();

async function submitCreate() {
  createErrors.value = validateCreateAdminForm(createForm);
  if (Object.keys(createErrors.value).length > 0) return;

  await runCreate(async () => {
    try {
      const result = await createAdmin(buildCreateAdminPayload(createForm));
      createdAdmin.value = result;
      // 不在前端保留密码
      createForm.password = "";
      message(`管理员 ${result.username} 已创建（ID ${result.id}）`, {
        type: "success"
      });
    } catch (cause) {
      message(resolveErrorMessage(cause, "创建管理员失败"), { type: "error" });
    }
  });
}

function resetCreate() {
  createForm.username = "";
  createForm.password = "";
  createErrors.value = {};
  createdAdmin.value = null;
}

/* ------------------------------- 目标管理员 ------------------------------- */

const targetIdInput = ref("");
const targetIdError = ref("");
/** 已确认的目标 id（用于操作按钮的启用状态，不代表后端已确认存在） */
const targetId = ref<number | null>(null);

function resolveTarget(): number | null {
  const id = parsePositiveId(targetIdInput.value);
  if (id === null) {
    targetIdError.value = "请输入正确的管理员 ID（正整数）";
    return null;
  }
  targetIdError.value = "";
  targetId.value = id;
  return id;
}

/** 禁用/启用：二次确认后提交；后端无详情接口，无法回读状态 */
async function handleStatus(next: 0 | 1) {
  const id = resolveTarget();
  if (id === null) return;

  const actionText = next === 1 ? "启用" : "禁用";
  try {
    await ElMessageBox.confirm(
      `确认${actionText}管理员 ${id}？后端禁止操作自身与超级管理员，违规会返回明确错误。`,
      `${actionText}管理员`,
      {
        type: "warning",
        confirmButtonText: actionText,
        cancelButtonText: "取消"
      }
    );
  } catch {
    return;
  }

  try {
    await updateAdminStatus(id, { status: next });
    message(`已提交${actionText}管理员 ${id}`, { type: "success" });
  } catch (cause) {
    message(resolveErrorMessage(cause, `${actionText}管理员失败`), {
      type: "error"
    });
  }
}

/** 删除：高危操作二次确认 */
async function handleDelete() {
  const id = resolveTarget();
  if (id === null) return;

  try {
    await ElMessageBox.confirm(
      `确认删除管理员 ${id}？后端会同时删除其全部角色关联，且无法恢复。`,
      "删除管理员",
      {
        type: "error",
        confirmButtonText: "删除",
        cancelButtonText: "取消"
      }
    );
  } catch {
    return;
  }

  try {
    await deleteAdmin(id);
    message(`已删除管理员 ${id}`, { type: "success" });
  } catch (cause) {
    message(resolveErrorMessage(cause, "删除管理员失败"), { type: "error" });
  }
}

/* -------------------------------- 角色分配 -------------------------------- */

const roles = ref<RoleDTO[]>([]);
const rolesLoading = ref(false);
const rolesError = ref("");
const roleId = ref<number | null>(null);
const { run: runRole, submitting: roleSubmitting } = useSubmitGuard();

async function loadRoles() {
  rolesLoading.value = true;
  rolesError.value = "";
  try {
    const result = await listRoles();
    roles.value = result.items ?? [];
  } catch (cause) {
    roles.value = [];
    rolesError.value = resolveErrorMessage(cause, "角色列表加载失败");
  } finally {
    rolesLoading.value = false;
  }
}

async function handleAssignRole(mode: "assign" | "remove") {
  const id = resolveTarget();
  if (id === null) return;
  if (!roleId.value) {
    message("请选择角色", { type: "warning" });
    return;
  }

  const actionText = mode === "assign" ? "分配" : "移除";
  try {
    await ElMessageBox.confirm(
      `确认${actionText}管理员 ${id} 的角色（角色 ID ${roleId.value}）？` +
        (mode === "assign"
          ? "后端对重复分配是幂等的。"
          : "后端对不存在的关联同样返回成功。"),
      `${actionText}角色`,
      {
        type: "warning",
        confirmButtonText: actionText,
        cancelButtonText: "取消"
      }
    );
  } catch {
    return;
  }

  await runRole(async () => {
    try {
      if (mode === "assign") {
        await assignAdminRole(id, { role_id: roleId.value as number });
      } else {
        await removeAdminRole(id, roleId.value as number);
      }
      message(`已${actionText}角色`, { type: "success" });
      // 角色变更后按需求刷新当前管理员的身份/权限与页面访问权限
      await refreshAccess();
    } catch (cause) {
      message(resolveErrorMessage(cause, `${actionText}角色失败`), {
        type: "error"
      });
    }
  });
}

onMounted(() => {
  if (canListRoles.value) void loadRoles();
});
</script>

<template>
  <div class="admin-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">管理员管理</h2>
        <p class="page-desc">
          创建管理员、禁用启用、删除与角色分配；超级管理员与自身操作由后端保护
        </p>
      </div>
      <el-button v-if="canListRoles" :loading="rolesLoading" @click="loadRoles"
        ><template #loading><BrandLoadingIcon /></template>
        刷新角色
      </el-button>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="使用说明"
      description="输入管理员 ID 执行管理操作，暂不提供管理员列表及详情查询。超级管理员受保护，不能禁用、删除或修改其角色；管理员不能操作自身。"
    />

    <el-row :gutter="16">
      <el-col :xs="24" :lg="12">
        <el-card shadow="never" class="panel">
          <template #header>
            <span>创建管理员</span>
          </template>

          <el-alert
            v-if="!canCreate"
            type="warning"
            :closable="false"
            show-icon
            title="当前账号没有 admin:create 权限"
          />

          <el-form v-else label-width="88px" @submit.prevent>
            <el-form-item label="用户名" :error="createErrors.username">
              <el-input
                v-model="createForm.username"
                placeholder="3~24 位大小写字母或数字"
                autocomplete="off"
                :disabled="creating"
              />
            </el-form-item>
            <el-form-item label="密码" :error="createErrors.password">
              <el-input
                v-model="createForm.password"
                type="password"
                show-password
                placeholder="8~24 位"
                autocomplete="new-password"
                :disabled="creating"
                @keyup.enter="submitCreate"
              />
            </el-form-item>
            <el-form-item>
              <el-button
                type="primary"
                :loading="creating"
                @click="submitCreate"
                ><template #loading><BrandLoadingIcon /></template>
                创建
              </el-button>
              <el-button :disabled="creating" @click="resetCreate">
                重置
              </el-button>
            </el-form-item>
          </el-form>

          <el-descriptions
            v-if="createdAdmin"
            class="created"
            :column="1"
            border
          >
            <el-descriptions-item label="新管理员 ID">
              {{ createdAdmin.id }}
            </el-descriptions-item>
            <el-descriptions-item label="用户名">
              {{ createdAdmin.username }}
            </el-descriptions-item>
          </el-descriptions>

          <p class="hint">
            密码只随本次请求提交，不在前端持久化、不写日志；创建成功后输入框会清空。
          </p>
        </el-card>
      </el-col>

      <el-col :xs="24" :lg="12">
        <el-card shadow="never" class="panel">
          <template #header>
            <span>按 ID 管理已有管理员</span>
          </template>

          <el-form label-width="88px" @submit.prevent>
            <el-form-item label="管理员 ID" :error="targetIdError">
              <el-input
                v-model="targetIdInput"
                placeholder="请输入目标管理员 ID"
                clearable
                @keyup.enter="resolveTarget"
              />
            </el-form-item>
          </el-form>

          <div class="action-row">
            <el-button
              v-if="canDisable"
              type="success"
              :disabled="!targetIdInput"
              @click="handleStatus(1)"
            >
              启用
            </el-button>
            <el-button
              v-if="canDisable"
              type="warning"
              :disabled="!targetIdInput"
              @click="handleStatus(0)"
            >
              禁用
            </el-button>
            <el-button
              v-if="canDelete"
              type="danger"
              :disabled="!targetIdInput"
              @click="handleDelete"
            >
              删除
            </el-button>
          </div>

          <el-divider />

          <div class="role-block">
            <p class="block-title">角色分配</p>

            <el-alert
              v-if="!canAssignRole"
              type="warning"
              :closable="false"
              show-icon
              title="当前账号没有 admin:assign_role 权限"
            />

            <template v-else>
              <el-alert
                v-if="!canListRoles"
                class="block-alert"
                type="warning"
                :closable="false"
                show-icon
                title="缺少 role:list 权限，无法加载角色列表，因此无法选择角色"
              />

              <el-alert
                v-else-if="rolesError"
                class="block-alert"
                type="error"
                :closable="false"
                show-icon
                :title="rolesError"
              >
                <template #default>
                  <el-button link type="primary" @click="loadRoles">
                    重试
                  </el-button>
                </template>
              </el-alert>

              <el-select
                v-if="canListRoles"
                v-model="roleId"
                class="role-select"
                placeholder="选择角色"
                :loading="rolesLoading"
                :disabled="roleSubmitting"
              >
                <el-option
                  v-for="role in roles"
                  :key="role.id"
                  :label="`${role.name}（ID ${role.id}）`"
                  :value="role.id"
                />
              </el-select>

              <div class="action-row">
                <el-button
                  type="primary"
                  :loading="roleSubmitting"
                  :disabled="!roleId"
                  @click="handleAssignRole('assign')"
                  ><template #loading><BrandLoadingIcon /></template>
                  分配角色
                </el-button>
                <el-button
                  type="danger"
                  :loading="roleSubmitting"
                  :disabled="!roleId"
                  @click="handleAssignRole('remove')"
                  ><template #loading><BrandLoadingIcon /></template>
                  移除角色
                </el-button>
              </div>
            </template>

            <p class="hint">
              后端没有"管理员详情/当前角色"查询接口，因此页面无法回显目标管理员已有角色；
              分配/移除均为幂等写操作，提交结果以成功或后端错误为准。
            </p>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<style lang="scss" scoped>
.admin-page {
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

.panel {
  height: 100%;
}

.action-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 8px;
}

.role-select {
  width: 240px;
  margin-bottom: 8px;
}

.block-title {
  margin: 0 0 8px;
  font-size: 14px;
  font-weight: 600;
}

.block-alert {
  margin-bottom: 12px;
}

.created {
  margin-top: 12px;
}

.hint {
  margin: 12px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);
}
</style>
