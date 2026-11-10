import { $t } from "@/plugins/i18n";
import { marketingAdmin } from "@/router/enums";

const Layout = () => import("@/layout/index.vue");

/**
 * 营销路由。
 *
 * 权限：优惠券有 `coupon:*`，秒杀有 `flash_sale:create/update`。
 * 读接口（优惠券列表）需要 coupon:list；秒杀没有读接口，
 * 菜单不按角色硬编码屏蔽，无权限时由后端 403 兜底。
 */
export default {
  path: "/marketing",
  name: "MarketingManagement",
  component: Layout,
  redirect: "/marketing/coupons",
  meta: {
    icon: "ep/present",
    title: $t("menus.pureMarketing"),
    rank: marketingAdmin
  },
  children: [
    {
      path: "/marketing/coupons",
      name: "CouponList",
      component: () => import("@/views/marketing/coupons/index.vue"),
      meta: {
        title: $t("menus.pureCoupon"),
        icon: "ep/ticket"
      }
    },
    {
      path: "/marketing/flash-sales",
      name: "FlashSaleManagement",
      component: () => import("@/views/marketing/flash-sales/index.vue"),
      meta: {
        title: $t("menus.pureFlashSale"),
        icon: "ep/alarm-clock"
      }
    }
  ]
} satisfies RouteConfigsTable;
