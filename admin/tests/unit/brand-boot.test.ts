import source from "../../public/brand/boot.js?raw";
import { runInNewContext } from "node:vm";
import { afterEach, describe, expect, it, vi } from "vitest";

function boot(withPlayer = true) {
  document.body.innerHTML =
    '<div id="boot-loading"><svg class="boot-symbol"><path d="M0 0L1 1" /></svg><span class="boot-message">加载中</span><button hidden>重试</button></div>';
  const player = {
    cancel: vi.fn(),
    finish: vi.fn().mockResolvedValue(undefined)
  };
  const create = vi.fn().mockReturnValue(player);
  const reload = vi.fn();
  const window = {
    FrameworkMotion: withPlayer ? { create } : undefined,
    FrameworkBoot: undefined as Window["FrameworkBoot"],
    addEventListener: vi.fn(),
    removeEventListener: vi.fn()
  };
  runInNewContext(source, {
    window,
    document,
    setTimeout,
    clearTimeout,
    ErrorEvent,
    location: { reload }
  });
  return { api: window.FrameworkBoot!, player, create, reload };
}
afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = "";
});
describe("品牌启动播放器生命周期", () => {
  it("页面就绪后调用官方退场并清理遮罩和定时器", async () => {
    vi.useFakeTimers();
    const loader = boot();
    expect(loader.create).toHaveBeenCalledOnce();
    await loader.api.finish();
    expect(loader.player.finish).toHaveBeenCalledWith({
      surface: expect.any(HTMLElement)
    });
    expect(loader.player.cancel).toHaveBeenCalledOnce();
    expect(document.getElementById("boot-loading")).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });
  it("超过等待上限后停止动画，显示重试入口", () => {
    vi.useFakeTimers();
    const loader = boot();
    vi.advanceTimersByTime(20000);
    expect(loader.player.cancel).toHaveBeenCalledOnce();
    expect(document.querySelector("button")!.hidden).toBe(false);
    expect(document.querySelector(".boot-message")!.textContent).toContain(
      "加载失败"
    );
    document.querySelector("button")!.click();
    expect(loader.reload).toHaveBeenCalledOnce();
  });
  it("播放器未能加载时，静态标志和页面退出仍可用", async () => {
    vi.useFakeTimers();
    const loader = boot(false);
    expect(document.querySelector("svg path")).not.toBeNull();
    await loader.api.finish();
    expect(document.getElementById("boot-loading")).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });
});
