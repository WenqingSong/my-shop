import { nextTick, onActivated, type Ref } from "vue";
import { onBeforeRouteLeave } from "vue-router";

/** 缓存列表返回时刷新数据，同时恢复真实滚动容器的位置。 */
export function useListReturn(
  root: Ref<HTMLElement | undefined>,
  reload: () => Promise<void>
) {
  let activated = false;
  let scrollTop = 0;
  const scroller = () =>
    root.value?.closest<HTMLElement>(".el-scrollbar__wrap");
  onBeforeRouteLeave(() => {
    scrollTop = scroller()?.scrollTop ?? 0;
  });
  onActivated(async () => {
    if (!activated) {
      activated = true;
      return;
    }
    await reload();
    await nextTick();
    scroller()?.scrollTo({ top: scrollTop, behavior: "instant" });
  });
}
