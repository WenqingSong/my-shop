import { $t } from "@/plugins/i18n";
import { aftersalesAdmin } from "@/router/enums";

const Layout = () => import("@/layout/index.vue");

/**
 * 售后（退款）路由。
 *
 * 后端权限：refund:list / refund:view / refund:review / refund:execute。
 * 审核与执行是两个阶段：审核通过只是进入「待退款」，「执行退款成功」
 * 才代表资金流程完成（后端 Mock 人工确认）。
 */
export default {
  path: "/aftersales",
  name: "AfterSalesManagement",
  component: Layout,
  redirect: "/refunds",
  meta: {
    icon: "ep/service",
    title: $t("menus.pureAfterSales"),
    rank: aftersalesAdmin
  },
  children: [
    {
      path: "/refunds",
      name: "RefundList",
      component: () => import("@/views/refund/index.vue"),
      meta: {
        title: $t("menus.pureRefundList"),
        icon: "ep/money"
      }
    },
    {
      path: "/refunds/:id",
      name: "RefundDetail",
      component: () => import("@/views/refund/detail.vue"),
      meta: {
        title: $t("menus.pureRefundDetail"),
        showLink: false
      }
    }
  ]
} satisfies RouteConfigsTable;
