/* 页面绘制前解析主题；登录页、启动层与 Vue 应用共享同一事实源。 */
(() => {
  const key = "surgecart-color-mode";
  const query = window.matchMedia("(prefers-color-scheme: dark)");
  const valid = value => ["light", "dark", "system"].includes(value);
  let mode = "system";
  try {
    const saved = localStorage.getItem(key);
    if (valid(saved)) mode = saved;
  } catch {
    /* 存储被禁用时仍能跟随系统。 */
  }
  const apply = () => {
    const dark = mode === "dark" || (mode === "system" && query.matches);
    // 清除组件库上一次主题留下的内联色阶，使登录页也能正确切换。
    const primaryProperties = [
      "--el-color-primary",
      ...Array.from(
        { length: 9 },
        (_, i) => `--el-color-primary-light-${i + 1}`
      ),
      "--el-color-primary-dark-1",
      "--el-color-primary-dark-2"
    ];
    for (const property of primaryProperties)
      document.documentElement.style.removeProperty?.(property);
    document.documentElement.classList.toggle("dark", dark);
    document.documentElement.dataset.colorMode = mode;
    document.documentElement.style.colorScheme = dark ? "dark" : "light";
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute("content", dark ? "#17191b" : "#f2f2ef");
    window.dispatchEvent(
      new CustomEvent("framework-theme-change", { detail: { mode, dark } })
    );
  };
  window.FrameworkTheme = {
    get mode() {
      return mode;
    },
    get dark() {
      return mode === "dark" || (mode === "system" && query.matches);
    },
    set(next) {
      if (!valid(next)) return;
      mode = next;
      try {
        localStorage.setItem(key, mode);
      } catch {
        /* 内存中继续生效。 */
      }
      apply();
    }
  };
  query.addEventListener("change", () => {
    if (mode === "system") apply();
  });
  window.addEventListener("storage", event => {
    if (event.key !== key && event.key !== null) return;
    mode = valid(event.newValue) ? event.newValue : "system";
    apply();
  });
  apply();
})();
