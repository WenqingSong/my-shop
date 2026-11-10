<script setup lang="ts">
import { onMounted, ref } from "vue";
import { resolveErrorMessage } from "@/utils/error-message";
import { listArticles, getArticle } from "@/api/modules/article";
import {
  toArticleDetailView,
  toArticleItemView,
  type ArticleDetailView,
  type ArticleItemView
} from "@/api/adapters/article-adapter";

/**
 * 文章内容（只读）。
 *
 * 后端没有管理端文章接口：写接口要求用户 token 且校验作者本人，
 * 管理员 token 不适用；文章表也没有 status / 封面 / 分类字段。
 * 因此本页只提供公开文章的浏览与详情，创建/编辑/发布/下架/删除均未实现。
 */
defineOptions({ name: "ArticleList" });

const rows = ref<ArticleItemView[]>([]);
const total = ref(0);
const page = ref(1);
const size = ref(10);
const loading = ref(false);
const error = ref("");

const detailVisible = ref(false);
const detailLoading = ref(false);
const detailError = ref("");
const detail = ref<ArticleDetailView | null>(null);

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await listArticles({ page: page.value, size: size.value });
    rows.value = (result.items ?? []).map(toArticleItemView);
    total.value = result.total;
  } catch (cause) {
    rows.value = [];
    total.value = 0;
    error.value = resolveErrorMessage(cause, "文章列表加载失败");
  } finally {
    loading.value = false;
  }
}

async function openDetail(id: number) {
  detailVisible.value = true;
  detailLoading.value = true;
  detailError.value = "";
  detail.value = null;
  try {
    const article = await getArticle(id);
    detail.value = toArticleDetailView(article);
  } catch (cause) {
    detailError.value = resolveErrorMessage(cause, "文章详情加载失败");
  } finally {
    detailLoading.value = false;
  }
}

function handlePageChange(next: number) {
  page.value = next;
  void load();
}

function handleSizeChange(next: number) {
  size.value = next;
  page.value = 1;
  void load();
}

onMounted(load);
</script>

<template>
  <div class="article-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">文章内容</h2>
        <p class="page-desc">浏览公开文章与正文内容。</p>
      </div>
      <el-button :loading="loading" @click="load"
        ><template #loading><BrandLoadingIcon /></template>刷新</el-button
      >
    </div>

    <el-alert
      type="warning"
      :closable="false"
      show-icon
      title="当前仅支持只读"
      description="此处仅提供公开文章浏览。文章由作者维护，管理员暂不能创建、编辑、发布、下架或删除文章。"
    />

    <el-card shadow="never">
      <el-alert
        v-if="error"
        class="block-alert"
        type="error"
        :closable="false"
        show-icon
        :title="error"
      >
        <template #default>
          <el-button link type="primary" @click="load">重试</el-button>
        </template>
      </el-alert>

      <el-table v-loading="loading" :data="rows" border stripe>
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="title" label="标题" min-width="220" />
        <el-table-column prop="authorName" label="作者" min-width="140" />
        <el-table-column label="创建时间" min-width="170">
          <template #default="{ row }">{{ row.createdText }}</template>
        </el-table-column>
        <el-table-column label="更新时间" min-width="170">
          <template #default="{ row }">{{ row.updatedText }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row.id)">
              查看
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-bar">
        <el-pagination
          :current-page="page"
          :page-size="size"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @current-change="handlePageChange"
          @size-change="handleSizeChange"
        />
      </div>
    </el-card>

    <el-drawer v-model="detailVisible" title="文章详情" size="560px">
      <el-skeleton v-if="detailLoading" :rows="6" animated />

      <el-alert
        v-else-if="detailError"
        type="error"
        :closable="false"
        show-icon
        :title="detailError"
      />

      <template v-else-if="detail">
        <h3 class="article-title">{{ detail.title }}</h3>
        <div class="article-meta">
          <span>作者：{{ detail.authorName }}</span>
          <span>创建：{{ detail.createdText }}</span>
          <span>更新：{{ detail.updatedText }}</span>
        </div>
        <div class="article-content">{{ detail.content || "（无正文）" }}</div>
      </template>
    </el-drawer>
  </div>
</template>

<style lang="scss" scoped>
.article-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
}

.page-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.page-desc {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.block-alert {
  margin-bottom: 12px;
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.article-title {
  margin: 0 0 8px;
  font-size: 18px;
  font-weight: 600;
}

.article-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.article-content {
  padding: 14px;
  font-size: 14px;
  line-height: 1.8;
  color: var(--el-text-color-regular);
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}
</style>
