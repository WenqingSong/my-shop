/**
 * 图片上传模块（七牛云直传）。
 *
 * 流程（与后端 `internal/logic/upload/upload.go` 一致）：
 * 1. `GET /admin/qiniu/upload/token` 由**服务端**签发上传凭证；
 * 2. 浏览器把 `token + key + file` POST 到凭证里的 `upload_url`（七牛主机）；
 * 3. 上传成功后使用凭证里的 `final_url` 作为图片地址。
 *
 * 安全约束：
 * - 前端不接触 AK/SK，不生成签名，不在环境变量里放私密凭证。
 * - 七牛是第三方域名，**禁止**把管理员 Bearer Token 发过去，
 *   因此直传使用独立的 Axios 实例（无任何拦截器）。
 *
 * 已验证程度：代码路径完整，但真实七牛账号未在本次环境中联调，
 * 真实上传标记为 NOT_VERIFIED；MSW 覆盖了前端交互与失败分支。
 */
import Axios from "axios";
import { http } from "@/utils/http";
import { resolveErrorMessage } from "@/utils/error-message";
import type { UploadTokenDTO, UploadedImage } from "../types/upload";

/** 后端默认允许的扩展名（`internal/config/contract.go`） */
export const IMAGE_EXTENSIONS = ["jpg", "jpeg", "png", "gif", "webp"] as const;

/** 后端默认允许的 MIME */
export const IMAGE_MIME_TYPES = [
  "image/jpeg",
  "image/png",
  "image/gif",
  "image/webp"
] as const;

/**
 * 前端预校验大小上限。
 *
 * 后端默认 `qiniu.max_file_size = 10MB`，实际上限由服务端写入 token 的
 * `fsizeLimit` 决定，前端无法读取该值。这里按默认值做友好提示，
 * 最终仍以七牛返回的失败为准。
 */
export const IMAGE_MAX_SIZE_BYTES = 10 * 1024 * 1024;

/** `el-upload` / `accept` 属性用 */
export const IMAGE_ACCEPT = IMAGE_EXTENSIONS.map(ext => `.${ext}`).join(",");

/**
 * 本地文件校验。
 *
 * @returns 错误文案；通过时返回 `null`
 */
export function validateImageFile(
  file: File | null | undefined
): string | null {
  if (!file) return "请选择图片文件";
  if (file.size <= 0) return "图片文件为空，请重新选择";
  if (file.size > IMAGE_MAX_SIZE_BYTES) return "图片不能超过 10MB";

  const extension = file.name.split(".").pop()?.toLowerCase() ?? "";
  if (!(IMAGE_EXTENSIONS as readonly string[]).includes(extension)) {
    return `仅支持 ${IMAGE_EXTENSIONS.join(" / ")} 格式`;
  }

  // 部分浏览器对未知类型给空字符串，此时只依赖扩展名
  if (
    file.type &&
    !(IMAGE_MIME_TYPES as readonly string[]).includes(file.type)
  ) {
    return "图片 MIME 类型不受支持";
  }

  return null;
}

/** 申请上传凭证 */
export function requestUploadToken(filename?: string, contentType?: string) {
  return http.get<UploadTokenDTO, undefined>(
    "/admin/qiniu/upload/token",
    {
      params: {
        ...(filename ? { filename } : {}),
        ...(contentType ? { content_type: contentType } : {})
      }
    },
    { silent: true }
  );
}

/** 直传客户端：独立实例，绝不携带管理员凭证 */
const qiniuClient = Axios.create({ timeout: 60_000 });

/** 从七牛错误响应中提取可读文案 */
function resolveQiniuError(cause: unknown): string {
  const response = (cause as { response?: { data?: unknown; status?: number } })
    ?.response;
  const data = response?.data;
  if (data && typeof data === "object") {
    const record = data as { error?: unknown; message?: unknown };
    if (typeof record.error === "string" && record.error) return record.error;
    if (typeof record.message === "string" && record.message) {
      return record.message;
    }
  }
  if (response?.status) return `上传失败（HTTP ${response.status}）`;
  return "上传失败，请检查网络后重试";
}

/**
 * 上传图片。
 *
 * @param file 已通过 `validateImageFile` 的文件（函数内部会再校验一次）
 * @param onProgress 上传进度（0-100）
 */
export async function uploadImage(
  file: File,
  onProgress?: (percent: number) => void
): Promise<UploadedImage> {
  const invalid = validateImageFile(file);
  if (invalid) throw new Error(invalid);

  let credential: UploadTokenDTO;
  try {
    credential = await requestUploadToken(file.name, file.type);
  } catch (cause) {
    // 凭证接口失败也要展示后端原文（例如 17001 上传参数非法）
    throw new Error(resolveErrorMessage(cause, "获取上传凭证失败"));
  }

  if (!credential.token || !credential.upload_url || !credential.final_url) {
    throw new Error("上传凭证不完整，请稍后重试");
  }

  const form = new FormData();
  form.append("token", credential.token);
  form.append("key", credential.key);
  form.append("file", file);

  try {
    await qiniuClient.post(credential.upload_url, form, {
      // 不手动设置 Content-Type：浏览器需要自动补 multipart boundary
      onUploadProgress: event => {
        if (!onProgress || !event.total) return;
        onProgress(
          Math.min(100, Math.round((event.loaded / event.total) * 100))
        );
      }
    });
  } catch (cause) {
    throw new Error(resolveQiniuError(cause));
  }

  return { url: credential.final_url, key: credential.key };
}
