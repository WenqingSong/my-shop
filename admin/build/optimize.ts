/**
 * 此文件作用于 `vite.config.ts` 的 `optimizeDeps.include` 依赖预构建配置项
 * 依赖预构建，`vite` 启动时会将下面 include 里的模块，编译成 esm 格式并缓存到 node_modules/.vite 文件夹
 * 温馨提示：如果您使用的第三方库是全局引入（引入到 src/main.ts），就不需要再添加到 include 里，
 * 因为 vite 会自动将它们缓存到 node_modules/.vite
 */
const include = [
  "qs",
  "mitt",
  "dayjs",
  "axios",
  "pinia",
  "vue-i18n",
  "js-cookie",
  "vue-tippy",
  "pinyin-pro",
  "sortablejs",
  "@vueuse/core",
  "@vueuse/motion",
  "@pureadmin/utils",
  "responsive-storage"
];

/**
 * 在预构建中强制排除的依赖项
 * 温馨提示：平台推荐的使用方式是哪里需要哪里引入而且都是单个的引入，不需要预构建，直接让浏览器加载就好
 */
const exclude = ["@iconify/json"];

export { include, exclude };
