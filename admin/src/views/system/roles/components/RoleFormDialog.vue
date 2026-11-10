<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { createRole, updateRole } from "@/api/modules/role";
import type { RoleDTO } from "@/api/types/role";
import {
  buildCreateRolePayload,
  buildUpdateRolePayload,
  validateRoleForm,
  type RoleForm,
  type RoleFormErrors
} from "@/api/adapters/access-adapter";

/**
 * 角色新增/编辑弹窗。
 *
 * 后端字段只有 name / description（无状态、无内置角色概念），
 * 校验规则与 `internal/logic/admin/admin.go` 对齐：名称 trim 后 1~64 字符，
 * 描述 trim 后 ≤255 字符。
 */
const props = defineProps<{
  modelValue: boolean;
  mode: "create" | "edit";
  role?: RoleDTO | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  saved: [];
}>();

const form = reactive<RoleForm>({ name: "", description: "" });
const errors = ref<RoleFormErrors>({});
const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

const title = computed(() =>
  props.mode === "create" ? "新增角色" : "编辑角色"
);

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    errors.value = {};
    if (props.mode === "edit" && props.role) {
      form.name = props.role.name;
      form.description = props.role.description;
    } else {
      form.name = "";
      form.description = "";
    }
  }
);

async function submit() {
  errors.value = validateRoleForm(form);
  if (Object.keys(errors.value).length > 0) {
    message("请检查表单内容", { type: "warning" });
    return;
  }

  await run(async () => {
    try {
      if (props.mode === "create") {
        await createRole(buildCreateRolePayload(form));
        message("角色已创建", { type: "success" });
      } else if (props.role) {
        const payload = buildUpdateRolePayload(props.role, form);
        if (Object.keys(payload).length === 0) {
          message("内容未变化", { type: "info" });
          visible.value = false;
          return;
        }
        await updateRole(props.role.id, payload);
        message("角色已更新", { type: "success" });
      }
      emit("saved");
      visible.value = false;
    } catch (cause) {
      message(resolveErrorMessage(cause, "角色保存失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog
    v-model="visible"
    :title="title"
    width="520px"
    :close-on-click-modal="false"
  >
    <el-form label-width="84px" @submit.prevent>
      <el-form-item label="角色名称" :error="errors.name">
        <el-input
          v-model="form.name"
          placeholder="不超过 64 个字符"
          maxlength="64"
          show-word-limit
          :disabled="submitting"
        />
      </el-form-item>
      <el-form-item label="角色描述" :error="errors.description">
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
      后端角色没有启用/停用状态，也没有系统内置角色保护；删除角色会级联删除其权限关联与管理员关联。
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
