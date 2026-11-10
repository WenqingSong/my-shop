<script setup lang="ts">
import { computed, reactive } from "vue";
import { ElMessageBox } from "element-plus";
import type { SkuDTO } from "@/api/types/sku";
import {
  buildSkuName,
  createDefaultSkuDraft,
  createSpecGroup,
  generateSkuDrafts,
  normalizeSpecValues,
  type SkuDraft,
  type SpecGroupDraft
} from "@/api/adapters/sku-adapter";
import { centsToYuanInput, yuanToCents } from "@/api/adapters/product-adapter";
import { message } from "@/utils/message";

/**
 * 商品规格与 SKU 编辑器。
 *
 * 交互模型：规格组 → 规格值 → 显式生成组合 → SKU 行。
 * 生成是**显式动作**，避免用户改一个规格值就把已填好的价格全部冲掉。
 *
 * 所有字段的持久化边界见组件内的 provisional 提示。
 */
const props = withDefaults(
  defineProps<{
    groups: SpecGroupDraft[];
    drafts: SkuDraft[];
    /** 商品价格（分），作为新 SKU 的默认价格 */
    defaultPriceCents: number;
    /** 编辑模式下后端已有的 SKU，用于生成时保留价格/状态 */
    existingSkus?: SkuDTO[];
    disabled?: boolean;
  }>(),
  { existingSkus: () => [], disabled: false }
);

const emit = defineEmits<{
  "update:groups": [groups: SpecGroupDraft[]];
  "update:drafts": [drafts: SkuDraft[]];
}>();

const newValueInputs = reactive<Record<string, string>>({});

const hasGroups = computed(() => props.groups.length > 0);

const provisionalTip = computed(
  () =>
    "规格组合用于辅助编辑，目前仅保存 SKU 名称、价格和状态。" +
    "规格值按「值1 / 值2」组合为名称；规格组结构、SKU 编码和图片暂不保存。"
);

function updateGroups(next: SpecGroupDraft[]) {
  emit("update:groups", next);
}

function updateDrafts(next: SkuDraft[]) {
  emit("update:drafts", next);
}

function addGroup() {
  updateGroups([
    ...props.groups,
    createSpecGroup(`规格 ${props.groups.length + 1}`, [""])
  ]);
}

function removeGroup(key: string) {
  updateGroups(props.groups.filter(group => group.key !== key));
}

function renameGroup(key: string, name: string) {
  updateGroups(
    props.groups.map(group => (group.key === key ? { ...group, name } : group))
  );
}

function addValue(group: SpecGroupDraft) {
  const value = (newValueInputs[group.key] ?? "").trim();
  if (!value) return;
  if (group.values.includes(value)) {
    message("该规格值已存在", { type: "warning" });
    return;
  }
  updateGroups(
    props.groups.map(item =>
      item.key === group.key
        ? { ...item, values: normalizeSpecValues([...item.values, value]) }
        : item
    )
  );
  newValueInputs[group.key] = "";
}

function removeValue(group: SpecGroupDraft, value: string) {
  updateGroups(
    props.groups.map(item =>
      item.key === group.key
        ? { ...item, values: item.values.filter(entry => entry !== value) }
        : item
    )
  );
}

/** 规格值 → SKU 组合（覆盖下方表格） */
async function regenerate() {
  if (props.disabled) return;

  const invalid = props.groups.find(
    group => normalizeSpecValues(group.values).length === 0
  );
  if (props.groups.length === 0) {
    message("请先添加规格组和规格值", { type: "warning" });
    return;
  }
  if (invalid) {
    message(`规格组「${invalid.name || "未命名"}」还没有规格值`, {
      type: "warning"
    });
    return;
  }

  if (props.drafts.length > 0) {
    try {
      await ElMessageBox.confirm(
        "将按当前规格重新生成 SKU 行，表格中手工添加的行会被覆盖（同名 SKU 的价格和状态会保留）。",
        "重新生成 SKU",
        { type: "warning", confirmButtonText: "生成", cancelButtonText: "取消" }
      );
    } catch {
      return;
    }
  }

  updateDrafts(
    generateSkuDrafts(props.groups, {
      defaultPriceCents: props.defaultPriceCents,
      existing: props.existingSkus
    })
  );
}

function addDraft() {
  const next = createDefaultSkuDraft(props.defaultPriceCents);
  next.name = `SKU ${props.drafts.length + 1}`;
  next.key = `manual-${Date.now()}-${props.drafts.length}`;
  updateDrafts([...props.drafts, next]);
}

function removeDraft(key: string) {
  if (props.drafts.length <= 1) {
    message("至少保留一行 SKU", { type: "warning" });
    return;
  }
  updateDrafts(props.drafts.filter(draft => draft.key !== key));
}

function updateDraft(key: string, patch: Partial<SkuDraft>) {
  updateDrafts(
    props.drafts.map(draft =>
      draft.key === key ? { ...draft, ...patch } : draft
    )
  );
}

function updateDraftPrice(key: string, yuan: number | null | undefined) {
  const cents = yuanToCents(yuan ?? null);
  updateDraft(key, { priceCents: cents ?? 0 });
}

/** 规格组合展示文本 */
function draftTitle(draft: SkuDraft) {
  return draft.values.length > 0 ? draft.values.join(" / ") : "无规格";
}

/** 名称与规格组合不一致时提示（名称可直接编辑） */
function needsNameSync(draft: SkuDraft) {
  if (draft.values.length === 0) return false;
  return draft.name.trim() !== buildSkuName(draft.values);
}
</script>

<template>
  <div class="sku-editor">
    <el-alert
      type="info"
      :closable="false"
      show-icon
      class="sku-editor__notice"
      title="规格保存说明"
      :description="provisionalTip"
    />

    <div class="sku-editor__groups">
      <div v-for="group in groups" :key="group.key" class="spec-group">
        <div class="spec-group__header">
          <el-input
            :model-value="group.name"
            class="spec-group__name"
            placeholder="规格名，如 颜色"
            :disabled="disabled"
            @update:model-value="renameGroup(group.key, $event)"
          />
          <el-button
            link
            type="danger"
            :disabled="disabled"
            @click="removeGroup(group.key)"
          >
            删除规格组
          </el-button>
        </div>

        <div class="spec-group__values">
          <el-tag
            v-for="value in group.values.filter(Boolean)"
            :key="value"
            :closable="!disabled"
            type="info"
            @close="removeValue(group, value)"
          >
            {{ value }}
          </el-tag>

          <el-input
            v-model="newValueInputs[group.key]"
            class="spec-group__value-input"
            size="small"
            placeholder="输入规格值后回车"
            :disabled="disabled"
            @keyup.enter="addValue(group)"
          />
          <el-button
            link
            type="primary"
            :disabled="disabled"
            @click="addValue(group)"
          >
            添加值
          </el-button>
        </div>
      </div>

      <div class="sku-editor__group-actions">
        <el-button :disabled="disabled" @click="addGroup">添加规格组</el-button>
        <el-button
          type="primary"
          plain
          :disabled="disabled"
          @click="regenerate"
        >
          根据规格生成 SKU 组合
        </el-button>
        <span class="sku-editor__hint">
          例：颜色（黑、白）× 尺码（40、41）→ 4 个 SKU
        </span>
      </div>
    </div>

    <el-table :data="drafts" border size="small" class="sku-editor__table">
      <el-table-column label="规格组合" min-width="150">
        <template #default="{ row }">
          <span>{{ draftTitle(row) }}</span>
        </template>
      </el-table-column>

      <el-table-column label="SKU 名称" min-width="190">
        <template #default="{ row }">
          <el-input
            :model-value="row.name"
            size="small"
            :disabled="disabled"
            maxlength="128"
            @update:model-value="updateDraft(row.key, { name: $event })"
          />
          <div v-if="needsNameSync(row)" class="sku-editor__name-hint">
            名称与规格组合不一致
          </div>
        </template>
      </el-table-column>

      <el-table-column label="价格（元）" width="170">
        <template #default="{ row }">
          <el-input-number
            :model-value="centsToYuanInput(row.priceCents)"
            size="small"
            :min="0"
            :max="999999.99"
            :precision="2"
            :step="1"
            :disabled="disabled"
            controls-position="right"
            @update:model-value="updateDraftPrice(row.key, $event)"
          />
        </template>
      </el-table-column>

      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-switch
            :model-value="row.status === 'enabled'"
            :disabled="disabled"
            inline-prompt
            active-text="启用"
            inactive-text="禁用"
            @update:model-value="
              updateDraft(row.key, {
                status: $event ? 'enabled' : 'disabled'
              })
            "
          />
        </template>
      </el-table-column>

      <el-table-column label="操作" width="90" align="center">
        <template #default="{ row }">
          <el-button
            link
            type="danger"
            :disabled="disabled"
            @click="removeDraft(row.key)"
          >
            移除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="sku-editor__footer">
      <el-button size="small" :disabled="disabled" @click="addDraft">
        添加 SKU 行
      </el-button>
      <span class="sku-editor__hint">
        {{
          hasGroups ? "可手工微调名称与价格" : "无规格商品可维护多行平铺 SKU"
        }}
      </span>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.sku-editor {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.sku-editor__notice {
  margin-bottom: 2px;
}

.sku-editor__groups {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}

.spec-group {
  padding: 10px 12px;
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
}

.spec-group__header {
  display: flex;
  gap: 8px;
  align-items: center;
}

.spec-group__name {
  width: 220px;
}

.spec-group__values {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-top: 10px;
}

.spec-group__value-input {
  width: 160px;
}

.sku-editor__group-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.sku-editor__table {
  width: 100%;
}

.sku-editor__name-hint {
  margin-top: 2px;
  font-size: 12px;
  color: var(--el-color-warning);
}

.sku-editor__footer {
  display: flex;
  gap: 8px;
  align-items: center;
}

.sku-editor__hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
