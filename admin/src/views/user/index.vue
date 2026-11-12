<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { getUser, listUsers, updateUserStatus } from "@/api/modules/user";
import {
  toUserListView,
  validateUserStatusReason,
  type UserListView
} from "@/api/adapters/access-adapter";
import { useListReturn } from "@/composables/useListReturn";
import { useSubmitGuard } from "@/composables/useSubmitGuard";

/**
 * 用户管理（列表 + 详情 + 账号状态）。
 *
 * 后端真实能力（`internal/logic/iam/user_status.go`）：
 * - `GET /admin/users`            分页列表（`user:read`，id 降序，只支持 page/size）
 * - `GET /admin/users/:id`        详情（`user:read`，不存在 2015）
 * - `GET /admin/users/:id/status` 账号状态（`user:read`）
 * - `PUT /admin/users/:id/status` 禁用/启用（`user:status`，reason 必填）
 *
 * 列表分页完全由服务端负责；页面不请求、也不展示后端未提供的字段
 * （昵称、手机号、最后登录时间、密码哈希等）。
 */
defineOptions({ name: "UserStatus" });

const pageRoot = ref<HTMLElement>();
const canChangeStatus = computed(() => hasPerms(PERMISSION.USER_STATUS));

const rows = ref<UserListView[]>([]);
const total = ref(0);
const page = ref(1);
const size = ref(10);
const loading = ref(false);
const error = ref("");
/** 请求令牌：快速翻页时只接受最后一次响应 */
let loadToken = 0;

const { run: runSubmit, submitting } = useSubmitGuard();

async function load() {
  const token = ++loadToken;
  loading.value = true;
  error.value = "";

  try {
    const result = await listUsers({ page: page.value, size: size.value });
    if (token !== loadToken) return;

    rows.value = (result.items ?? []).map(toUserListView);
    total.value = result.total;
    // 以服务端回显的分页为准（后端会校验并回填默认值）
    page.value = result.page;
    size.value = result.size;
  } catch (cause) {
    if (token !== loadToken) return;
    rows.value = [];
    total.value = 0;
    error.value = resolveErrorMessage(cause, "用户列表加载失败");
  } finally {
    if (token === loadToken) loading.value = false;
  }
}

function handlePageChange(next: number) {
  page.value = next;
  void load();
}

function handleSizeChange(next: number) {
  size.value = next;
  page.value = 1;
  void load();
}

/* -------------------------------------------------------------------------- */
/* 详情                                                                        */
/* -------------------------------------------------------------------------- */

const detailVisible = ref(false);
const detailLoading = ref(false);
const detailError = ref("");
const detail = ref<UserListView | null>(null);
const detailId = ref<number | null>(null);

async function loadDetail(id: number) {
  detailId.value = id;
  detailLoading.value = true;
  detailError.value = "";
  try {
    const dto = await getUser(id);
    detail.value = toUserListView(dto);
  } catch (cause) {
    detail.value = null;
    detailError.value = resolveErrorMessage(cause, "用户详情加载失败");
  } finally {
    detailLoading.value = false;
  }
}

function openDetail(id: number) {
  detailVisible.value = true;
  void loadDetail(id);
}

function retryDetail() {
  if (detailId.value !== null) void loadDetail(detailId.value);
}

/* -------------------------------------------------------------------------- */
/* 禁用 / 启用                                                                 */
/* -------------------------------------------------------------------------- */

/** 禁用/启用：二次确认 + 必填原因，成功后重新拉取详情与列表 */
async function changeStatus(target: 0 | 1) {
  if (!detail.value) return;
  const id = detail.value.id;
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
      // 状态变更后以服务端最新数据为准：同时刷新详情与列表行。
      await loadDetail(id);
      await load();
    } catch (cause) {
      message(resolveErrorMessage(cause, `${actionText}用户失败`), {
        type: "error"
      });
    }
  });
}

onMounted(load);
useListReturn(pageRoot, load);
</script>

<template>
  <div ref="pageRoot" class="user-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">用户管理</h2>
        <p class="page-desc">
          按服务端分页查看用户，查看账号详情并执行禁用/启用；不涉及密码与认证数据
        </p>
      </div>
      <el-button :loading="loading" @click="load"
        ><template #loading><BrandLoadingIcon /></template>
        刷新
      </el-button>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="数据范围说明"
      description="列表与详情来自 GET /admin/users 与 GET /admin/users/:id，仅包含用户 ID、用户名、账号状态与注册时间；后端未提供昵称、手机号、最后登录时间等资料字段。"
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
        <el-table-column label="用户 ID" width="110">
          <template #default="{ row }">{{ row.id }}</template>
        </el-table-column>
        <el-table-column label="用户名" min-width="200">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row.id)">
              {{ row.username }}
            </el-button>
          </template>
        </el-table-column>
        <el-table-column label="账号状态" width="120">
          <template #default="{ row }">
            <el-tag :type="row.statusTagType" size="small">
              {{ row.statusLabel }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="注册时间" min-width="170">
          <template #default="{ row }">{{ row.createdText }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row.id)">
              详情
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty
            :description="error ? '列表加载失败，请重试' : '暂无用户数据'"
          />
        </template>
      </el-table>

      <div class="pagination-bar">
        <el-pagination
          :current-page="page"
          :page-size="size"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @current-change="handlePageChange"
          @size-change="handleSizeChange"
        />
      </div>
    </el-card>

    <el-drawer
      v-model="detailVisible"
      :title="detailId === null ? '用户详情' : `用户详情 · #${detailId}`"
      size="480px"
    >
      <div v-loading="detailLoading" class="detail-body">
        <el-alert
          v-if="detailError"
          type="error"
          :closable="false"
          show-icon
          :title="detailError"
        >
          <template #default>
            <el-button
              v-if="detailId !== null"
              link
              type="primary"
              @click="retryDetail"
            >
              重试
            </el-button>
          </template>
        </el-alert>

        <template v-else-if="detail">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="用户 ID">
              {{ detail.id }}
            </el-descriptions-item>
            <el-descriptions-item label="用户名">
              {{ detail.username }}
            </el-descriptions-item>
            <el-descriptions-item label="账号状态">
              <el-tag :type="detail.statusTagType" size="small">
                {{ detail.statusLabel }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="注册时间">
              {{ detail.createdText }}
            </el-descriptions-item>
          </el-descriptions>

          <div class="actions">
            <el-button
              v-if="canChangeStatus && detail.status === 1"
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
              当前账号没有 user:status 权限，仅可查看账号信息。
            </span>
          </div>

          <p class="muted hint">
            禁用/启用需要填写变更原因，后端会写入状态变更审计并递增
            auth_epoch（禁用时撤销该用户全部 refresh token）。
          </p>
        </template>

        <el-empty
          v-else-if="!detailLoading && !detailError"
          description="暂无详情数据"
        />
      </div>
    </el-drawer>
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

.block-alert {
  margin-bottom: 12px;
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.detail-body {
  min-height: 160px;
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

.hint {
  margin-top: 12px;
  line-height: 1.6;
}
</style>
