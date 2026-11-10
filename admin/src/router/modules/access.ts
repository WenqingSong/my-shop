import { $t } from "@/plugins/i18n";
import { systemAdmin, userAdmin } from "@/router/enums";
import { PERMISSION } from "@/utils/permission";

const Layout = () => import("@/layout/index.vue");

/**
 * A5：用户与权限域路由。
 *
 * 权限声明只使用 `internal/cmd/routes_admin.go` 中真实存在的权限码：
 * - 用户：`user:read`（查询状态）/ `user:status`（禁用启用）；
 * - 管理员：`admin:create` / `admin:disable` / `admin:delete` / `admin:assign_role`，
 *   后端**没有** `admin:list`，因此页面采用 `some` 模式：持有任一管理权限即可进入；
 * - 角色：`role:list`；权限：`permission:list`。
 *
 * 商品、分类、库存、轮播图、推荐位的读接口在 A2/A4 已确认"仅 AdminAuth"，
 * 本阶段不为其补造读权限码。
 */
export default [
  {
    path: "/user",
    name: "UserManagement",
    component: Layout,
    redirect: "/users",
    meta: {
      icon: "ep/user",
      title: $t("menus.pureUser"),
      rank: userAdmin
    },
    children: [
      {
        path: "/users",
        name: "UserStatus",
        component: () => import("@/views/user/index.vue"),
        meta: {
          title: $t("menus.pureUserList"),
          icon: "ep/user-filled",
          permissions: [PERMISSION.USER_READ]
        }
      }
    ]
  },
  {
    path: "/system",
    name: "SystemManagement",
    component: Layout,
    redirect: "/system/admins",
    meta: {
      icon: "ep/setting",
      title: $t("menus.pureSysManagement"),
      rank: systemAdmin
    },
    children: [
      {
        path: "/system/admins",
        name: "AdminManagement",
        component: () => import("@/views/system/admins/index.vue"),
        meta: {
          title: $t("menus.pureAdmin"),
          icon: "ep/avatar",
          permissions: [
            PERMISSION.ADMIN_CREATE,
            PERMISSION.ADMIN_DISABLE,
            PERMISSION.ADMIN_DELETE,
            PERMISSION.ADMIN_ASSIGN_ROLE
          ],
          permissionsMode: "some"
        }
      },
      {
        path: "/system/roles",
        name: "RoleManagement",
        component: () => import("@/views/system/roles/index.vue"),
        meta: {
          title: $t("menus.pureRole"),
          icon: "ep/key",
          permissions: [PERMISSION.ROLE_LIST]
        }
      },
      {
        path: "/system/permissions",
        name: "PermissionManagement",
        component: () => import("@/views/system/permissions/index.vue"),
        meta: {
          title: $t("menus.purePermissionAdmin"),
          icon: "ep/lock",
          permissions: [PERMISSION.PERMISSION_LIST]
        }
      }
    ]
  }
] satisfies Array<RouteConfigsTable>;
