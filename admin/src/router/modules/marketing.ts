import { $t } from "@/plugins/i18n";
import { marketingAdmin } from "@/router/enums";
import { PERMISSION } from "@/utils/permission";

const Layout = () => import("@/layout/index.vue");

/**
 * 营销路由。
 *
 * 权限：优惠券列表读接口要求 coupon:list，页面级权限与菜单一致；
 * 秒杀后台没有读接口（只有 create/update/repair），因此页面不声明读权限码，
 * 进入后由按钮权限与后端 403 兜底。
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
        icon: "ep/ticket",
        permissions: [PERMISSION.COUPON_LIST]
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
