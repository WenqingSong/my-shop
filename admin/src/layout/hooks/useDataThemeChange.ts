import { ref, watch } from "vue";
import { useColorMode } from "@/composables/useColorMode";
import { getConfig } from "@/config";
import { useLayout } from "./useLayout";
import { removeToken } from "@/utils/auth";
import { routerArrays } from "@/layout/types";
import { router, resetRouter } from "@/router";
import type { themeColorsType } from "../types";
import { useAppStoreHook } from "@/store/modules/app";
import { useEpThemeStoreHook } from "@/store/modules/epTheme";
import { useMultiTagsStoreHook } from "@/store/modules/multiTags";
import { useUserStoreHook } from "@/store/modules/user";
import { darken, lighten, useGlobal, storageLocal } from "@pureadmin/utils";

export function useDataThemeChange() {
  const { layoutTheme, layout } = useLayout();
  const themeColors = ref<Array<themeColorsType>>([
    /* 亮白色 */
    { color: "#ffffff", themeColor: "light" },
    /* 道奇蓝 */
    { color: "#1b2a47", themeColor: "default" },
    /* 深紫罗兰色 */
    { color: "#722ed1", themeColor: "saucePurple" },
    /* 深粉色 */
    { color: "#eb2f96", themeColor: "pink" },
    /* 猩红色 */
    { color: "#f5222d", themeColor: "dusk" },
    /* 橙红色 */
    { color: "#fa541c", themeColor: "volcano" },
    /* 绿宝石 */
    { color: "#13c2c2", themeColor: "mingQing" },
    /* 酸橙绿 */
    { color: "#52c41a", themeColor: "auroraGreen" }
  ]);

  const { $storage } = useGlobal<GlobalPropertiesApi>();
  const { dark: dataTheme, mode: themeMode, setMode } = useColorMode();
  const body = document.documentElement as HTMLElement;

  function toggleClass(flag: boolean, clsName: string, target?: HTMLElement) {
    const targetEl = target || document.body;
    let { className } = targetEl;
    className = className.replace(clsName, "").trim();
    targetEl.className = flag ? `${className} ${clsName}` : className;
  }

  /** 设置导航主题色 */
  function setLayoutThemeColor(
    theme = getConfig().Theme ?? "light",
    isClick = true
  ) {
    layoutTheme.value.theme = theme;
    document.documentElement.setAttribute("data-theme", theme);
    // 如果非isClick，保留之前的themeColor
    const storageThemeColor = $storage.layout.themeColor;
    $storage.layout = {
      layout: layout.value,
      theme,
      darkMode: dataTheme.value,
      sidebarStatus: $storage.layout?.sidebarStatus,
      epThemeColor: $storage.layout?.epThemeColor,
      themeColor: isClick ? theme : storageThemeColor,
      themeMode: themeMode.value
    };

    if (theme === "default" || theme === "light") {
      setEpThemeColor(getConfig().EpThemeColor ?? "#409EFF");
    } else {
      const colors = themeColors.value.find(v => v.themeColor === theme);
      setEpThemeColor(colors?.color ?? "#409EFF");
    }
  }

  function setPropertyPrimary(mode: string, i: number, color: string) {
    document.documentElement.style.setProperty(
      `--el-color-primary-${mode}-${i}`,
      dataTheme.value ? darken(color, i / 10) : lighten(color, i / 10)
    );
  }

  /** 设置 `element-plus` 主题色 */
  const setEpThemeColor = (color: string) => {
    useEpThemeStoreHook().setEpThemeColor(color);
    const actual =
      dataTheme.value && color.toLowerCase() === "#17191d" ? "#e4e5df" : color;
    document.documentElement.style.setProperty("--el-color-primary", actual);
    for (let i = 1; i <= 2; i++) {
      setPropertyPrimary("dark", i, actual);
    }
    for (let i = 1; i <= 9; i++) {
      setPropertyPrimary("light", i, actual);
    }
  };

  /** 浅色、深色主题模式切换 */
  function dataThemeChange(mode: ColorMode = themeMode.value) {
    setMode(mode);
    setLayoutThemeColor($storage.layout?.theme ?? "light", false);
  }
  watch([dataTheme, themeMode], () => {
    setLayoutThemeColor($storage.layout?.theme ?? "light", false);
  });

  /**
   * 清空缓存并返回登录页。
   *
   * A1 起除清 token 与本地缓存外，还需要重置 Pinia 中的管理员身份与权限快照，
   * 否则刷新后守卫会误判 `profileLoaded` 而跳过 `/admin/me` 校验。
   */
  function onReset() {
    removeToken();
    useUserStoreHook().RESET_AUTH_STATE();
    storageLocal().clear();
    const { Grey, Weak, MultiTagsCache, EpThemeColor, Layout } = getConfig();
    useAppStoreHook().setLayout(Layout ?? "vertical");
    setEpThemeColor(EpThemeColor ?? "#409EFF");
    useMultiTagsStoreHook().multiTagsCacheChange(!!MultiTagsCache);
    toggleClass(
      !!Grey,
      "html-grey",
      document.querySelector("html") ?? undefined
    );
    toggleClass(
      !!Weak,
      "html-weakness",
      document.querySelector("html") ?? undefined
    );
    router.push("/login");
    useMultiTagsStoreHook().handleTags("equal", [...routerArrays]);
    resetRouter();
  }

  return {
    body,
    dataTheme,
    themeMode,
    layoutTheme,
    themeColors,
    onReset,
    toggleClass,
    dataThemeChange,
    setEpThemeColor,
    setLayoutThemeColor
  };
}
