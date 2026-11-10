<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { deleteRole, listRoles } from "@/api/modules/role";
import type { RoleDTO } from "@/api/types/role";
import { toRoleView, type RoleView } from "@/api/adapters/access-adapter";
import { refreshAccess } from "@/composables/useAccessRefresh";
import RoleFormDialog from "./components/RoleFormDialog.vue";
import RolePermissionDrawer from "./components/RolePermissionDrawer.vue";

/**
 * 角色管理。
 *
 * 后端真实能力：列表 / 创建 / 更新（名称、描述）/ 删除，
 * 以及角色权限的授予/移除（写接口）。
 *
 * 不提供：
 * - 角色状态启停（roles 表没有 status 字段）；
 * - 权限数量列（没有"角色-权限"读接口，无法计算真实数量）；
 * - 内置角色保护（后端没有该概念）。
 */
defineOptions({ name: "RoleManagement" });

const canCreate = computed(() => hasPerms(PERMISSION.ROLE_CREATE));
const canUpdate = computed(() => hasPerms(PERMISSION.ROLE_UPDATE));
const canDelete = computed(() => hasPerms(PERMISSION.ROLE_DELETE));
const canAssignPermission = computed(() =>
  hasPerms(PERMISSION.ROLE_ASSIGN_PERMISSION)
);
const canListPermission = computed(() => hasPerms(PERMISSION.PERMISSION_LIST));

const rows = ref<RoleView[]>([]);
const loading = ref(false);
const error = ref("");

const formVisible = ref(false);
const formMode = ref<"create" | "edit">("create");
const editingRole = ref<RoleDTO | null>(null);

const permissionVisible = ref(false);
const permissionRole = ref<RoleDTO | null>(null);

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await listRoles();
    rows.value = (result.items ?? []).map(toRoleView);
  } catch (cause) {
    rows.value = [];
    error.value = resolveErrorMessage(cause, "角色列表加载失败");
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  formMode.value = "create";
  editingRole.value = null;
  formVisible.value = true;
}

function openEdit(role: RoleView) {
  formMode.value = "edit";
  editingRole.value = role;
  formVisible.value = true;
}

function openPermissions(role: RoleView) {
  permissionRole.value = role;
  permissionVisible.value = true;
}

async function handleDelete(role: RoleView) {
  try {
    await ElMessageBox.confirm(
      `确认删除角色「${role.name}」？后端会级联删除该角色的全部权限关联与管理员关联，且无法恢复。`,
      "删除角色",
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
    await deleteRole(role.id);
    message(`已删除角色「${role.name}」`, { type: "success" });
    await load();
    // 角色删除会改变其成员的有效权限，刷新当前管理员的权限与菜单
    await refreshAccess();
  } catch (cause) {
    message(resolveErrorMessage(cause, "删除角色失败"), { type: "error" });
  }
}

onMounted(load);
</script>

<template>
  <div class="role-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">角色管理</h2>
        <p class="page-desc">
          角色定义与权限分配；权限码来自后端权限定义，角色字段只有名称与描述
        </p>
      </div>
      <div class="page-actions">
        <el-button :loading="loading" @click="load"
          ><template #loading><BrandLoadingIcon /></template>刷新</el-button
        >
        <el-button v-if="canCreate" type="primary" @click="openCreate">
          新建角色
        </el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="使用说明"
      description="维护角色名称与说明，并执行权限授予或移除。暂不支持查询角色已拥有的权限和权限数量，请谨慎操作。"
    />

    <el-card shadow="never">
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

      <el-table v-loading="loading" :data="rows" border stripe>
        <el-table-column prop="id" label="ID" width="90" />
        <el-table-column prop="name" label="角色名称" min-width="180" />
        <el-table-column label="角色描述" min-width="260">
          <template #default="{ row }">{{ row.descriptionText }}</template>
        </el-table-column>
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="canUpdate"
              link
              type="primary"
              @click="openEdit(row)"
            >
              编辑
            </el-button>
            <el-button
              v-if="canAssignPermission"
              link
              type="warning"
              @click="openPermissions(row)"
            >
              权限分配
            </el-button>
            <el-button
              v-if="canDelete"
              link
              type="danger"
              @click="handleDelete(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>

        <template #empty>
          <el-empty description="暂无角色" />
        </template>
      </el-table>
    </el-card>

    <RoleFormDialog
      v-model="formVisible"
      :mode="formMode"
      :role="editingRole"
      @saved="load"
    />

    <RolePermissionDrawer
      v-model="permissionVisible"
      :role="permissionRole"
      :can-assign="canAssignPermission"
      :can-list="canListPermission"
    />
  </div>
</template>

<style lang="scss" scoped>
.role-page {
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

.page-actions {
  display: flex;
  gap: 8px;
}

.block-alert {
  margin-bottom: 12px;
}
</style>
