import { $t } from "@/plugins/i18n";
import { contentAdmin } from "@/router/enums";

const Layout = () => import("@/layout/index.vue");

/**
 * 内容路由。
 *
 * 轮播图/推荐位读接口仅要求 AdminAuth；文章只有公开只读接口，
 * 没有管理端写接口。菜单不按角色硬编码屏蔽。
 */
export default {
  path: "/content",
  name: "ContentManagement",
  component: Layout,
  redirect: "/content/banners",
  meta: {
    icon: "ep/document",
    title: $t("menus.pureContent"),
    rank: contentAdmin
  },
  children: [
    {
      path: "/content/banners",
      name: "BannerList",
      component: () => import("@/views/content/banners/index.vue"),
      meta: {
        title: $t("menus.pureBanner"),
        icon: "ep/picture"
      }
    },
    {
      path: "/content/recommendations",
      name: "RecommendationManagement",
      component: () => import("@/views/content/recommendations/index.vue"),
      meta: {
        title: $t("menus.pureRecommendation"),
        icon: "ep/star"
      }
    },
    {
      path: "/content/articles",
      name: "ArticleList",
      component: () => import("@/views/content/articles/index.vue"),
      meta: {
        title: $t("menus.pureArticle"),
        icon: "ep/document"
      }
    }
  ]
} satisfies RouteConfigsTable;
