import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  createBanner,
  deleteBanner,
  getBanner,
  listBanners,
  updateBanner
} from "@/api/modules/banner";
import {
  buildCreateBannerPayload,
  emptyBannerForm,
  hasBannerErrors,
  toBannerView,
  validateBannerForm
} from "@/api/adapters/banner-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

function validForm() {
  return {
    ...emptyBannerForm(),
    title: "新轮播图",
    imageUrl: "https://mock-upload.local/files/new.png",
    linkUrl: "/products/1001",
    sort: 5,
    status: 1 as const
  };
}

describe("轮播图模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  it("列表返回全部状态并按 sort,id 升序", async () => {
    const result = await listBanners();

    expect(result.items.map(item => item.id)).toEqual([8201, 8202, 8203]);
    expect(result.items[2].status).toBe(0);
  });

  it("详情包含图片、跳转、排序与状态", async () => {
    const detail = await getBanner(8201);
    const view = toBannerView(detail);

    expect(view.title).toBe("双十一主会场");
    expect(view.imageUrl).toContain("/banner-1.png");
    expect(view.linkUrl).toBe("/products/1001");
    expect(view.statusLabel).toBe("启用");
  });

  it("详情不存在返回 14001", async () => {
    await expectBusinessCode(getBanner(999999), 14001);
  });

  it("创建轮播图：图片 URL 由上传组件提供，状态可为禁用", async () => {
    const created = await createBanner({
      ...buildCreateBannerPayload({ ...validForm(), status: 0 })
    });

    expect(created.id).toBeGreaterThan(8203);
    expect(created.status).toBe(0);
    expect(created.sort).toBe(5);

    const list = await listBanners();
    expect(list.items).toHaveLength(4);
  });

  it("后端拒绝空标题/空图片（400/14002）", async () => {
    await expectBusinessCode(
      createBanner({ ...buildCreateBannerPayload(validForm()), title: "  " }),
      14002
    );
    await expectBusinessCode(
      createBanner({ ...buildCreateBannerPayload(validForm()), image_url: "" }),
      14002
    );
  });

  it("更新为部分更新：只改状态不影响标题与排序", async () => {
    const updated = await updateBanner(8201, { status: 0 });

    expect(updated.status).toBe(0);
    expect(updated.title).toBe("双十一主会场");
    expect(updated.sort).toBe(1);
  });

  it("删除轮播图后列表减少，重复删除返回 14001", async () => {
    await expect(deleteBanner(8202)).resolves.toBeNull();

    const list = await listBanners();
    expect(list.items.map(item => item.id)).toEqual([8201, 8203]);

    await expectBusinessCode(deleteBanner(8202), 14001);
  });

  it("表单校验：标题/图片必填、长度上限、排序整数", () => {
    const errors = validateBannerForm(emptyBannerForm());
    expect(errors.title).toBe("请输入轮播图标题");
    expect(errors.imageUrl).toBe("请上传轮播图片");
    expect(hasBannerErrors(errors)).toBe(true);

    const longLink = validateBannerForm({
      ...validForm(),
      linkUrl: "a".repeat(513)
    });
    expect(longLink.linkUrl).toContain("512");

    const badSort = validateBannerForm({
      ...validForm(),
      sort: 1.5
    });
    expect(badSort.sort).toBeDefined();
  });

  it("标题超过 64 字符被拒绝", () => {
    expect(
      validateBannerForm({ ...validForm(), title: "a".repeat(65) }).title
    ).toContain("64");
  });

  it("无权访问时展示权限提示", async () => {
    server.use(
      mswHttp.get("*/api/admin/banners", () =>
        HttpResponse.json(
          { code: 1003, message: "没有访问权限", data: null },
          { status: 403 }
        )
      )
    );

    try {
      await listBanners();
    } catch (cause) {
      expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
    }
  });

  it("使用后端真实权限码", () => {
    expect(PERMISSION.BANNER_CREATE).toBe("banner:create");
    expect(PERMISSION.BANNER_UPDATE).toBe("banner:update");
    expect(PERMISSION.BANNER_DELETE).toBe("banner:delete");
  });
});
