import source from "../../public/brand/theme.js?raw";
import { runInNewContext } from "node:vm";
import { describe, expect, it } from "vitest";

function createTheme(systemDark: boolean, saved: string | null = null) {
  let listener = () => {};
  const classes = new Set<string>();
  const query = {
    matches: systemDark,
    addEventListener: (_: string, fn: () => void) => {
      listener = fn;
    }
  };
  const localStorage = {
    getItem: () => saved,
    setItem: (_: string, value: string) => {
      saved = value;
    }
  };
  const root = {
    classList: {
      toggle: (name: string, enabled: boolean) => {
        if (enabled) classes.add(name);
        else classes.delete(name);
      }
    },
    dataset: {} as Record<string, string>,
    style: { colorScheme: "" }
  };
  const window = {
    matchMedia: () => query,
    dispatchEvent: () => {},
    addEventListener: () => {},
    FrameworkTheme: undefined as Window["FrameworkTheme"]
  };
  runInNewContext(source, {
    window,
    document: { documentElement: root, querySelector: () => null },
    localStorage,
    CustomEvent
  });
  return {
    api: window.FrameworkTheme!,
    root,
    classes,
    changeSystem: (dark: boolean) => {
      query.matches = dark;
      listener();
    },
    saved: () => saved
  };
}

describe("品牌主题在首次绘制前解析", () => {
  it.each([true, false])("未选择主题时跟随系统：深色=%s", dark => {
    const theme = createTheme(dark);
    expect(theme.api.mode).toBe("system");
    expect(theme.classes.has("dark")).toBe(dark);
    expect(theme.root.style.colorScheme).toBe(dark ? "dark" : "light");
  });
  it("系统主题变化即时生效", () => {
    const theme = createTheme(false);
    theme.changeSystem(true);
    expect(theme.classes.has("dark")).toBe(true);
    theme.changeSystem(false);
    expect(theme.classes.has("dark")).toBe(false);
  });
  it("手动浅色优先于系统深色，并保存选择", () => {
    const theme = createTheme(true);
    theme.api.set("light");
    theme.changeSystem(false);
    theme.changeSystem(true);
    expect(theme.classes.has("dark")).toBe(false);
    expect(theme.saved()).toBe("light");
  });
  it("刷新后恢复手动深色；选择跟随系统后重新跟随", () => {
    const theme = createTheme(false, "dark");
    expect(theme.classes.has("dark")).toBe(true);
    theme.api.set("system");
    expect(theme.classes.has("dark")).toBe(false);
    theme.changeSystem(true);
    expect(theme.classes.has("dark")).toBe(true);
  });
  it("无效旧配置回退到系统", () => {
    const theme = createTheme(true, "invalid");
    expect(theme.api.mode).toBe("system");
    expect(theme.classes.has("dark")).toBe(true);
  });
});
