<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { listPermissions } from "@/api/modules/permission";
import {
  assignRolePermission,
  listRolePermissions,
  removeRolePermission
} from "@/api/modules/role";
import type { RoleDTO } from "@/api/types/role";
import {
  filterPermissions,
  groupPermissionsByDomain,
  toPermissionView,
  type PermissionView
} from "@/api/adapters/access-adapter";
import { refreshAccess } from "@/composables/useAccessRefresh";

/**
 * 角色权限抽屉（读取 + 编辑）。
 *
 * 后端真实能力：
 * - `GET    /admin/roles/:id/permissions`              `role:list`（按权限 id 升序）
 * - `GET    /admin/permissions`                        `permission:list`（全部定义）
 * - `POST   /admin/roles/:id/permissions`              `role:assign_permission`
 * - `DELETE /admin/roles/:id/permissions/:permission_id` `role:assign_permission`
 *
 * 读取失败（网络/500/403）与"角色确实没有权限"必须区分：
 * 前者展示错误并禁用保存，后者展示空勾选但允许继续授权。
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

/** 全部权限定义 */
const views = ref<PermissionView[]>([]);
/** 服务端当前已授权限 id（保存/重置的基线） */
const savedIds = ref<number[]>([]);
/** 界面勾选状态（保存时与 savedIds 求差集） */
const selectedIds = ref<number[]>([]);

const loading = ref(false);
const saving = ref(false);
const error = ref("");
const keyword = ref("");
const domain = ref("");

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

/** 本次保存需要新增的权限 */
const added = computed(() =>
  selectedIds.value.filter(id => !savedIds.value.includes(id))
);
/** 本次保存需要移除的权限 */
const removed = computed(() =>
  savedIds.value.filter(id => !selectedIds.value.includes(id))
);
const dirty = computed(
  () => added.value.length > 0 || removed.value.length > 0
);

async function load() {
  if (!props.role || !props.canList) return;

  const roleId = props.role.id;
  loading.value = true;
  error.value = "";
  try {
    // 两份数据必须同时成功：只拿到一半会把"读取失败"渲染成"没有权限"。
    const [definitions, assigned] = await Promise.all([
      listPermissions(),
      listRolePermissions(roleId)
    ]);
    views.value = (definitions.items ?? []).map(toPermissionView);
    const ids = (assigned.items ?? []).map(item => item.id);
    savedIds.value = ids;
    selectedIds.value = [...ids];
  } catch (cause) {
    views.value = [];
    savedIds.value = [];
    selectedIds.value = [];
    error.value = resolveErrorMessage(cause, "角色权限加载失败");
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.modelValue, props.role?.id] as const,
  ([isOpen]) => {
    if (!isOpen) return;
    keyword.value = "";
    domain.value = "";
    void load();
  }
);

function resetSelection() {
  selectedIds.value = [...savedIds.value];
}

async function save() {
  const role = props.role;
  if (!role || !props.canAssign || saving.value || !dirty.value) return;

  const addedIds = [...added.value];
  const removedIds = [...removed.value];
  const failures: string[] = [];

  saving.value = true;
  try {
    for (const permissionId of addedIds) {
      try {
        await assignRolePermission(role.id, { permission_id: permissionId });
      } catch (cause) {
        failures.push(
          resolveErrorMessage(cause, `授予权限 ${permissionId} 失败`)
        );
      }
    }

    for (const permissionId of removedIds) {
      try {
        await removeRolePermission(role.id, permissionId);
      } catch (cause) {
        failures.push(
          resolveErrorMessage(cause, `移除权限 ${permissionId} 失败`)
        );
      }
    }

    // 无论成功与否都以服务端为准回读：避免界面停留在"看起来保存成功"的本地状态。
    await load();
    await refreshAccess();

    if (failures.length === 0) {
      message(
        `已保存角色权限（新增 ${addedIds.length} 项、移除 ${removedIds.length} 项）`,
        { type: "success" }
      );
    } else {
      message(`部分权限变更失败（${failures.length} 项）：${failures[0]}`, {
        type: "error"
      });
    }
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <el-drawer
    v-model="visible"
    :title="`角色权限 · ${role?.name ?? ''}`"
    size="600px"
  >
    <el-alert
      class="block-alert"
      type="info"
      :closable="false"
      show-icon
      title="权限来源说明"
      description="勾选状态来自 GET /admin/roles/:id/permissions 返回的真实授权集合；保存时只提交新增与移除的差异，保存后会重新读取服务端数据。授予/移除接口本身是幂等的。"
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
          description="暂无权限定义"
        />

        <el-empty
          v-else-if="!loading && !error && groups.length === 0"
          description="没有匹配的权限"
        />

        <el-checkbox-group
          v-else
          v-model="selectedIds"
          :disabled="!canAssign || saving"
        >
          <el-collapse>
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
                <el-checkbox
                  :value="permission.id"
                  class="permission-check"
                  :title="permission.code"
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
                </el-checkbox>
              </div>
            </el-collapse-item>
          </el-collapse>
        </el-checkbox-group>
      </div>

      <p v-if="!canAssign" class="hint">
        当前账号没有 role:assign_permission 权限，仅可查看角色的已授权限。
      </p>
    </template>

    <template #footer>
      <div class="drawer-footer">
        <span class="footer-summary">
          已选 {{ selectedIds.length }} / 共 {{ total }} 项
          <template v-if="dirty">
            （待新增 {{ added.length }}，待移除 {{ removed.length }}）
          </template>
        </span>
        <div class="footer-actions">
          <el-button :disabled="!dirty || saving" @click="resetSelection">
            重置
          </el-button>
          <el-button
            type="primary"
            :loading="saving"
            :disabled="!canAssign || !dirty || Boolean(error)"
            @click="save"
          >
            <template #loading><BrandLoadingIcon /></template>
            保存变更
          </el-button>
        </div>
      </div>
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
  padding: 8px 0;
  border-bottom: 1px dashed var(--el-border-color-lighter);
}

.permission-check {
  display: flex;
  align-items: flex-start;
  width: 100%;
  height: auto;

  :deep(.el-checkbox__input) {
    margin-top: 2px;
  }

  :deep(.el-checkbox__label) {
    flex: 1;
    min-width: 0;
    white-space: normal;
  }
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

.hint {
  margin-top: 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.drawer-footer {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
}

.footer-summary {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.footer-actions {
  display: flex;
  flex-shrink: 0;
  gap: 8px;
}
</style>
