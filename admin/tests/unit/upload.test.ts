import { beforeEach, describe, expect, it, vi } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  IMAGE_MAX_SIZE_BYTES,
  uploadImage,
  validateImageFile
} from "@/api/modules/upload";
import { useImageUploader } from "@/views/product/composables/useImageUploader";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

function imageFile(
  name = "photo.png",
  type = "image/png",
  size?: number
): File {
  const file = new File(["x"], name, { type });
  if (size !== undefined) {
    Object.defineProperty(file, "size", { value: size });
  }
  return file;
}

describe("图片上传", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  describe("本地文件校验", () => {
    it("允许后端白名单内的格式", () => {
      ["a.jpg", "b.jpeg", "c.png", "d.gif", "e.webp"].forEach(name => {
        expect(validateImageFile(imageFile(name))).toBeNull();
      });
    });

    it("拒绝不在白名单的扩展名", () => {
      expect(validateImageFile(imageFile("shell.svg"))).toContain("仅支持");
      expect(
        validateImageFile(imageFile("doc.pdf", "application/pdf"))
      ).toContain("仅支持");
    });

    it("拒绝不匹配的 MIME", () => {
      expect(validateImageFile(imageFile("fake.png", "text/plain"))).toContain(
        "MIME"
      );
    });

    it("拒绝超过 10MB 的文件", () => {
      expect(
        validateImageFile(
          imageFile("big.png", "image/png", IMAGE_MAX_SIZE_BYTES + 1)
        )
      ).toContain("10MB");
    });

    it("拒绝空文件与未选择文件", () => {
      expect(
        validateImageFile(imageFile("empty.png", "image/png", 0))
      ).toContain("为空");
      expect(validateImageFile(null)).toBe("请选择图片文件");
    });
  });

  describe("七牛直传流程", () => {
    it("先取凭证，再上传到凭证里的 upload_url，返回 final_url", async () => {
      const result = await uploadImage(imageFile("product.png"));

      expect(result.url).toMatch(
        /^https:\/\/mock-upload\.local\/files\/upload\/\d+\/mock-\d+\.png$/
      );
      expect(result.key).toMatch(/^upload\/\d+\/mock-\d+\.png$/);
    });

    it("本地校验失败时不会发起网络请求", async () => {
      const spy = vi.fn();
      server.use(
        mswHttp.get("*/api/admin/qiniu/upload/token", () => {
          spy();
          return HttpResponse.json({ code: 0, message: "OK", data: null });
        })
      );

      await expect(
        uploadImage(imageFile("bad.txt", "text/plain"))
      ).rejects.toThrow("仅支持");
      expect(spy).not.toHaveBeenCalled();
    });

    it("凭证接口失败时抛出后端文案", async () => {
      server.use(
        mswHttp.get("*/api/admin/qiniu/upload/token", () =>
          HttpResponse.json(
            { code: 17001, message: "上传参数非法", data: null },
            { status: 400 }
          )
        )
      );

      await expect(uploadImage(imageFile())).rejects.toThrow("上传参数非法");
    });

    it("七牛返回失败时不谎报成功", async () => {
      server.use(
        mswHttp.post("*/mock-upload", () =>
          HttpResponse.json({ error: "invalid token" }, { status: 401 })
        )
      );

      await expect(uploadImage(imageFile())).rejects.toThrow("invalid token");
    });
  });

  describe("图片组件状态", () => {
    it("上传成功后进入已完成列表并回写 URL", async () => {
      const changes: string[][] = [];
      const uploader = vi.fn(async () => ({
        url: "https://mock-upload.local/files/a.png",
        key: "a.png"
      }));

      const state = useImageUploader({
        max: 3,
        uploader,
        onChange: urls => changes.push(urls)
      });

      await state.addFiles([imageFile("a.png")]);

      expect(uploader).toHaveBeenCalledTimes(1);
      expect(state.items.value[0].status).toBe("done");
      expect(state.urls.value).toEqual([
        "https://mock-upload.local/files/a.png"
      ]);
      expect(changes.at(-1)).toEqual(["https://mock-upload.local/files/a.png"]);
    });

    it("上传失败时保留失败项并回调错误，不写入 URL", async () => {
      const errors: string[] = [];
      const uploader = vi.fn(async () => {
        throw new Error("upload boom");
      });

      const state = useImageUploader({
        max: 3,
        uploader,
        onChange: () => {},
        onError: text => errors.push(text)
      });

      await state.addFiles([imageFile("a.png")]);

      expect(state.items.value[0].status).toBe("error");
      expect(state.items.value[0].error).toBe("upload boom");
      expect(state.urls.value).toEqual([]);
      expect(errors).toContain("upload boom");
    });

    it("预览列表只包含上传成功的 URL", async () => {
      const state = useImageUploader({
        max: 3,
        initial: ["https://cdn/seed.png"],
        uploader: vi.fn(async () => {
          throw new Error("boom");
        }),
        onChange: () => {}
      });

      await state.addFiles([imageFile("failed.png")]);

      // el-image 的 preview-src-list 直接绑定 urls：
      // 失败项不会进入预览列表，也不会被当作已选图片提交
      expect(state.urls.value).toEqual(["https://cdn/seed.png"]);
      expect(state.items.value).toHaveLength(2);
    });

    it("删除已选图片会同步外部 URL 列表", async () => {
      const changes: string[][] = [];
      const state = useImageUploader({
        max: 3,
        initial: ["https://cdn/a.png", "https://cdn/b.png"],
        uploader: vi.fn(async () => ({ url: "", key: "" })),
        onChange: urls => changes.push([...urls])
      });

      expect(state.doneCount.value).toBe(2);

      const firstUid = state.items.value[0].uid;
      state.remove(firstUid);

      expect(state.urls.value).toEqual(["https://cdn/b.png"]);
      expect(changes.at(-1)).toEqual(["https://cdn/b.png"]);
    });

    it("超过上限的文件被拒绝并提示", async () => {
      const errors: string[] = [];
      const uploader = vi.fn(async () => ({
        url: "https://cdn/1.png",
        key: "1"
      }));

      const state = useImageUploader({
        max: 1,
        uploader,
        onChange: () => {},
        onError: text => errors.push(text)
      });

      await state.addFiles([imageFile("a.png"), imageFile("b.png")]);

      expect(uploader).toHaveBeenCalledTimes(1);
      expect(errors.some(text => text.includes("最多上传 1 张"))).toBe(true);
    });

    it("上传中的图片不允许删除", async () => {
      const errors: string[] = [];
      let release: (value: { url: string; key: string }) => void = () => {};
      const uploader = vi.fn(
        () =>
          new Promise<{ url: string; key: string }>(resolve => {
            release = resolve;
          })
      );

      const state = useImageUploader({
        max: 2,
        uploader,
        onChange: () => {},
        onError: text => errors.push(text)
      });

      const pending = state.addFiles([imageFile("a.png")]);
      await Promise.resolve();
      const uid = state.items.value[0].uid;

      state.remove(uid);
      expect(state.items.value).toHaveLength(1);
      expect(errors.some(text => text.includes("上传中"))).toBe(true);

      release({ url: "https://cdn/a.png", key: "a" });
      await pending;
      expect(state.items.value[0].status).toBe("done");
    });

    it("外部整体替换（切换商品）会刷新列表", () => {
      const state = useImageUploader({
        max: 3,
        initial: ["https://cdn/a.png"],
        uploader: vi.fn(async () => ({ url: "", key: "" })),
        onChange: () => {}
      });

      state.replace(["https://cdn/c.png"]);

      expect(state.urls.value).toEqual(["https://cdn/c.png"]);
    });
  });
});
