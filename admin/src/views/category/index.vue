<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { formatServerDateTime } from "@/utils/datetime";
import { PERMISSION } from "@/utils/permission";
import { deleteCategory, listCategories } from "@/api/modules/category";
import type { CategoryDTO } from "@/api/types/category";
import {
  flattenCategoryTree,
  type CategoryRow
} from "@/api/adapters/category-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import CategoryFormDialog from "./components/CategoryFormDialog.vue";

/**
 * 商品分类管理。
 *
 * 说明：后端没有后台分类列表接口，列表直接使用公开的 `GET /categories`
 * （树形、仅启用项）。因此页面**不提供**"查看禁用分类"的入口，
 * 也不会在前端伪造树层级或级联删除。
 */
defineOptions({ name: "CategoryList" });

const canCreate = computed(() => hasPerms(PERMISSION.CATEGORY_CREATE));
const canUpdate = computed(() => hasPerms(PERMISSION.CATEGORY_UPDATE));
const canDelete = computed(() => hasPerms(PERMISSION.CATEGORY_DELETE));

const tree = ref<CategoryDTO[]>([]);
const loading = ref(false);
const error = ref("");
const { run: runDelete } = useSubmitGuard();

const dialogVisible = ref(false);
const dialogMode = ref<"create" | "edit">("create");
const editingCategory = ref<CategoryDTO | null>(null);
const defaultParentId = ref(0);

/** id → 扁平信息（父级名称、层级） */
const rowMeta = computed(() => {
  const map = new Map<number, CategoryRow>();
  for (const row of flattenCategoryTree(tree.value)) map.set(row.id, row);
  return map;
});

const flatRows = computed(() => flattenCategoryTree(tree.value));

function parentNameOf(id: number) {
  return rowMeta.value.get(id)?.parentName || "—";
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await listCategories();
    tree.value = result.items ?? [];
  } catch (cause) {
    tree.value = [];
    error.value = resolveErrorMessage(cause, "分类加载失败");
  } finally {
    loading.value = false;
  }
}

function openCreate(parentId = 0) {
  dialogMode.value = "create";
  editingCategory.value = null;
  defaultParentId.value = parentId;
  dialogVisible.value = true;
}

function openEdit(row: CategoryDTO) {
  dialogMode.value = "edit";
  editingCategory.value = row;
  dialogVisible.value = true;
}

async function handleDelete(row: CategoryDTO) {
  try {
    await ElMessageBox.confirm(
      `确认删除分类「${row.name}」？后端会拒绝删除有子分类或被商品引用的分类，且不会级联删除。`,
      "删除分类",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" }
    );
  } catch {
    return;
  }

  const result = await runDelete(async () => {
    try {
      await deleteCategory(row.id);
      message("分类已删除", { type: "success" });
      await load();
      return true;
    } catch (cause) {
      message(resolveErrorMessage(cause, "分类删除失败"), { type: "error" });
      return false;
    }
  });

  if (result.executed === false) {
    message("正在删除，请勿重复操作", { type: "warning" });
  }
}

onMounted(load);
</script>

<template>
  <div class="category-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">商品分类</h2>
        <p class="page-desc">
          树形层级最多 3 级；删除由后端校验子分类与商品引用，前端不做级联
        </p>
      </div>
      <div class="page-actions">
        <el-button :loading="loading" @click="load"
          ><template #loading><BrandLoadingIcon /></template>刷新</el-button
        >
        <el-button v-if="canCreate" type="primary" @click="openCreate(0)">
          新增分类
        </el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="分类列表仅包含启用项"
      description="后端 GET /categories 返回树形且只含启用分类；禁用分类不会出现在列表中，请谨慎变更状态。"
    />

    <el-alert
      v-if="error"
      type="error"
      :closable="false"
      show-icon
      :title="error"
    >
      <template #default>
        <el-button link type="primary" @click="load">重试</el-button>
      </template>
    </el-alert>

    <el-card shadow="never">
      <el-table
        v-loading="loading"
        :data="tree"
        row-key="id"
        border
        default-expand-all
        :tree-props="{ children: 'children' }"
      >
        <el-table-column label="分类名称" min-width="220">
          <template #default="{ row }">
            <span class="category-name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="分类 ID" width="100">
          <template #default="{ row }">{{ row.id }}</template>
        </el-table-column>
        <el-table-column label="父级分类" min-width="140">
          <template #default="{ row }">
            {{ parentNameOf(row.id) }}
          </template>
        </el-table-column>
        <el-table-column label="排序" width="90" align="center">
          <template #default="{ row }">{{ row.sort }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
              {{ row.status === 1 ? "启用" : "禁用" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="更新时间" min-width="170">
          <template #default="{ row }">
            {{ formatServerDateTime(row.updated_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="canCreate"
              link
              type="primary"
              @click="openCreate(row.id)"
            >
              新增子分类
            </el-button>
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
      </el-table>

      <p class="table-hint">
        共 {{ flatRows.length }} 个分类节点（不含被禁用的子树）
      </p>
    </el-card>

    <CategoryFormDialog
      v-model="dialogVisible"
      :mode="dialogMode"
      :category="editingCategory"
      :default-parent-id="defaultParentId"
      :tree="tree"
      @saved="load"
    />
  </div>
</template>

<style lang="scss" scoped>
.category-page {
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

.category-name {
  font-weight: 500;
}

.table-hint {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
