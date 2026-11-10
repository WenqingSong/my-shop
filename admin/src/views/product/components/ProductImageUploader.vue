<script setup lang="ts">
import { computed, ref, watch } from "vue";
import type { UploadFile } from "element-plus";
import { message } from "@/utils/message";
import { uploadImage, IMAGE_ACCEPT } from "@/api/modules/upload";
import { useImageUploader } from "../composables/useImageUploader";

/**
 * 商品图片上传组件（七牛云直传）。
 *
 * - 上传凭证由 `GET /admin/qiniu/upload/token` 签发，前端不接触 AK/SK；
 * - 支持上传中进度、失败重试、预览、删除；
 * - 只有真正上传成功的图片才会写回 `modelValue`。
 */
const props = withDefaults(
  defineProps<{
    modelValue: string[];
    /** 最多图片数，主图传 1 */
    max?: number;
    disabled?: boolean;
    /** 自定义提示文案 */
    tip?: string;
  }>(),
  { max: 5, disabled: false, tip: "" }
);

const emit = defineEmits<{
  "update:modelValue": [urls: string[]];
}>();

const uploadRef = ref<{ clearFiles: () => void } | null>(null);

const state = useImageUploader({
  max: props.max,
  initial: props.modelValue,
  uploader: uploadImage,
  onChange: urls => emit("update:modelValue", urls),
  onError: text => message(text, { type: "error" })
});

const { items, urls, uploading, doneCount } = state;

const tipText = computed(
  () =>
    props.tip ||
    `支持 jpg / png / gif / webp，单张不超过 10MB，最多 ${props.max} 张`
);

/** 外部值变化（如切换商品、表单重置）时同步内部列表 */
watch(
  () => props.modelValue,
  next => {
    const current = urls.value;
    const same =
      next.length === current.length &&
      next.every((url, index) => url === current[index]);
    if (!same) state.replace(next);
  }
);

async function handleChange(file: UploadFile) {
  // 上传完全由组件自己控制，清空 el-upload 内部列表以允许重复选择同一文件
  uploadRef.value?.clearFiles();
  if (!file.raw) return;
  await state.addFiles([file.raw]);
}
</script>

<template>
  <div class="image-uploader">
    <div class="image-grid">
      <div v-for="item in items" :key="item.uid" class="image-item">
        <el-image
          v-if="item.status === 'done'"
          :src="item.url"
          fit="cover"
          class="image-thumb"
          :preview-src-list="urls"
          preview-teleported
        />
        <div v-else class="image-thumb image-thumb--pending">
          <el-progress
            v-if="item.status === 'uploading'"
            type="circle"
            :percentage="item.percent"
            :width="48"
          />
          <span v-else class="image-error">上传失败</span>
        </div>

        <div class="image-actions">
          <el-button
            v-if="item.status === 'error'"
            link
            type="primary"
            @click="state.retry(item.uid)"
          >
            重试
          </el-button>
          <el-button link type="danger" @click="state.remove(item.uid)">
            删除
          </el-button>
        </div>
      </div>

      <el-upload
        v-if="doneCount < max && !disabled"
        ref="uploadRef"
        class="image-add"
        :auto-upload="false"
        :show-file-list="false"
        :multiple="max > 1"
        :accept="IMAGE_ACCEPT"
        :on-change="handleChange"
      >
        <div class="image-add__inner">
          <span class="image-add__plus">+</span>
          <span>上传图片</span>
        </div>
      </el-upload>
    </div>

    <p class="image-tip">
      {{ tipText }}
      <span v-if="uploading" class="image-tip__uploading">（上传中…）</span>
    </p>
  </div>
</template>

<style lang="scss" scoped>
.image-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.image-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 104px;
}

.image-thumb {
  width: 104px;
  height: 104px;
  overflow: hidden;
  cursor: pointer;
  background: var(--el-fill-color-lighter);
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
}

.image-thumb--pending {
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: default;
}

.image-error {
  font-size: 12px;
  color: var(--el-color-danger);
}

.image-actions {
  display: flex;
  justify-content: center;
}

.image-add {
  width: 104px;
  height: 104px;
}

.image-add__inner {
  display: flex;
  flex-direction: column;
  gap: 4px;
  align-items: center;
  justify-content: center;
  width: 104px;
  height: 104px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  background: var(--el-fill-color-lighter);
  border: 1px dashed var(--el-border-color);
  border-radius: 6px;
}

.image-add__inner:hover {
  color: var(--el-color-primary);
  border-color: var(--el-color-primary);
}

.image-add__plus {
  font-size: 20px;
  line-height: 1;
}

.image-tip {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.image-tip__uploading {
  color: var(--el-color-primary);
}
</style>
