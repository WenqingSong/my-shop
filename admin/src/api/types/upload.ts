/**
 * 图片上传 DTO（七牛云直传）。
 *
 * 依据：`api/upload/v1/upload.go` + `internal/logic/upload/upload.go`。
 *
 * 后端事实：
 * - `GET /admin/qiniu/upload/token?filename=&content_type=` 只签发**凭证**，
 *   不接收文件本身；浏览器用返回的 token 直传七牛。
 * - 后端不返回 AK/SK，前端也**不得**持有；签名在服务端完成。
 * - 默认扩展名白名单：jpg/jpeg/png/gif/webp；默认上限 10MB（可被服务端配置调整）；
 *   token 默认 1 小时过期。
 * - `final_url` = `domain` + `/` + `key`，上传成功后可直接作为图片地址。
 */

/** 上传凭证响应（TokenRes） */
export interface UploadTokenDTO {
  /** 七牛上传凭证 */
  token: string;
  /** 后端预生成的存储 key */
  key: string;
  /** 上传主机（按 region 映射） */
  upload_url: string;
  /** 对外访问域名 */
  domain: string;
  /** 上传成功后的可访问地址 */
  final_url: string;
  /** 凭证过期时间（unix 秒） */
  expires_at: number;
}

/** 上传凭证请求参数（均可选） */
export interface UploadTokenQuery {
  filename?: string;
  content_type?: string;
}

/** 上传成功结果 */
export interface UploadedImage {
  /** 最终可访问 URL（后端 final_url） */
  url: string;
  /** 存储 key */
  key: string;
}
