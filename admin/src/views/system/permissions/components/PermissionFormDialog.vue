<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { createPermission, updatePermission } from "@/api/modules/permission";
import type { PermissionDTO } from "@/api/types/permission";
import {
  buildCreatePermissionPayload,
  buildUpdatePermissionPayload,
  validatePermissionForm,
  type PermissionForm,
  type PermissionFormErrors
} from "@/api/adapters/access-adapter";

/**
 * 权限新增/编辑弹窗。
 *
 * 后端字段只有 code / name / description，校验规则与
 * `internal/logic/admin/admin.go` 一致：code/name trim 后 1~64；描述 ≤255。
 * 权限码由管理员手工录入（后端不自动生成），前端不做任何码值拼装。
 */
const props = defineProps<{
  modelValue: boolean;
  mode: "create" | "edit";
  permission?: PermissionDTO | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  saved: [];
}>();

const form = reactive<PermissionForm>({
  code: "",
  name: "",
  description: ""
});
const errors = ref<PermissionFormErrors>({});
const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

const title = computed(() =>
  props.mode === "create" ? "新增权限" : "编辑权限"
);

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    errors.value = {};
    if (props.mode === "edit" && props.permission) {
      form.code = props.permission.code;
      form.name = props.permission.name;
      form.description = props.permission.description;
    } else {
      form.code = "";
      form.name = "";
      form.description = "";
    }
  }
);

async function submit() {
  errors.value = validatePermissionForm(form);
  if (Object.keys(errors.value).length > 0) {
    message("请检查表单内容", { type: "warning" });
    return;
  }

  await run(async () => {
    try {
      if (props.mode === "create") {
        await createPermission(buildCreatePermissionPayload(form));
        message("权限已创建", { type: "success" });
      } else if (props.permission) {
        const payload = buildUpdatePermissionPayload(props.permission, form);
        if (Object.keys(payload).length === 0) {
          message("内容未变化", { type: "info" });
          visible.value = false;
          return;
        }
        await updatePermission(props.permission.id, payload);
        message("权限已更新", { type: "success" });
      }
      emit("saved");
      visible.value = false;
    } catch (cause) {
      message(resolveErrorMessage(cause, "权限保存失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog
    v-model="visible"
    :title="title"
    width="560px"
    :close-on-click-modal="false"
  >
    <el-form label-width="84px" @submit.prevent>
      <el-form-item label="权限码" :error="errors.code">
        <el-input
          v-model="form.code"
          placeholder="例如 product:create"
          maxlength="64"
          show-word-limit
          :disabled="submitting"
        />
      </el-form-item>
      <el-form-item label="权限名称" :error="errors.name">
        <el-input
          v-model="form.name"
          placeholder="不超过 64 个字符"
          maxlength="64"
          show-word-limit
          :disabled="submitting"
        />
      </el-form-item>
      <el-form-item label="权限描述" :error="errors.description">
        <el-input
          v-model="form.description"
          type="textarea"
          :rows="3"
          placeholder="可选，不超过 255 个字符"
          maxlength="255"
          show-word-limit
          :disabled="submitting"
        />
      </el-form-item>
    </el-form>

    <p class="hint">
      权限码是后端授权使用的稳定
      key；修改已被角色引用的权限码会直接影响这些角色的授权结果。
    </p>

    <template #footer>
      <el-button :disabled="submitting" @click="visible = false">
        取消
      </el-button>
      <el-button type="primary" :loading="submitting" @click="submit">
        保存
      </el-button>
    </template>
  </el-dialog>
</template>

<style lang="scss" scoped>
.hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);
}
</style>
