import { HttpResponse, http } from "msw";
import { fail, isAuthorized, ok, unauthorized } from "./shared";

/**
 * 图片上传 Mock（七牛直传）。
 *
 * 模拟后端"只签发凭证 + 浏览器直传第三方主机"的两段式流程：
 * - `GET /admin/qiniu/upload/token` 返回假凭证（**不是真实七牛 token**）；
 * - `POST https://mock-upload.local/upload` 模拟七牛表单上传；
 * - `GET https://mock-upload.local/files/*` 返回 SVG 占位图，保证演示时图片能显示。
 *
 * 真实七牛账号未联调，真实上传状态为 NOT_VERIFIED。
 */
const ALLOWED_EXTENSIONS = ["jpg", "jpeg", "png", "gif", "webp"];
const ALLOWED_MIME = ["image/jpeg", "image/png", "image/gif", "image/webp"];

/**
 * 模拟上传地址。
 *
 * 刻意使用**同源**路径：真实七牛是第三方域名，浏览器需要 CORS；
 * 在 jsdom 测试环境里跨域 XHR 会被直接判定为网络错误，
 * 无法验证"取凭证 → 直传 → 使用 final_url"的完整链路。
 * 同源 mock 只改变传输地址，不改变业务代码的调用方式。
 */
export const MOCK_UPLOAD_PATH = "/mock-upload";
export const MOCK_FILE_DOMAIN = "https://mock-upload.local/files";

let uploadSeed = 0;

const PLACEHOLDER_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="240">
  <rect width="240" height="240" fill="#eef2f7"/>
  <rect x="20" y="20" width="200" height="200" fill="#dbe4ef" rx="12"/>
  <text x="120" y="126" font-family="sans-serif" font-size="16" fill="#7a8ba3" text-anchor="middle">MSW 占位图</text>
</svg>`;

export const uploadHandlers = [
  /** GET /admin/qiniu/upload/token */
  http.get("*/api/admin/qiniu/upload/token", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();

    const url = new URL(request.url);
    const filename = url.searchParams.get("filename") ?? "";
    const contentType = url.searchParams.get("content_type") ?? "";

    const extension = filename.includes(".")
      ? (filename.split(".").pop()?.toLowerCase() ?? "")
      : "";

    if (extension && !ALLOWED_EXTENSIONS.includes(extension)) {
      return fail(17001, "上传参数非法", 400);
    }
    if (contentType && !ALLOWED_MIME.includes(contentType)) {
      return fail(17001, "上传参数非法", 400);
    }

    uploadSeed += 1;
    const key = `upload/20261110/mock-${uploadSeed}.${extension || "png"}`;

    return ok({
      token: "mock-qiniu-upload-token",
      key,
      upload_url: `${url.origin}${MOCK_UPLOAD_PATH}`,
      domain: MOCK_FILE_DOMAIN,
      final_url: `${MOCK_FILE_DOMAIN}/${key}`,
      expires_at: 1760000000
    });
  }),

  /** POST {origin}/mock-upload（模拟七牛表单上传） */
  http.post(`*${MOCK_UPLOAD_PATH}`, async ({ request }) => {
    const form = await request.formData();
    const token = form.get("token");
    const key = form.get("key");
    const file = form.get("file");

    if (!token || !key || !file) {
      return HttpResponse.json(
        { error: "invalid upload form" },
        { status: 400 }
      );
    }

    return HttpResponse.json({ key: String(key), hash: "mock-hash" });
  }),

  /** 占位图片（演示环境真实可加载） */
  http.get(`${MOCK_FILE_DOMAIN}/*`, () => {
    return new HttpResponse(PLACEHOLDER_SVG, {
      headers: { "Content-Type": "image/svg+xml" }
    });
  })
];
