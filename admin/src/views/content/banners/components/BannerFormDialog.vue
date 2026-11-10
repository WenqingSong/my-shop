<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { createBanner, updateBanner } from "@/api/modules/banner";
import {
  buildCreateBannerPayload,
  buildUpdateBannerPayload,
  emptyBannerForm,
  hasBannerErrors,
  validateBannerForm,
  type BannerForm,
  type BannerFormErrors,
  type BannerView
} from "@/api/adapters/banner-adapter";
import ProductImageUploader from "@/views/product/components/ProductImageUploader.vue";

/**
 * 轮播图新增/编辑弹窗。
 *
 * 图片复用 A2 的七牛直传组件：只有真正上传成功的 URL 才会写入表单，
 * 前端不接触 AK/SK，也不会把管理员 Token 发往第三方域名。
 */
const props = defineProps<{
  modelValue: boolean;
  mode: "create" | "edit";
  banner?: BannerView | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  saved: [];
}>();

const form = reactive<BannerForm>(emptyBannerForm());
const errors = ref<BannerFormErrors>({});
const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

const title = computed(() =>
  props.mode === "create" ? "新增轮播图" : "编辑轮播图"
);

/** 上传组件需要数组，主图最多 1 张 */
const imageList = computed<string[]>({
  get: () => (form.imageUrl ? [form.imageUrl] : []),
  set: value => {
    form.imageUrl = value[0] ?? "";
  }
});

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    errors.value = {};
    if (props.mode === "edit" && props.banner) {
      Object.assign(form, {
        title: props.banner.title,
        imageUrl: props.banner.imageUrl,
        linkUrl: props.banner.linkUrl,
        sort: props.banner.sort,
        status: props.banner.status
      });
    } else {
      Object.assign(form, emptyBannerForm());
    }
  }
);

async function submit() {
  errors.value = validateBannerForm(form);
  if (hasBannerErrors(errors.value)) {
    message("请检查表单内容", { type: "warning" });
    return;
  }

  await run(async () => {
    try {
      if (props.mode === "create") {
        await createBanner(buildCreateBannerPayload(form));
        message("轮播图已创建", { type: "success" });
      } else if (props.banner) {
        await updateBanner(props.banner.id, buildUpdateBannerPayload(form));
        message("轮播图已更新", { type: "success" });
      }
      emit("saved");
      visible.value = false;
    } catch (cause) {
      message(resolveErrorMessage(cause, "轮播图保存失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog v-model="visible" :title="title" width="620px" destroy-on-close>
    <el-form label-width="96px">
      <el-form-item label="标题" required :error="errors.title">
        <el-input
          v-model="form.title"
          maxlength="64"
          show-word-limit
          placeholder="例如：双十一主会场"
        />
      </el-form-item>

      <el-form-item label="图片" required :error="errors.imageUrl">
        <ProductImageUploader
          v-model="imageList"
          :max="1"
          tip="建议 1920×600；支持 jpg / png / gif / webp，单张不超过 10MB"
        />
      </el-form-item>

      <el-form-item label="跳转链接" :error="errors.linkUrl">
        <el-input
          v-model="form.linkUrl"
          maxlength="512"
          placeholder="可选，例如 /products/1001"
        />
      </el-form-item>

      <el-form-item label="排序" :error="errors.sort">
        <el-input-number
          v-model="form.sort"
          :min="0"
          :max="9999"
          :step="1"
          step-strictly
          controls-position="right"
        />
        <span class="field-hint">越小越靠前</span>
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
      <el-button type="primary" :loading="submitting" @click="submit">
        保存
      </el-button>
    </template>
  </el-dialog>
</template>

<style lang="scss" scoped>
.field-hint {
  margin-left: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
