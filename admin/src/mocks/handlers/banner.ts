import { http } from "msw";
import { mockDb, type MockBanner } from "./mock-db";
import { fail, isAuthorized, ok, toInt, unauthorized } from "./shared";

/**
 * 轮播图 Mock（对齐 `internal/logic/banner/banner.go`）。
 *
 * - 后台列表/详情返回全部状态，按 sort,id 升序；
 * - 创建：title 非空 ≤64、image_url 非空 ≤255、link_url ≤512、status 0/1；
 * - 更新：部分更新，未提交字段保持不变；
 * - 删除：不存在 14001。
 */
const MAX_TITLE = 64;
const MAX_IMAGE = 255;
const MAX_LINK = 512;

function validate(
  title: string,
  imageUrl: string,
  linkUrl: string,
  status?: number
): number | null {
  if (!title.trim() || [...title.trim()].length > MAX_TITLE) return 14002;
  if (!imageUrl.trim() || [...imageUrl.trim()].length > MAX_IMAGE) return 14002;
  if ([...linkUrl.trim()].length > MAX_LINK) return 14002;
  if (status !== undefined && status !== 0 && status !== 1) return 14002;
  return null;
}

function toBanner(banner: MockBanner) {
  return { ...banner };
}

export const bannerHandlers = [
  /** GET /admin/banners */
  http.get("*/api/admin/banners", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const items = [...mockDb.banners]
      .sort((left, right) => left.sort - right.sort || left.id - right.id)
      .map(toBanner);
    return ok({ items });
  }),

  /** GET /admin/banners/:id */
  http.get("*/api/admin/banners/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const banner = mockDb.banners.find(item => item.id === Number(params.id));
    if (!banner) return fail(14001, "轮播图不存在", 404);
    return ok(toBanner(banner));
  }),

  /** POST /admin/banners */
  http.post("*/api/admin/banners", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const body = (await request.json()) as Record<string, unknown>;

    const title = String(body.title ?? "");
    const imageUrl = String(body.image_url ?? "");
    const linkUrl = String(body.link_url ?? "");
    const status = body.status === undefined ? 1 : toInt(body.status);

    if (validate(title, imageUrl, linkUrl, status ?? undefined)) {
      return fail(14002, "轮播图参数非法", 400);
    }

    const banner: MockBanner = {
      id: mockDb.nextBannerId++,
      title: title.trim(),
      image_url: imageUrl.trim(),
      link_url: linkUrl.trim(),
      sort: toInt(body.sort) ?? 0,
      status: (status === 0 ? 0 : 1) as 0 | 1,
      created_at: mockDb.now(),
      updated_at: mockDb.now()
    };

    mockDb.banners.push(banner);
    return ok(toBanner(banner));
  }),

  /** PUT /admin/banners/:id */
  http.put("*/api/admin/banners/:id", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const banner = mockDb.banners.find(item => item.id === Number(params.id));
    if (!banner) return fail(14001, "轮播图不存在", 404);

    const body = (await request.json()) as Record<string, unknown>;
    const title = body.title === undefined ? banner.title : String(body.title);
    const imageUrl =
      body.image_url === undefined ? banner.image_url : String(body.image_url);
    const linkUrl =
      body.link_url === undefined ? banner.link_url : String(body.link_url);
    const status =
      body.status === undefined ? banner.status : (toInt(body.status) ?? -1);

    if (validate(title, imageUrl, linkUrl, status)) {
      return fail(14002, "轮播图参数非法", 400);
    }

    banner.title = title.trim();
    banner.image_url = imageUrl.trim();
    banner.link_url = linkUrl.trim();
    if (body.sort !== undefined) banner.sort = toInt(body.sort) ?? banner.sort;
    banner.status = status as 0 | 1;
    banner.updated_at = mockDb.now();

    return ok(toBanner(banner));
  }),

  /** DELETE /admin/banners/:id */
  http.delete("*/api/admin/banners/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const index = mockDb.banners.findIndex(
      item => item.id === Number(params.id)
    );
    if (index < 0) return fail(14001, "轮播图不存在", 404);

    mockDb.banners.splice(index, 1);
    return ok(null);
  })
];
