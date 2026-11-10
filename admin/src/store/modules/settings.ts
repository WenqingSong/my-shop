import { defineStore } from "pinia";
import { type setType, store, getConfig } from "../utils";

export const useSettingStore = defineStore("pure-setting", {
  state: (): setType => ({
    title: getConfig().Title ?? "SurgeCart",
    fixedHeader: getConfig().FixedHeader ?? true,
    hiddenSideBar: getConfig().HiddenSideBar ?? false
  }),
  getters: {
    getTitle(state) {
      return state.title;
    },
    getFixedHeader(state) {
      return state.fixedHeader;
    },
    getHiddenSideBar(state) {
      return state.hiddenSideBar;
    }
  },
  actions: {
    CHANGE_SETTING({ key, value }: { key: string; value: any }) {
      if (Reflect.has(this, key)) {
        (this as any)[key] = value;
      }
    },
    changeSetting(data: { key: string; value: any }) {
      this.CHANGE_SETTING(data);
    }
  }
});

export function useSettingStoreHook() {
  return useSettingStore(store);
}
