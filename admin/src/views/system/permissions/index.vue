<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { deletePermission, listPermissions } from "@/api/modules/permission";
import type { PermissionDTO } from "@/api/types/permission";
import {
  filterPermissions,
  groupPermissionsByDomain,
  toPermissionView,
  type PermissionView
} from "@/api/adapters/access-adapter";
import { refreshAccess } from "@/composables/useAccessRefresh";
import PermissionFormDialog from "./components/PermissionFormDialog.vue";

/**
 * 权限管理（系统全部权限定义）。
 *
 * 后端真实能力：列表 / 创建 / 更新 / 删除。
 * - 列表返回**系统全部权限定义**，不是当前管理员的有效权限；
 * - 没有权限树/菜单数据，业务域由真实权限码前缀推导；
 * - 没有分页参数，筛选与分页在完整数据集上执行。
 */
defineOptions({ name: "PermissionManagement" });

const canCreate = computed(() => hasPerms(PERMISSION.PERMISSION_CREATE));
const canUpdate = computed(() => hasPerms(PERMISSION.PERMISSION_UPDATE));
const canDelete = computed(() => hasPerms(PERMISSION.PERMISSION_DELETE));

const allViews = ref<PermissionView[]>([]);
const loading = ref(false);
const error = ref("");
const keyword = ref("");
const domain = ref("");
const page = ref(1);
const size = ref(20);

const formVisible = ref(false);
const formMode = ref<"create" | "edit">("create");
const editingPermission = ref<PermissionDTO | null>(null);

const domainOptions = computed(() =>
  groupPermissionsByDomain(allViews.value).map(group => ({
    value: group.domain,
    label: group.domainLabel
  }))
);

const filtered = computed(() =>
  filterPermissions(allViews.value, keyword.value, domain.value)
);

const rows = computed(() =>
  filtered.value.slice((page.value - 1) * size.value, page.value * size.value)
);

const total = computed(() => filtered.value.length);

watch([keyword, domain], () => {
  page.value = 1;
});

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await listPermissions();
    allViews.value = (result.items ?? []).map(toPermissionView);
  } catch (cause) {
    allViews.value = [];
    error.value = resolveErrorMessage(cause, "权限列表加载失败");
  } finally {
    loading.value = false;
  }
}

function resetFilters() {
  keyword.value = "";
  domain.value = "";
  page.value = 1;
}

function openCreate() {
  formMode.value = "create";
  editingPermission.value = null;
  formVisible.value = true;
}

function openEdit(row: PermissionView) {
  formMode.value = "edit";
  editingPermission.value = row;
  formVisible.value = true;
}

async function handleDelete(row: PermissionView) {
  try {
    await ElMessageBox.confirm(
      `确认删除权限「${row.code}」？后端会级联删除所有角色与该权限的关联，删除后相关接口将不再被这些角色授权。`,
      "删除权限",
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
    await deletePermission(row.id);
    message(`已删除权限 ${row.code}`, { type: "success" });
    await load();
    await refreshAccess();
  } catch (cause) {
    message(resolveErrorMessage(cause, "删除权限失败"), { type: "error" });
  }
}

function handleSizeChange(next: number) {
  size.value = next;
  page.value = 1;
}

onMounted(load);
</script>

<template>
  <div class="permission-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">权限管理</h2>
        <p class="page-desc">
          系统全部权限定义；授权由角色关联完成，本页不修改当前管理员的权限
        </p>
      </div>
      <div class="page-actions">
        <el-button :loading="loading" @click="load">刷新</el-button>
        <el-button v-if="canCreate" type="primary" @click="openCreate">
          新建权限
        </el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="后端能力边界"
      description="GET /admin/permissions 返回系统全部权限定义（不是当前管理员的有效权限，也不是某个角色已有的权限）。后端没有权限树/菜单数据，业务域由真实权限码前缀推导；列表无分页参数，筛选与分页在前端完整数据上执行。"
    />

    <el-card shadow="never">
      <el-form :inline="true" @submit.prevent>
        <el-form-item label="搜索">
          <el-input
            v-model="keyword"
            class="filter-keyword"
            placeholder="权限码 / 名称"
            clearable
          />
        </el-form-item>
        <el-form-item label="业务域">
          <el-select v-model="domain" class="filter-domain">
            <el-option label="全部业务域" value="" />
            <el-option
              v-for="option in domainOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button @click="resetFilters">重置</el-button>
        </el-form-item>
      </el-form>

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
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column label="权限码" min-width="190">
          <template #default="{ row }">
            <el-tag size="small">{{ row.code }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="权限名称" min-width="160" />
        <el-table-column label="业务域" width="130">
          <template #default="{ row }">
            <el-tag size="small" type="info">{{ row.domainLabel }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="描述" min-width="200">
          <template #default="{ row }">
            {{ row.description || "—" }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
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
          <el-empty
            :description="
              error ? '权限列表加载失败' : '暂无权限定义（后端未返回数据）'
            "
          />
        </template>
      </el-table>

      <div class="pagination-bar">
        <el-pagination
          :current-page="page"
          :page-size="size"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next"
          background
          @current-change="page = $event"
          @size-change="handleSizeChange"
        />
      </div>
    </el-card>

    <PermissionFormDialog
      v-model="formVisible"
      :mode="formMode"
      :permission="editingPermission"
      @saved="load"
    />
  </div>
</template>

<style lang="scss" scoped>
.permission-page {
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

.filter-keyword {
  width: 190px;
}

.filter-domain {
  width: 160px;
}

.block-alert {
  margin-bottom: 12px;
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
