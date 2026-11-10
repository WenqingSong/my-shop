<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { deleteBanner, listBanners, updateBanner } from "@/api/modules/banner";
import { toBannerView, type BannerView } from "@/api/adapters/banner-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import BannerFormDialog from "./components/BannerFormDialog.vue";

/**
 * 轮播图管理。
 *
 * 后台列表返回全部状态（无分页参数），因此分页在前端完整数据上执行。
 * 启停通过 update 的 status 字段完成；没有独立启停接口。
 */
defineOptions({ name: "BannerList" });

const canCreate = computed(() => hasPerms(PERMISSION.BANNER_CREATE));
const canUpdate = computed(() => hasPerms(PERMISSION.BANNER_UPDATE));
const canDelete = computed(() => hasPerms(PERMISSION.BANNER_DELETE));

const allItems = ref<BannerView[]>([]);
const loading = ref(false);
const error = ref("");
const page = ref(1);
const size = ref(10);

const { run: runToggle } = useSubmitGuard();
const { run: runDelete } = useSubmitGuard();

const rows = computed(() => {
  const start = (page.value - 1) * size.value;
  return allItems.value.slice(start, start + size.value);
});
const total = computed(() => allItems.value.length);

const dialogVisible = ref(false);
const dialogMode = ref<"create" | "edit">("create");
const editing = ref<BannerView | null>(null);

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await listBanners();
    allItems.value = (result.items ?? []).map(toBannerView);
  } catch (cause) {
    allItems.value = [];
    error.value = resolveErrorMessage(cause, "轮播图列表加载失败");
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  dialogMode.value = "create";
  editing.value = null;
  dialogVisible.value = true;
}

function openEdit(row: BannerView) {
  dialogMode.value = "edit";
  editing.value = row;
  dialogVisible.value = true;
}

async function toggleStatus(row: BannerView) {
  const target = row.status === 1 ? 0 : 1;
  const action = target === 1 ? "启用" : "禁用";

  try {
    await ElMessageBox.confirm(
      `确认${action}轮播图「${row.title}」？`,
      `${action}轮播图`,
      { type: "warning" }
    );
  } catch {
    return;
  }

  await runToggle(async () => {
    try {
      await updateBanner(row.id, { status: target as 0 | 1 });
      message(`${action}成功`, { type: "success" });
      await load();
    } catch (cause) {
      message(resolveErrorMessage(cause, `${action}失败`), { type: "error" });
    }
  });
}

async function handleDelete(row: BannerView) {
  try {
    await ElMessageBox.confirm(
      `确认删除轮播图「${row.title}」？删除后不可恢复。`,
      "删除轮播图",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" }
    );
  } catch {
    return;
  }

  await runDelete(async () => {
    try {
      await deleteBanner(row.id);
      message("轮播图已删除", { type: "success" });
      if (rows.value.length === 0 && page.value > 1) page.value -= 1;
      await load();
    } catch (cause) {
      message(resolveErrorMessage(cause, "删除失败"), { type: "error" });
    }
  });
}

function handleSaved() {
  void load();
}

function handlePageChange(next: number) {
  page.value = next;
}

function handleSizeChange(next: number) {
  size.value = next;
  page.value = 1;
}

onMounted(load);
</script>

<template>
  <div class="banner-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">轮播图管理</h2>
        <p class="page-desc">
          图片经七牛直传；启停通过更新接口的 status 字段完成
        </p>
      </div>
      <div class="page-actions">
        <el-button :loading="loading" @click="load">刷新</el-button>
        <el-button v-if="canCreate" type="primary" @click="openCreate">
          新增轮播图
        </el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="后端能力说明"
      description="后台轮播图列表一次性返回全部状态数据（无分页参数），分页在前端执行；没有展示位置与有效期字段。"
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
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column label="图片" width="140">
          <template #default="{ row }">
            <el-image
              v-if="row.imageUrl"
              :src="row.imageUrl"
              fit="cover"
              class="banner-thumb"
              :preview-src-list="[row.imageUrl]"
              preview-teleported
            />
            <span v-else>—</span>
          </template>
        </el-table-column>
        <el-table-column prop="title" label="标题" min-width="160" />
        <el-table-column label="跳转链接" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.linkUrl || "—" }}
          </template>
        </el-table-column>
        <el-table-column prop="sort" label="排序" width="90" align="center" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.statusTagType" size="small">
              {{ row.statusLabel }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="更新时间" min-width="170">
          <template #default="{ row }">{{ row.updatedText }}</template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right">
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
              v-if="canUpdate"
              link
              :type="row.status === 1 ? 'warning' : 'success'"
              @click="toggleStatus(row)"
            >
              {{ row.status === 1 ? "禁用" : "启用" }}
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
      </el-table>

      <div class="pagination-bar">
        <el-pagination
          :current-page="page"
          :page-size="size"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next"
          background
          @current-change="handlePageChange"
          @size-change="handleSizeChange"
        />
      </div>
    </el-card>

    <BannerFormDialog
      v-model="dialogVisible"
      :mode="dialogMode"
      :banner="editing"
      @saved="handleSaved"
    />
  </div>
</template>

<style lang="scss" scoped>
.banner-page {
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

.banner-thumb {
  width: 120px;
  height: 48px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
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
