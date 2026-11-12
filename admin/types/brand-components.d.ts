import type BrandLoadingIcon from "../src/components/BrandLoadingIcon.vue";
import type PageLoading from "../src/components/PageLoading.vue";
declare module "vue" {
  export interface GlobalComponents {
    BrandLoadingIcon: typeof BrandLoadingIcon;
    PageLoading: typeof PageLoading;
  }
}
export {};
