import type { Directive } from "vue";
import symbol from "@/assets/brand/symbol.svg?raw";

interface LoadingState {
  mask?: HTMLElement;
  player?: FrameworkMotionPlayer;
  timer?: ReturnType<typeof setTimeout>;
  position?: string;
}
const states = new WeakMap<HTMLElement, LoadingState>();

function stop(element: HTMLElement) {
  const state = states.get(element);
  if (!state) return;
  clearTimeout(state.timer);
  state.player?.cancel();
  state.mask?.remove();
  if (state.position !== undefined) element.style.position = state.position;
  element.removeAttribute("aria-busy");
  states.delete(element);
}
function start(element: HTMLElement, text = "正在加载…") {
  if (states.has(element)) return;
  const state: LoadingState = {};
  states.set(element, state);
  element.setAttribute("aria-busy", "true");
  state.timer = setTimeout(() => {
    if (getComputedStyle(element).position === "static") {
      state.position = element.style.position;
      element.style.position = "relative";
    }
    const mask = document.createElement("div");
    mask.className = "brand-loading-mask";
    mask.setAttribute("role", "status");
    mask.setAttribute("aria-live", "polite");
    const icon = document.createElement("span");
    icon.className = "brand-loading-mark";
    icon.innerHTML = symbol;
    const label = document.createElement("span");
    label.textContent = text;
    mask.append(icon, label);
    element.append(mask);
    state.mask = mask;
    const svg = icon.querySelector("svg");
    if (svg) state.player = window.FrameworkMotion?.create(svg, { loop: true });
  }, 120);
}
export const brandLoading: Directive<HTMLElement, boolean> = {
  mounted(element, binding) {
    if (binding.value) start(element);
  },
  updated(element, binding) {
    if (binding.value) start(element);
    else stop(element);
  },
  beforeUnmount: stop
};

let routePath = "";
let routeHost: HTMLElement | null = null;
export function startRouteLoading(path: string) {
  if (routeHost) stop(routeHost);
  routePath = path;
  routeHost = document.getElementById("boot-loading")
    ? null
    : document.querySelector<HTMLElement>(".main-container");
  if (routeHost) start(routeHost, "正在打开页面…");
}
export function stopRouteLoading(path?: string) {
  if (path && routePath !== path) return;
  if (routeHost) stop(routeHost);
  routeHost = null;
  routePath = "";
}
