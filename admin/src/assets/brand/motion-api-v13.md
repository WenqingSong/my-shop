# Framework 动画接入 · v13

v13 保留正式 v5 几何、5.4 秒正常循环和 1.08 秒清晰标志展示。页面就绪后的退出改为 **640ms 高斯模糊消散**：不缩放、不补线、不收缩线条。v12 历史源及 [接口文档](../api.md) 原样保留。

```html
<div id="loading">
  <svg aria-label="页面加载中"></svg><span>FRAMEWORK</span>
</div>
<script src="motion-v13.js"></script>
<script>
  const overlay = document.querySelector("#loading");
  const intro = FrameworkMotion.create(overlay.querySelector("svg"), {
    loop: true
  });
  async function ready() {
    await preparePage(); // 包括需要展示的视频画面和可用的交互控制
    await intro.finish({ surface: overlay });
    overlay.remove();
    intro.cancel();
  }
</script>
```

## `finish({ surface })`

`surface` 可选，传入最初不透明且没有滤镜的加载层元素。播放器冻结当前标志画面，同时将整个加载层的 CSS Gaussian `blur()` 从 0px 增至 14px，透明度从 1 降至 0。暂时关闭加载层原有 CSS transition，避免外部过渡叠加拖慢逐帧退场。采用两端速度为零的平滑曲线，不增加或缩放图形。标志、文字和背景一起退场；滤镜仅作用于加载层，底下的视频保持清晰且继续播放。该元素应仅包含加载内容，不包含下一场景；全屏层须覆盖完整视口。

不传 `surface` 时，播放器通过自有 SVG `feGaussianBlur` 将当前图形逐渐模糊并淡出。图形留白阶段直接完成；传入加载层时，即使图形暂时为空白，文字与背景仍完整消散。

Promise 在 640ms 消散结束后兑现，重复调用返回同一个 Promise。正常播放速度不影响退出时长。完成时加载层保持透明，调用方应移除它，之后可 `cancel()` 清理实例。中途 `cancel()` 或 `restart()` 会还原传入元素原有的内联 opacity、filter、transition 和 will-change，并清理计时器、动画帧和可见性监听。

后台、系统减少动态效果时立即结束，不依赖动画帧。计时器兜底保证前台丢帧时仍能完成。若期间变成后台，也会立即清理并完成。

## 完整首轮由使用方控制

`finish()` 不负责页面资源加载或最低展示时长。需要至少播放一轮的站点，先等待 `intro.currentTime >= FrameworkMotion.duration`（5400ms），再确认资源可用并调用 `finish()`。减少动态效果模式呈现静态正式标志，可跳过这一等待。

视频建议在加载层仍完全不透明时启动并等待首个真实视频帧，确认按钮已经挂载和完成布局后，再退出加载层。不要在加载层消失之后才请求播放。

## 其余 API

`speed`、`currentTime`、`pause()`、`play()`、`restart()`、`cancel()`、`state`、`phase` 沿用 v12。v13 另外公开只读元数据 `FrameworkMotion.version = 13`、`duration = 5400`、`exitDuration = 640`。多个实例的几何、裁切和滤镜 ID 彼此独立。

品牌预览页 `/motion.html` 已接入 v13，“模拟加载完成”可审阅 SVG 高斯退场，全屏模拟演示整个品牌加载层的消散。
