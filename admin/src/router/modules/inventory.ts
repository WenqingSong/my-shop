import { $t } from "@/plugins/i18n";
import { inventoryAdmin } from "@/router/enums";

const Layout = () => import("@/layout/index.vue");

/**
 * 库存路由。
 *
 * 后端库存查询只要求 AdminAuth（没有 inventory:read 权限码），
 * 调整按钮分别对应 inventory:increase / inventory:deduct。
 */
export default {
  path: "/stock",
  name: "InventoryManagement",
  component: Layout,
  redirect: "/inventory",
  meta: {
    icon: "ep/box",
    title: $t("menus.pureInventory"),
    rank: inventoryAdmin
  },
  children: [
    {
      path: "/inventory",
      name: "InventoryList",
      component: () => import("@/views/inventory/index.vue"),
      meta: {
        title: $t("menus.pureInventoryList"),
        icon: "ep/coin"
      }
    },
    {
      path: "/inventory/transactions",
      name: "InventoryTransactions",
      component: () => import("@/views/inventory/transactions.vue"),
      meta: {
        title: $t("menus.pureInventoryTransactions"),
        icon: "ep/tickets"
      }
    }
  ]
} satisfies RouteConfigsTable;
