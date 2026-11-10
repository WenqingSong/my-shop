<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { createPosition, updatePosition } from "@/api/modules/recommendation";
import {
  buildCreatePositionPayload,
  buildUpdatePositionPayload,
  emptyPositionForm,
  hasPositionErrors,
  validatePositionForm,
  type PositionForm,
  type PositionFormErrors,
  type PositionView
} from "@/api/adapters/recommendation-adapter";

/**
 * 推荐位新增/编辑弹窗。
 *
 * code 创建后不可变：编辑模式下置灰且不提交。
 */
const props = defineProps<{
  modelValue: boolean;
  mode: "create" | "edit";
  position?: PositionView | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  saved: [];
}>();

const form = reactive<PositionForm>(emptyPositionForm());
const errors = ref<PositionFormErrors>({});
const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

const title = computed(() =>
  props.mode === "create" ? "新增推荐位" : "编辑推荐位"
);

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    errors.value = {};
    if (props.mode === "edit" && props.position) {
      Object.assign(form, {
        code: props.position.code,
        name: props.position.name,
        status: props.position.status
      });
    } else {
      Object.assign(form, emptyPositionForm());
    }
  }
);

async function submit() {
  errors.value = validatePositionForm(form, props.mode);
  if (hasPositionErrors(errors.value)) return;

  await run(async () => {
    try {
      if (props.mode === "create") {
        await createPosition(buildCreatePositionPayload(form));
        message("推荐位已创建", { type: "success" });
      } else if (props.position) {
        await updatePosition(
          props.position.id,
          buildUpdatePositionPayload(form)
        );
        message("推荐位已更新", { type: "success" });
      }
      emit("saved");
      visible.value = false;
    } catch (cause) {
      message(resolveErrorMessage(cause, "推荐位保存失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog v-model="visible" :title="title" width="520px" destroy-on-close>
    <el-form label-width="110px">
      <el-form-item label="code" required :error="errors.code">
        <el-input
          v-model="form.code"
          :disabled="mode === 'edit'"
          maxlength="64"
          placeholder="小写字母/数字/连字符，如 home-top"
        />
        <div v-if="mode === 'edit'" class="field-hint">
          后端规定 code 创建后不可变
        </div>
      </el-form-item>

      <el-form-item label="名称" required :error="errors.name">
        <el-input
          v-model="form.name"
          maxlength="64"
          show-word-limit
          placeholder="例如：首页顶部推荐"
        />
      </el-form-item>

      <el-form-item label="状态" :error="errors.status">
        <el-radio-group v-model="form.status">
          <el-radio-button :value="1">启用</el-radio-button>
          <el-radio-button :value="0">禁用</el-radio-button>
        </el-radio-group>
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit"
        ><template #loading><BrandLoadingIcon /></template>
        保存
      </el-button>
    </template>
  </el-dialog>
</template>

<style lang="scss" scoped>
.field-hint {
  margin-top: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
