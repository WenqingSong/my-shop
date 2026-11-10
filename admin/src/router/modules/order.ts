import { $t } from "@/plugins/i18n";
import { orderAdmin } from "@/router/enums";
import { PERMISSION } from "@/utils/permission";

const Layout = () => import("@/layout/index.vue");

/**
 * 订单路由。
 *
 * 访问控制：后端订单读接口有真实权限码（order:list / order:view），
 * A5 起由 `meta.permissions` 在菜单与路由守卫两层控制；
 * 发货按钮按 order:ship 控制。非超级管理员的有效权限在后端
 * 未下发 `permissions[]` 前按 fail-closed 处理（见 `@/utils/permission`）。
 */
export default {
  path: "/order",
  name: "OrderManagement",
  component: Layout,
  redirect: "/orders",
  meta: {
    icon: "ep/shopping-cart",
    title: $t("menus.pureOrder"),
    rank: orderAdmin
  },
  children: [
    {
      path: "/orders",
      name: "OrderList",
      component: () => import("@/views/order/index.vue"),
      meta: {
        title: $t("menus.pureOrderList"),
        icon: "ep/tickets",
        permissions: [PERMISSION.ORDER_LIST]
      }
    },
    {
      path: "/orders/:id",
      name: "OrderDetail",
      component: () => import("@/views/order/detail.vue"),
      meta: {
        title: $t("menus.pureOrderDetail"),
        showLink: false,
        permissions: [PERMISSION.ORDER_VIEW]
      }
    }
  ]
} satisfies RouteConfigsTable;
