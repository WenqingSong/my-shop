<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { listPermissions } from "@/api/modules/permission";
import { assignRolePermission, removeRolePermission } from "@/api/modules/role";
import type { RoleDTO } from "@/api/types/role";
import {
  filterPermissions,
  groupPermissionsByDomain,
  toPermissionView,
  type PermissionView
} from "@/api/adapters/access-adapter";
import { refreshAccess } from "@/composables/useAccessRefresh";

/**
 * 角色权限分配抽屉。
 *
 * 后端真实能力：
 * - `POST   /admin/roles/:id/permissions`              `role:assign_permission`
 * - `DELETE /admin/roles/:id/permissions/:permission_id` `role:assign_permission`
 * - `GET    /admin/permissions`                        `permission:list`
 *
 * **没有**"查询角色已有权限"接口，因此这里不显示勾选状态：
 * 每个权限只提供"授予/移除"两个幂等写操作，避免用前端状态伪装后端事实。
 */
const props = defineProps<{
  modelValue: boolean;
  role?: RoleDTO | null;
  canAssign: boolean;
  canList: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
}>();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

const views = ref<PermissionView[]>([]);
const loading = ref(false);
const error = ref("");
const keyword = ref("");
const domain = ref("");
const pendingId = ref<number | null>(null);

const domainOptions = computed(() =>
  groupPermissionsByDomain(views.value).map(group => ({
    value: group.domain,
    label: group.domainLabel
  }))
);

const groups = computed(() =>
  groupPermissionsByDomain(
    filterPermissions(views.value, keyword.value, domain.value)
  )
);

const total = computed(() => views.value.length);

async function load() {
  if (!props.canList) return;
  loading.value = true;
  error.value = "";
  try {
    const result = await listPermissions();
    views.value = (result.items ?? []).map(toPermissionView);
  } catch (cause) {
    views.value = [];
    error.value = resolveErrorMessage(cause, "权限列表加载失败");
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    keyword.value = "";
    domain.value = "";
    void load();
  }
);

async function grant(permission: PermissionView) {
  if (!props.role || pendingId.value !== null) return;
  pendingId.value = permission.id;
  try {
    await assignRolePermission(props.role.id, {
      permission_id: permission.id
    });
    message(`已授予 ${permission.code}（后端幂等）`, { type: "success" });
    await refreshAccess();
  } catch (cause) {
    message(resolveErrorMessage(cause, "授予权限失败"), { type: "error" });
  } finally {
    pendingId.value = null;
  }
}

async function revoke(permission: PermissionView) {
  if (!props.role || pendingId.value !== null) return;

  try {
    await ElMessageBox.confirm(
      `确认从角色「${props.role.name}」移除权限 ${permission.code}？` +
        "后端对不存在的关联同样返回成功，且页面无法回显移除前的状态。",
      "移除权限",
      {
        type: "warning",
        confirmButtonText: "移除",
        cancelButtonText: "取消"
      }
    );
  } catch {
    return;
  }

  pendingId.value = permission.id;
  try {
    await removeRolePermission(props.role.id, permission.id);
    message(`已移除 ${permission.code}（后端幂等）`, { type: "success" });
    await refreshAccess();
  } catch (cause) {
    message(resolveErrorMessage(cause, "移除权限失败"), { type: "error" });
  } finally {
    pendingId.value = null;
  }
}
</script>

<template>
  <el-drawer
    v-model="visible"
    :title="`角色权限 · ${role?.name ?? ''}`"
    size="560px"
  >
    <el-alert
      class="block-alert"
      type="info"
      :closable="false"
      show-icon
      title="后端能力边界"
      description="没有「查询角色已有权限」接口，因此本页不显示勾选状态；授予/移除都是幂等写操作，提交后以成功或后端错误为准。权限码全部来自后端 GET /admin/permissions。"
    />

    <el-alert
      v-if="!canList"
      type="warning"
      :closable="false"
      show-icon
      title="当前账号没有 permission:list 权限，无法加载权限定义"
    />

    <template v-else>
      <el-alert
        v-if="error"
        class="block-alert"
        type="error"
        :closable="false"
        show-icon
        :title="error"
      >
        <template #default>
          <el-button link type="primary" @click="load">重试</el-button>
        </template>
      </el-alert>

      <el-form :inline="true" @submit.prevent>
        <el-form-item label="筛选">
          <el-input
            v-model="keyword"
            placeholder="权限码 / 名称"
            clearable
            class="filter-keyword"
          />
        </el-form-item>
        <el-form-item>
          <el-select
            v-model="domain"
            class="filter-domain"
            placeholder="全部业务域"
          >
            <el-option label="全部业务域" value="" />
            <el-option
              v-for="option in domainOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </el-select>
        </el-form-item>
      </el-form>

      <div v-loading="loading" class="permission-list">
        <el-empty
          v-if="!loading && !error && total === 0"
          description="后端暂无权限定义"
        />

        <el-empty
          v-else-if="!loading && !error && groups.length === 0"
          description="没有匹配的权限"
        />

        <el-collapse v-else>
          <el-collapse-item
            v-for="group in groups"
            :key="group.domain"
            :name="group.domain"
          >
            <template #title>
              <span class="domain-title">
                {{ group.domainLabel }}
                <el-tag size="small" type="info">{{
                  group.items.length
                }}</el-tag>
              </span>
            </template>

            <div
              v-for="permission in group.items"
              :key="permission.id"
              class="permission-row"
            >
              <div class="permission-meta">
                <div class="permission-name">
                  {{ permission.name }}
                  <el-tag size="small">{{ permission.code }}</el-tag>
                </div>
                <div class="permission-desc">
                  {{ permission.description || "无描述" }}
                </div>
              </div>
              <div class="permission-actions">
                <el-button
                  link
                  type="primary"
                  :disabled="!canAssign || pendingId !== null"
                  :loading="pendingId === permission.id"
                  @click="grant(permission)"
                >
                  授予
                </el-button>
                <el-button
                  link
                  type="danger"
                  :disabled="!canAssign || pendingId !== null"
                  @click="revoke(permission)"
                >
                  移除
                </el-button>
              </div>
            </div>
          </el-collapse-item>
        </el-collapse>
      </div>

      <p v-if="!canAssign" class="hint">
        当前账号没有 role:assign_permission 权限，仅可查看权限定义。
      </p>
    </template>
  </el-drawer>
</template>

<style lang="scss" scoped>
.block-alert {
  margin-bottom: 12px;
}

.filter-keyword {
  width: 180px;
}

.filter-domain {
  width: 160px;
}

.permission-list {
  min-height: 160px;
}

.domain-title {
  display: flex;
  gap: 8px;
  align-items: center;
  font-weight: 600;
}

.permission-row {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  justify-content: space-between;
  padding: 8px 0;
  border-bottom: 1px dashed var(--el-border-color-lighter);
}

.permission-meta {
  min-width: 0;
}

.permission-name {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  font-size: 13px;
}

.permission-desc {
  margin-top: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.permission-actions {
  display: flex;
  flex-shrink: 0;
  gap: 4px;
}

.hint {
  margin-top: 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
