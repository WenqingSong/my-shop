import { defineStore } from "pinia";
import {
  type multiType,
  type positionType,
  store,
  isUrl,
  isEqual,
  isNumber,
  isBoolean,
  getConfig,
  routerArrays,
  storageLocal,
  responsiveStorageNameSpace
} from "../utils";
import { usePermissionStoreHook } from "./permission";

export const useMultiTagsStore = defineStore("pure-multiTags", {
  // 标签页数据结构由路由动态决定，这里显式声明为宽松结构，避免下游推断为 never[]
  state: (): { multiTags: any[]; multiTagsCache: boolean | undefined } => ({
    // 存储标签页信息（路由信息）
    multiTags: storageLocal().getItem<StorageConfigs>(
      `${responsiveStorageNameSpace()}configure`
    )?.multiTagsCache
      ? storageLocal().getItem<StorageConfigs>(
          `${responsiveStorageNameSpace()}tags`
        )
      : ([
          ...routerArrays,
          ...usePermissionStoreHook().flatteningRoutes.filter(
            (v: any) => v?.meta?.fixedTag
          )
        ] as any),
    multiTagsCache: storageLocal().getItem<StorageConfigs>(
      `${responsiveStorageNameSpace()}configure`
    )?.multiTagsCache
  }),
  getters: {
    getMultiTagsCache(state) {
      return state.multiTagsCache;
    }
  },
  actions: {
    multiTagsCacheChange(multiTagsCache: boolean) {
      this.multiTagsCache = multiTagsCache;
      if (multiTagsCache) {
        storageLocal().setItem(
          `${responsiveStorageNameSpace()}tags`,
          this.multiTags
        );
      } else {
        storageLocal().removeItem(`${responsiveStorageNameSpace()}tags`);
      }
    },
    tagsCache(multiTags: Array<multiType>) {
      this.getMultiTagsCache &&
        storageLocal().setItem(
          `${responsiveStorageNameSpace()}tags`,
          multiTags
        );
    },
    handleTags<T>(
      mode: string,
      value?: T | multiType,
      position?: positionType
    ): any {
      switch (mode) {
        case "equal":
          this.multiTags = value as any;
          this.tagsCache(this.multiTags);
          break;
        case "push":
          {
            const tagVal = value as multiType;
            // 不添加到标签页
            if (tagVal?.meta?.hiddenTag) return;
            // 如果是外链无需添加信息到标签页
            if (isUrl(tagVal?.name)) return;
            // 如果title为空拒绝添加空信息到标签页
            if ((tagVal?.meta?.title?.length ?? 0) === 0) return;
            // showLink:false 不添加到标签页
            if (isBoolean(tagVal?.meta?.showLink) && !tagVal?.meta?.showLink)
              return;
            const tagPath = tagVal.path;
            const tagHasExits = this.multiTags.some((tag: any) => {
              return (
                tag.path === tagPath &&
                isEqual(tag?.query, tagVal?.query) &&
                isEqual(tag?.params, tagVal?.params)
              );
            });

            if (tagHasExits) return;

            // 动态路由可打开的最大数量
            const dynamicLevel = tagVal?.meta?.dynamicLevel ?? -1;
            if (dynamicLevel > 0) {
              if (
                this.multiTags.filter((e: any) => e?.path === tagPath).length >=
                dynamicLevel
              ) {
                // 如果当前已打开的动态路由数大于dynamicLevel，替换第一个动态路由标签
                const index = this.multiTags.findIndex(
                  (item: any) => item?.path === tagPath
                );
                index !== -1 && this.multiTags.splice(index, 1);
              }
            }
            this.multiTags.push(value);
            this.tagsCache(this.multiTags);
            const maxTagsLevel = getConfig()?.MaxTagsLevel;
            if (maxTagsLevel && isNumber(maxTagsLevel)) {
              if (this.multiTags.length > maxTagsLevel) {
                this.multiTags.splice(1, 1);
              }
            }
          }
          break;
        case "splice":
          if (!position) {
            const index = this.multiTags.findIndex(
              (v: any) => v.path === value
            );
            if (index === -1) return;
            this.multiTags.splice(index, 1);
          } else {
            this.multiTags.splice(
              position.startIndex ?? 0,
              position.length ?? 0
            );
          }
          this.tagsCache(this.multiTags);
          return this.multiTags;
        case "slice":
          return this.multiTags.slice(-1);
      }
    }
  }
});

export function useMultiTagsStoreHook() {
  return useMultiTagsStore(store);
}
