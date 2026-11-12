import { $t } from "@/plugins/i18n";
import { home } from "@/router/enums";
import { PERMISSION } from "@/utils/permission";
const Layout = () => import("@/layout/index.vue");

export default {
  path: "/",
  name: "Home",
  component: Layout,
  redirect: "/dashboard",
  meta: {
    icon: "ep/home-filled",
    title: $t("menus.pureHome"),
    rank: home
  },
  children: [
    {
      path: "/dashboard",
      name: "Dashboard",
      component: () => import("@/views/dashboard/index.vue"),
      meta: {
        title: $t("menus.pureDashboard"),
        icon: "ep/odometer",
        // 后端 5 个工作台接口均要求 dashboard:view
        permissions: [PERMISSION.DASHBOARD_VIEW]
      }
    }
  ]
} satisfies RouteConfigsTable;
