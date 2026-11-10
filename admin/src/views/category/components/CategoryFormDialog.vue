<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { createCategory, updateCategory } from "@/api/modules/category";
import type { CategoryDTO } from "@/api/types/category";
import {
  buildCategoryParentOptions,
  buildCreateCategoryPayload,
  buildUpdateCategoryPayload,
  emptyCategoryForm,
  toCategoryForm,
  validateCategoryForm,
  type CategoryForm
} from "@/api/adapters/category-adapter";

/**
 * 分类新增 / 编辑对话框。
 *
 * 只提交后端真实支持的字段：parent_id / name / sort / status。
 * 父级下拉会禁用"自身及后代"，避免形成环（后端也会拒绝）。
 */
const props = defineProps<{
  modelValue: boolean;
  mode: "create" | "edit";
  /** 编辑时的原始分类 */
  category?: CategoryDTO | null;
  /** 新增时的默认父级 */
  defaultParentId?: number;
  /** 完整分类树（用于父级下拉） */
  tree: CategoryDTO[];
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  saved: [];
}>();

const form = reactive<CategoryForm>(emptyCategoryForm());
const errors = ref<Partial<Record<"name" | "sort" | "status", string>>>({});
const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

const title = computed(() =>
  props.mode === "create" ? "新增分类" : "编辑分类"
);

const parentOptions = computed(() =>
  buildCategoryParentOptions(
    props.tree,
    props.mode === "edit" ? props.category?.id : null
  )
);

function optionLabel(label: string, depth: number) {
  return `${"　".repeat(depth)}${label}`;
}

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    errors.value = {};
    if (props.mode === "edit" && props.category) {
      Object.assign(form, toCategoryForm(props.category));
    } else {
      Object.assign(form, emptyCategoryForm(props.defaultParentId ?? 0));
    }
  }
);

async function submit() {
  errors.value = validateCategoryForm(form);
  if (Object.values(errors.value).some(Boolean)) return;

  await run(async () => {
    try {
      if (props.mode === "create") {
        await createCategory(buildCreateCategoryPayload(form));
        message("分类已创建", { type: "success" });
      } else if (props.category) {
        await updateCategory(
          props.category.id,
          buildUpdateCategoryPayload(form)
        );
        message("分类已更新", { type: "success" });
      }
      emit("saved");
      visible.value = false;
    } catch (cause) {
      message(resolveErrorMessage(cause, "分类保存失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog v-model="visible" :title="title" width="520px" destroy-on-close>
    <el-form label-width="96px">
      <el-form-item label="分类名称" required :error="errors.name">
        <el-input
          v-model="form.name"
          maxlength="64"
          show-word-limit
          placeholder="请输入分类名称"
        />
      </el-form-item>

      <el-form-item label="父级分类">
        <el-select v-model="form.parentId" class="full-width">
          <el-option
            v-for="option in parentOptions"
            :key="option.value"
            :label="optionLabel(option.label, option.depth)"
            :value="option.value"
            :disabled="option.disabled"
          />
        </el-select>
      </el-form-item>

      <el-form-item label="排序值" :error="errors.sort">
        <el-input-number
          v-model="form.sort"
          :min="0"
          :max="9999"
          :step="1"
          step-strictly
          controls-position="right"
        />
        <span class="field-hint">同级内越小越靠前</span>
      </el-form-item>

      <el-form-item label="状态" :error="errors.status">
        <el-radio-group v-model="form.status">
          <el-radio-button :value="1">启用</el-radio-button>
          <el-radio-button :value="0">禁用</el-radio-button>
        </el-radio-group>
      </el-form-item>
    </el-form>

    <el-alert
      type="warning"
      :closable="false"
      show-icon
      title="禁用分类会从分类列表消失"
      description="后端 GET /categories 只返回启用项；禁用后该分类及其子树不会出现在列表中（数据仍保留）。"
    />

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">
        保存
      </el-button>
    </template>
  </el-dialog>
</template>

<style lang="scss" scoped>
.full-width {
  width: 100%;
}

.field-hint {
  margin-left: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
