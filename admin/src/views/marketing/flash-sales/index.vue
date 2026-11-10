<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { formatCentsAsCny, parseYuanToCents } from "@/utils/money";
import { PERMISSION } from "@/utils/permission";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { createFlashSale, updateFlashSale } from "@/api/modules/flash-sale";
import {
  buildCreatePayload,
  buildUpdatePayload,
  FLASH_SALE_STATUS_OPTIONS,
  toActivityView,
  validateCreateForm,
  validateUpdateForm,
  type ActivityView,
  type FlashSaleBindingForm,
  type FlashSaleCreateForm,
  type FlashSaleUpdateForm
} from "@/api/adapters/flash-sale-adapter";
import type { FlashSaleStatus } from "@/api/types/flash-sale";
import SkuBindingPicker from "./components/SkuBindingPicker.vue";

/**
 * 秒杀活动管理。
 *
 * 关键事实：后端**没有活动列表/详情/删除接口**，只有创建与更新。
 * 因此页面不提供"活动列表"，只在本地展示本次创建/更新接口返回的活动快照；
 * 刷新页面后无法回读历史活动（这是后端能力缺失，不是 Mock 假数据）。
 *
 * 页面只维护活动配置，不接触 Redis Lua 准入、Kafka、Outbox、库存扣减与请求状态。
 */
defineOptions({ name: "FlashSaleManagement" });

const canCreate = computed(() => hasPerms(PERMISSION.FLASH_SALE_CREATE));
const canUpdate = computed(() => hasPerms(PERMISSION.FLASH_SALE_UPDATE));

const createForm = reactive<FlashSaleCreateForm>({
  name: "",
  range: undefined,
  bindings: []
});
const createErrors = ref<string[]>([]);
const { submitting: creating, run: runCreate } = useSubmitGuard();

const updateForm = reactive<FlashSaleUpdateForm>({
  id: null,
  name: "",
  status: "",
  range: undefined,
  bindings: []
});
const updateErrors = ref<string[]>([]);
const { submitting: updating, run: runUpdate } = useSubmitGuard();

const result = ref<ActivityView | null>(null);
const resultSource = ref<"create" | "update" | "">("");

const updateStatusOptions = [
  { value: "", label: "不修改" },
  ...FLASH_SALE_STATUS_OPTIONS
];

function addCreateBinding(binding: FlashSaleBindingForm) {
  if (createForm.bindings.some(item => item.skuId === binding.skuId)) {
    message("该 SKU 已添加", { type: "warning" });
    return;
  }
  createForm.bindings.push(binding);
}

function addUpdateBinding(binding: FlashSaleBindingForm) {
  if (updateForm.bindings.some(item => item.skuId === binding.skuId)) {
    message("该 SKU 已添加", { type: "warning" });
    return;
  }
  updateForm.bindings.push(binding);
}

function removeCreateBinding(key: string) {
  createForm.bindings = createForm.bindings.filter(item => item.key !== key);
}

function removeUpdateBinding(key: string) {
  updateForm.bindings = updateForm.bindings.filter(item => item.key !== key);
}

async function submitCreate() {
  createErrors.value = validateCreateForm(createForm);
  if (createErrors.value.length > 0) return;

  await runCreate(async () => {
    try {
      const activity = await createFlashSale(buildCreatePayload(createForm));
      result.value = toActivityView(activity);
      resultSource.value = "create";
      message("秒杀活动已创建（后端默认启用）", { type: "success" });

      createForm.name = "";
      createForm.range = undefined;
      createForm.bindings = [];
      createErrors.value = [];
    } catch (cause) {
      message(resolveErrorMessage(cause, "创建秒杀活动失败"), {
        type: "error"
      });
    }
  });
}

async function submitUpdate() {
  updateErrors.value = validateUpdateForm(updateForm);
  if (updateErrors.value.length > 0) return;

  const id = updateForm.id;
  if (!id) return;

  await runUpdate(async () => {
    try {
      const activity = await updateFlashSale(
        id,
        buildUpdatePayload(updateForm)
      );
      result.value = toActivityView(activity);
      resultSource.value = "update";
      message("秒杀活动已更新", { type: "success" });
      updateForm.bindings = [];
      updateErrors.value = [];
    } catch (cause) {
      // 保留表单，展示后端原文（12001/12007）
      message(resolveErrorMessage(cause, "更新秒杀活动失败"), {
        type: "error"
      });
    }
  });
}

function bindingPriceText(value: number | null): string {
  const cents = parseYuanToCents(value);
  return cents === null ? "—" : formatCentsAsCny(cents);
}

const updateStatusModel = computed<FlashSaleStatus | "">({
  get: () => updateForm.status,
  set: value => {
    updateForm.status = value;
  }
});
</script>

<template>
  <div class="flash-sale-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">秒杀活动管理</h2>
        <p class="page-desc">
          仅维护活动配置；秒杀交易由后端 Redis/Kafka 链路独立完成
        </p>
      </div>
    </div>

    <el-alert
      type="warning"
      :closable="false"
      show-icon
      title="后端只提供创建与更新接口"
      description="没有活动列表/详情/删除/独立启停接口。页面无法展示历史活动列表，刷新后也无法回读；下方仅展示本次创建或更新接口返回的真实活动快照。"
    />

    <el-alert
      v-if="!canCreate && !canUpdate"
      type="warning"
      :closable="false"
      show-icon
      title="当前管理员没有秒杀活动写权限"
      description="需要 flash_sale:create 或 flash_sale:update。"
    />

    <el-card v-if="canCreate" shadow="never">
      <template #header><span class="card-title">创建秒杀活动</span></template>

      <el-form label-width="110px">
        <el-form-item label="活动名称" required>
          <el-input
            v-model="createForm.name"
            maxlength="64"
            show-word-limit
            placeholder="例如：周末闪购"
            class="name-input"
          />
        </el-form-item>
        <el-form-item label="活动时间" required>
          <el-date-picker
            v-model="createForm.range"
            type="datetimerange"
            range-separator="至"
            start-placeholder="开始时间"
            end-placeholder="结束时间"
          />
        </el-form-item>
        <el-form-item label="选择 SKU">
          <SkuBindingPicker :disabled="creating" @add="addCreateBinding" />
        </el-form-item>
      </el-form>

      <el-table
        :data="createForm.bindings"
        border
        size="small"
        class="binding-table"
      >
        <el-table-column prop="productName" label="商品" min-width="180" />
        <el-table-column prop="skuName" label="SKU" min-width="150" />
        <el-table-column label="秒杀价" width="120" align="right">
          <template #default="{ row }">
            {{ bindingPriceText(row.flashPriceYuan) }}
          </template>
        </el-table-column>
        <el-table-column label="秒杀库存" width="110" align="center">
          <template #default="{ row }">{{ row.totalStock ?? "—" }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90" align="center">
          <template #default="{ row }">
            <el-button link type="danger" @click="removeCreateBinding(row.key)">
              移除
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-alert
        v-if="createErrors.length > 0"
        class="error-alert"
        type="error"
        :closable="false"
        show-icon
        title="请先修正以下问题"
      >
        <ul class="error-list">
          <li v-for="error in createErrors" :key="error">{{ error }}</li>
        </ul>
      </el-alert>

      <div class="form-actions">
        <el-button type="primary" :loading="creating" @click="submitCreate">
          创建活动
        </el-button>
      </div>
    </el-card>

    <el-card v-if="canUpdate" shadow="never">
      <template #header
        ><span class="card-title">更新活动（按 ID）</span></template
      >

      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="无详情接口，无法回填历史值"
        description="更新是部分更新：只提交填写/选择的字段。SKU 绑定只能更新该活动已绑定的 SKU，新增绑定会在创建时完成；秒杀库存不得低于已售数量。"
      />

      <el-form label-width="110px" class="update-form">
        <el-form-item label="活动 ID" required>
          <el-input-number
            v-model="updateForm.id"
            :min="1"
            :step="1"
            step-strictly
            controls-position="right"
          />
        </el-form-item>
        <el-form-item label="活动名称">
          <el-input
            v-model="updateForm.name"
            maxlength="64"
            placeholder="不填写则不修改"
            class="name-input"
          />
        </el-form-item>
        <el-form-item label="活动状态">
          <el-select v-model="updateStatusModel" class="status-select">
            <el-option
              v-for="option in updateStatusOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="活动时间">
          <el-date-picker
            v-model="updateForm.range"
            type="datetimerange"
            range-separator="至"
            start-placeholder="不修改开始时间"
            end-placeholder="不修改结束时间"
          />
        </el-form-item>
        <el-form-item label="更新 SKU">
          <SkuBindingPicker :disabled="updating" @add="addUpdateBinding" />
        </el-form-item>
      </el-form>

      <el-table
        :data="updateForm.bindings"
        border
        size="small"
        class="binding-table"
      >
        <el-table-column prop="productName" label="商品" min-width="180" />
        <el-table-column prop="skuName" label="SKU" min-width="150" />
        <el-table-column label="新秒杀价" width="120" align="right">
          <template #default="{ row }">
            {{ bindingPriceText(row.flashPriceYuan) }}
          </template>
        </el-table-column>
        <el-table-column label="新库存" width="100" align="center">
          <template #default="{ row }">{{ row.totalStock ?? "—" }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90" align="center">
          <template #default="{ row }">
            <el-button link type="danger" @click="removeUpdateBinding(row.key)">
              移除
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-alert
        v-if="updateErrors.length > 0"
        class="error-alert"
        type="error"
        :closable="false"
        show-icon
        title="请先修正以下问题"
      >
        <ul class="error-list">
          <li v-for="error in updateErrors" :key="error">{{ error }}</li>
        </ul>
      </el-alert>

      <div class="form-actions">
        <el-button type="primary" :loading="updating" @click="submitUpdate">
          提交更新
        </el-button>
      </div>
    </el-card>

    <el-card v-if="result" shadow="never">
      <template #header>
        <span class="card-title">
          最近操作结果（{{ resultSource === "create" ? "创建" : "更新" }}）
        </span>
      </template>

      <el-descriptions :column="3" border>
        <el-descriptions-item label="活动 ID">{{
          result.id
        }}</el-descriptions-item>
        <el-descriptions-item label="活动名称">{{
          result.name
        }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="result.statusTagType" size="small">
            {{ result.statusLabel }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="开始时间">{{
          result.startText
        }}</el-descriptions-item>
        <el-descriptions-item label="结束时间">{{
          result.endText
        }}</el-descriptions-item>
        <el-descriptions-item label="库存 / 已售">
          {{ result.totalStock }} / {{ result.totalSold }}
        </el-descriptions-item>
      </el-descriptions>

      <el-table :data="result.skus" border size="small" class="binding-table">
        <el-table-column prop="skuId" label="SKU ID" width="110" />
        <el-table-column label="秒杀价" width="130" align="right">
          <template #default="{ row }">{{ row.flashPriceText }}</template>
        </el-table-column>
        <el-table-column
          prop="totalStock"
          label="初始库存"
          width="110"
          align="center"
        />
        <el-table-column prop="sold" label="已售" width="100" align="center" />
      </el-table>

      <p class="result-note">
        以上字段来自接口响应；刷新页面后无法重新查询该活动。
      </p>
    </el-card>
  </div>
</template>

<style lang="scss" scoped>
.flash-sale-page {
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

.card-title {
  font-weight: 600;
}

.name-input {
  width: 320px;
}

.status-select {
  width: 160px;
}

.update-form {
  margin-top: 12px;
}

.binding-table {
  margin-top: 8px;
}

.error-alert {
  margin-top: 12px;
}

.error-list {
  padding-left: 18px;
  margin: 4px 0 0;
  font-size: 13px;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 14px;
}

.result-note {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
