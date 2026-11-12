import { ref } from "vue";

const mode = ref<ColorMode>(window.FrameworkTheme?.mode ?? "system");
const dark = ref(
  window.FrameworkTheme?.dark ??
    window.matchMedia?.("(prefers-color-scheme: dark)").matches ??
    false
);
const sync = () => {
  mode.value = window.FrameworkTheme?.mode ?? "system";
  dark.value = window.FrameworkTheme?.dark ?? false;
};
window.addEventListener("framework-theme-change", sync);
if (import.meta.hot)
  import.meta.hot.dispose(() =>
    window.removeEventListener("framework-theme-change", sync)
  );

export function useColorMode() {
  return {
    mode,
    dark,
    setMode: (next: ColorMode) => window.FrameworkTheme?.set(next)
  };
}
