import { computed, ref, type Ref } from "vue";
import type { UploadedImage } from "@/api/types/upload";
import { validateImageFile } from "@/api/modules/upload";

/** 单张图片的上传状态 */
export interface ImageUploadItem {
  uid: string;
  /** 本地文件名，仅用于展示 */
  name: string;
  status: "uploading" | "done" | "error";
  /** 上传成功后的 URL；未成功时为空 */
  url: string;
  /** 0-100 */
  percent: number;
  /** 失败原因 */
  error: string;
  /** 重试需要的原始文件（不参与展示） */
  file?: File;
}

export interface UseImageUploaderOptions {
  /** 最多保留多少张（主图为 1） */
  max: number;
  /** 初始 URL（编辑回显） */
  initial?: string[];
  /** 实际上传实现（便于测试注入） */
  uploader: (
    file: File,
    onProgress?: (percent: number) => void
  ) => Promise<UploadedImage>;
  /** URL 列表变化回调（成功上传 / 删除 / 替换） */
  onChange: (urls: string[]) => void;
  /** 校验或上传失败回调 */
  onError?: (message: string) => void;
}

let uidSeed = 0;
const nextUid = () => {
  uidSeed += 1;
  return `image-${uidSeed}`;
};

/**
 * 图片列表状态管理。
 *
 * 设计要点：
 * - 只有 `status === "done"` 的项会进入 `urls`，上传中/失败的图片不会被当作已选图片；
 * - 上传失败保留失败项，可重试，**不会**用占位 URL 冒充成功；
 * - `max` 在本地拦截，避免用户选满后才发现超出。
 */
export function useImageUploader(options: UseImageUploaderOptions) {
  const items = ref<ImageUploadItem[]>(
    (options.initial ?? []).filter(Boolean).map(url => ({
      uid: nextUid(),
      name: url.split("/").pop() || url,
      status: "done" as const,
      url,
      percent: 100,
      error: ""
    }))
  );

  const urls = computed(() =>
    items.value.filter(item => item.status === "done").map(item => item.url)
  );

  const uploading = computed(() =>
    items.value.some(item => item.status === "uploading")
  );

  const doneCount = computed(() => urls.value.length);

  function emitChange() {
    options.onChange([...urls.value]);
  }

  function report(message: string) {
    options.onError?.(message);
  }

  /** 上传单个条目（失败只写入条目状态，不抛出） */
  async function uploadItem(item: ImageUploadItem) {
    if (!item.file) return;

    item.status = "uploading";
    item.percent = 0;
    item.error = "";

    try {
      const result = await options.uploader(item.file, percent => {
        item.percent = Math.min(99, Math.max(0, percent));
      });
      item.url = result.url;
      item.percent = 100;
      item.status = "done";
      delete item.file;
      emitChange();
    } catch (cause) {
      item.status = "error";
      item.percent = 0;
      item.error =
        cause instanceof Error ? cause.message : "上传失败，请稍后重试";
      report(item.error);
    }
  }

  /**
   * 加入文件并上传。
   *
   * 校验不通过的文件不会进入列表；超过上限的部分直接拒绝并提示。
   */
  async function addFiles(files: File[]): Promise<void> {
    const accepted: File[] = [];

    for (const file of files) {
      const invalid = validateImageFile(file);
      if (invalid) {
        report(`${file.name}：${invalid}`);
        continue;
      }
      if (items.value.length + accepted.length >= options.max) {
        report(`最多上传 ${options.max} 张图片`);
        break;
      }
      accepted.push(file);
    }

    if (accepted.length === 0) return;

    const created = accepted.map(file => {
      const item: ImageUploadItem = {
        uid: nextUid(),
        name: file.name,
        status: "uploading" as const,
        url: "",
        percent: 0,
        error: "",
        file
      };
      items.value.push(item);
      return item;
    });

    await Promise.allSettled(created.map(item => uploadItem(item)));
  }

  /** 重试失败项（复用原始文件） */
  async function retry(uid: string) {
    const item = items.value.find(entry => entry.uid === uid);
    if (!item || item.status !== "error" || !item.file) return;
    await uploadItem(item);
  }

  /** 删除（上传中的条目不允许删除，避免请求回来后又写入） */
  function remove(uid: string) {
    const item = items.value.find(entry => entry.uid === uid);
    if (!item) return;
    if (item.status === "uploading") {
      report("图片上传中，请稍候再删除");
      return;
    }
    items.value = items.value.filter(entry => entry.uid !== uid);
    emitChange();
  }

  /** 外部整体替换（例如切换商品时） */
  function replace(next: string[]) {
    items.value = next.filter(Boolean).map(url => ({
      uid: nextUid(),
      name: url.split("/").pop() || url,
      status: "done" as const,
      url,
      percent: 100,
      error: ""
    }));
    emitChange();
  }

  return {
    items,
    urls,
    uploading,
    doneCount,
    addFiles,
    retry,
    remove,
    replace
  };
}

export type ImageUploaderState = ReturnType<typeof useImageUploader> & {
  items: Ref<ImageUploadItem[]>;
};
