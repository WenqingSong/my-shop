import { $t } from "@/plugins/i18n";
import { productAdmin } from "@/router/enums";

const Layout = () => import("@/layout/index.vue");

/**
 * 商品与分类路由。
 *
 * 访问控制：后端商品/分类的**读接口没有权限码**（仅 AdminAuth，
 * 分类树更是公开接口），所以这里不使用 `meta.roles` 编造"读权限"；
 * 写操作按钮在页面内按真实权限码（product:* / category:* / sku:*）控制。
 */
export default {
  path: "/product",
  name: "ProductManagement",
  component: Layout,
  redirect: "/products",
  meta: {
    icon: "ep/goods",
    title: $t("menus.pureProduct"),
    rank: productAdmin
  },
  children: [
    {
      path: "/products",
      name: "ProductList",
      component: () => import("@/views/product/index.vue"),
      meta: {
        title: $t("menus.pureProductList"),
        keepAlive: true,
        icon: "ep/list"
      }
    },
    {
      path: "/products/create",
      name: "ProductCreate",
      component: () => import("@/views/product/form.vue"),
      meta: {
        title: $t("menus.pureProductCreate"),
        showLink: false
      }
    },
    {
      path: "/products/:id/edit",
      name: "ProductEdit",
      component: () => import("@/views/product/form.vue"),
      meta: {
        title: $t("menus.pureProductEdit"),
        showLink: false
      }
    },
    {
      path: "/categories",
      name: "CategoryList",
      component: () => import("@/views/category/index.vue"),
      meta: {
        title: $t("menus.pureCategory"),
        icon: "ep/files"
      }
    }
  ]
} satisfies RouteConfigsTable;
