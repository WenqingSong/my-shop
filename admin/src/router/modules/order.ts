import { $t } from "@/plugins/i18n";
import { orderAdmin } from "@/router/enums";

const Layout = () => import("@/layout/index.vue");

/**
 * 订单路由。
 *
 * 访问控制：后端订单读接口有真实权限码（order:list / order:view），
 * 但前端无法从 `/admin/me` 拿到 permissions[]，因此菜单不按角色硬编码屏蔽；
 * 无权限时由后端返回 403，页面展示可读错误。发货按钮按 order:ship 控制。
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
        icon: "ep/tickets"
      }
    },
    {
      path: "/orders/:id",
      name: "OrderDetail",
      component: () => import("@/views/order/detail.vue"),
      meta: {
        title: $t("menus.pureOrderDetail"),
        showLink: false
      }
    }
  ]
} satisfies RouteConfigsTable;
