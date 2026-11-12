/* 直接调用品牌仓库 v13 播放器。应用可交互后退场，不强制等待完整循环。 */
(() => {
  const surface = document.getElementById("boot-loading");
  const svg = surface.querySelector(".boot-symbol");
  const originalSymbol = svg.innerHTML;
  const player = window.FrameworkMotion?.create(svg, { loop: true });
  const text = surface.querySelector(".boot-message");
  const retry = surface.querySelector("button");
  let finished = false;
  let finishing = null;
  const fail = () => {
    if (finished || finishing) return;
    clearTimeout(timer);
    player?.cancel();
    svg.innerHTML = originalSymbol;
    text.textContent = "工作台加载失败，请检查网络后重试。";
    retry.hidden = false;
    surface.setAttribute("role", "alert");
  };
  const timer = setTimeout(fail, 20000);
  retry.addEventListener("click", () => location.reload());
  const onError = event => {
    if (event instanceof ErrorEvent || event.target?.tagName === "SCRIPT")
      fail();
  };
  window.addEventListener("error", onError, true);
  window.FrameworkBoot = {
    fail,
    finish() {
      if (finishing) return finishing;
      clearTimeout(timer);
      finishing = (async () => {
        try {
          await player?.finish({ surface });
        } finally {
          finished = true;
          player?.cancel();
          surface.remove();
          window.removeEventListener("error", onError, true);
        }
      })();
      return finishing;
    }
  };
})();
