<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { listCoupons, stopCoupon } from "@/api/modules/coupon";
import type { CouponTemplateDTO } from "@/api/types/coupon";
import {
  filterCoupons,
  sliceCoupons,
  toCouponView,
  type CouponView
} from "@/api/adapters/coupon-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import CouponFormDialog from "./components/CouponFormDialog.vue";

/**
 * 优惠券模板管理。
 *
 * 后端接口只有 创建 / 列表 / 停止领取：
 * - 列表一次性返回全部模板（无分页参数），因此搜索、状态筛选与分页都在
 *   **完整数据集**上做，不是"只过滤当前页"；
 * - 没有详情、编辑、删除、重新启用接口，页面不提供对应按钮。
 */
defineOptions({ name: "CouponList" });

const canCreate = computed(() => hasPerms(PERMISSION.COUPON_CREATE));
const canStop = computed(() => hasPerms(PERMISSION.COUPON_STOP));

const allItems = ref<CouponTemplateDTO[]>([]);
const loading = ref(false);
const error = ref("");
const page = ref(1);
const size = ref(10);

const filters = reactive<{ keyword: string; status: 0 | 1 | "" }>({
  keyword: "",
  status: ""
});

const { run: runStop } = useSubmitGuard();

const filtered = computed(() =>
  filterCoupons(allItems.value, filters.keyword, filters.status)
);

const rows = computed<CouponView[]>(() =>
  sliceCoupons(filtered.value, page.value, size.value).map(toCouponView)
);

const total = computed(() => filtered.value.length);

/** 筛选条件变化时回到第一页（数据来源是全量列表） */
watch(
  () => [filters.keyword, filters.status],
  () => {
    page.value = 1;
  }
);

const detailVisible = ref(false);
const detail = ref<CouponView | null>(null);

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await listCoupons();
    allItems.value = result.items ?? [];
  } catch (cause) {
    allItems.value = [];
    error.value = resolveErrorMessage(cause, "优惠券列表加载失败");
  } finally {
    loading.value = false;
  }
}

function search() {
  page.value = 1;
}

function reset() {
  filters.keyword = "";
  filters.status = "";
  page.value = 1;
}

function openDetail(row: CouponView) {
  detail.value = row;
  detailVisible.value = true;
}

async function handleStop(row: CouponView) {
  try {
    await ElMessageBox.confirm(
      `确认停止「${row.name}」的新领取？已领取的券不受影响，且后端没有重新启用接口。`,
      "停止领取",
      {
        type: "warning",
        confirmButtonText: "停止领取",
        cancelButtonText: "取消"
      }
    );
  } catch {
    return;
  }

  await runStop(async () => {
    try {
      await stopCoupon(row.id);
      message("已停止新领取", { type: "success" });
      await load();
    } catch (cause) {
      message(resolveErrorMessage(cause, "停止领取失败"), { type: "error" });
    }
  });
}

function handlePageChange(next: number) {
  page.value = next;
}

function handleSizeChange(next: number) {
  size.value = next;
  page.value = 1;
}

const formVisible = ref(false);

function handleCreated() {
  void load();
}

onMounted(load);
</script>

<template>
  <div class="coupon-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">优惠券管理</h2>
        <p class="page-desc">满减券模板管理；领取与核销由用户端和后端完成</p>
      </div>
      <div class="page-actions">
        <el-button :loading="loading" @click="load"
          ><template #loading><BrandLoadingIcon /></template>刷新</el-button
        >
        <el-button v-if="canCreate" type="primary" @click="formVisible = true">
          新建优惠券
        </el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="使用说明"
      description="支持创建满减券和停止领取。创建后无法编辑或删除，停止领取后无法重新启用。"
    />

    <el-card shadow="never">
      <el-form :inline="true" @submit.prevent>
        <el-form-item label="名称搜索">
          <el-input
            v-model="filters.keyword"
            placeholder="按名称过滤"
            clearable
            class="filter-keyword"
            @keyup.enter="search"
          />
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="filters.status" class="filter-status">
            <el-option label="全部状态" value="" />
            <el-option label="可领取" :value="1" />
            <el-option label="已停止领取" :value="0" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>

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
        <el-table-column prop="name" label="名称" min-width="160" />
        <el-table-column label="规则" min-width="180">
          <template #default="{ row }">
            {{ row.thresholdText }}，减 {{ row.discountText }}
          </template>
        </el-table-column>
        <el-table-column label="发放" width="120" align="center">
          <template #default="{ row }">
            {{ row.issuedQuantity }} / {{ row.totalQuantity }}
          </template>
        </el-table-column>
        <el-table-column label="领取时间" min-width="200">
          <template #default="{ row }">{{ row.claimRangeText }}</template>
        </el-table-column>
        <el-table-column label="使用时间" min-width="200">
          <template #default="{ row }">{{ row.useRangeText }}</template>
        </el-table-column>
        <el-table-column label="状态" width="120">
          <template #default="{ row }">
            <el-tag :type="row.statusTagType" size="small">
              {{ row.statusLabel }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="170">
          <template #default="{ row }">{{ row.createdText }}</template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row)">
              详情
            </el-button>
            <el-button
              v-if="canStop && row.status === 1"
              link
              type="warning"
              @click="handleStop(row)"
            >
              停止领取
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
          layout="total, sizes, prev, pager, next"
          background
          @current-change="handlePageChange"
          @size-change="handleSizeChange"
        />
      </div>
    </el-card>

    <el-drawer v-model="detailVisible" title="优惠券详情" size="480px">
      <el-descriptions v-if="detail" :column="1" border>
        <el-descriptions-item label="模板 ID">
          {{ detail.id }}
        </el-descriptions-item>
        <el-descriptions-item label="名称">
          {{ detail.name }}
        </el-descriptions-item>
        <el-descriptions-item label="规则">
          {{ detail.thresholdText }}，减 {{ detail.discountText }}
        </el-descriptions-item>
        <el-descriptions-item label="发放数量">
          {{ detail.issuedQuantity }} / {{ detail.totalQuantity }}
        </el-descriptions-item>
        <el-descriptions-item label="领取时间">
          {{ detail.claimRangeText }}
        </el-descriptions-item>
        <el-descriptions-item label="使用时间">
          {{ detail.useRangeText }}
        </el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="detail.statusTagType" size="small">
            {{ detail.statusLabel }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="创建时间">
          {{ detail.createdText }}
        </el-descriptions-item>
      </el-descriptions>
      <p class="drawer-note">
        后端没有优惠券详情接口，此处展示列表中的完整模板字段；没有编辑能力。
      </p>
    </el-drawer>

    <CouponFormDialog v-model="formVisible" @saved="handleCreated" />
  </div>
</template>

<style lang="scss" scoped>
.coupon-page {
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

.page-actions {
  display: flex;
  gap: 8px;
}

.filter-keyword {
  width: 190px;
}

.filter-status {
  width: 150px;
}

.block-alert {
  margin-bottom: 12px;
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.drawer-note {
  margin-top: 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
