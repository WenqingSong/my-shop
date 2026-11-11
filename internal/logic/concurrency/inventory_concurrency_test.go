package concurrency

// 4.1 库存扣减并发测试。
//
// 被测实现：internal/logic/inventory 的条件扣减
//   UPDATE inventories SET quantity = quantity - ? WHERE sku_id = ? AND quantity >= ?
// 命中 0 行即库存不足，并在同一事务内写 inventory_logs（before/after 快照）。
//
// 本文件验证的业务不变量：
//   IN-1 不允许超卖：成功扣减总量 ≤ 初始库存；
//   IN-2 库存不为负：最终 quantity ≥ 0，流水 after_qty 全为 ≥ 0；
//   IN-3 成功数与流水数一致：每条成功扣减恰好一条流水，失败请求 0 条；
//   IN-4 流水自洽：按 id 升序，before_qty == 上一条 after_qty，after == before ± change_qty；
//   IN-5 混合增/减并发下，最终库存 == 初始库存 + Σ增加 - Σ扣减。

import (
	"context"
	"testing"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// invLogRow 是 inventory_logs 的一条记录（只取一致性校验需要的列）。
type invLogRow struct {
	Id         int64 `json:"id"`
	ChangeType int   `json:"change_type"`
	ChangeQty  int64 `json:"change_qty"`
	BeforeQty  int64 `json:"before_qty"`
	AfterQty   int64 `json:"after_qty"`
}

// loadInventoryLedger 读取指定 SKU 的全部库存流水（id 升序）。
func loadInventoryLedger(t *testing.T, skuID int64) []*invLogRow {
	t.Helper()
	var rows []*invLogRow
	if err := g.DB().Model("inventory_logs").Ctx(context.Background()).
		Fields("id", "change_type", "change_qty", "before_qty", "after_qty").
		Where("sku_id", skuID).Order("id ASC").Scan(&rows); err != nil {
		t.Fatalf("查询库存流水失败: %v", err)
	}
	return rows
}

// assertLedgerChain 校验流水自洽：before 接续上一条 after，after = before ± change_qty 且不为负。
func assertLedgerChain(t *testing.T, rows []*invLogRow) (increaseQty, deductQty int64, deductCount int) {
	t.Helper()
	prevAfter := int64(0)
	for _, r := range rows {
		if r.ChangeQty <= 0 {
			t.Fatalf("流水 %d 的 change_qty=%d 必须为正", r.Id, r.ChangeQty)
		}
		if r.BeforeQty != prevAfter {
			t.Fatalf("流水 %d 断链：before_qty=%d，上一条 after_qty=%d", r.Id, r.BeforeQty, prevAfter)
		}
		switch r.ChangeType {
		case 1: // 增加
			if r.AfterQty != r.BeforeQty+r.ChangeQty {
				t.Fatalf("增加流水 %d 金额不自洽：%d + %d != %d", r.Id, r.BeforeQty, r.ChangeQty, r.AfterQty)
			}
			increaseQty += r.ChangeQty
		case 2: // 扣减
			if r.AfterQty != r.BeforeQty-r.ChangeQty {
				t.Fatalf("扣减流水 %d 金额不自洽：%d - %d != %d", r.Id, r.BeforeQty, r.ChangeQty, r.AfterQty)
			}
			deductQty += r.ChangeQty
			deductCount++
			if r.AfterQty < 0 {
				t.Fatalf("扣减流水 %d 扣出负数库存: after_qty=%d", r.Id, r.AfterQty)
			}
		default:
			t.Fatalf("流水 %d 出现未知 change_type=%d", r.Id, r.ChangeType)
		}
		prevAfter = r.AfterQty
	}
	return increaseQty, deductQty, deductCount
}

// TestInventoryConcurrentDeductNoOversell 覆盖 IN-1/IN-2/IN-3/IN-4：
// 初始库存 25，三轮共 120 个并发扣减请求（每轮 40 个 goroutine 同时起跑）。
// 只有恰好 25 次成功，其余全部返回 6001；最终库存 0，流水 25 条且自洽。
func TestInventoryConcurrentDeductNoOversell(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		initialStock = 25
		perRound     = 40 // 单轮并发度（控制连接数，避免压垮测试 MySQL）
		rounds       = 3
	)
	f := seedCatalog(t, "inv-oversell", 10000, initialStock)

	var successes, insufficient, other int
	for round := 0; round < rounds; round++ {
		errs := make([]error, perRound)
		runConcurrently(perRound, func(i int) {
			_, err := service.Inventory().Deduct(ctx, f.SkuID, 1, nil)
			errs[i] = err
		})
		for _, err := range errs {
			switch {
			case err == nil:
				successes++
			case codes.FromError(err) == codes.CodeInventoryInsufficient:
				insufficient++
			default:
				other++
				t.Logf("非预期错误: %v", err)
			}
		}
	}
	if other != 0 {
		t.Fatalf("出现 %d 个非「库存不足」错误，条件扣减路径异常", other)
	}
	if successes != initialStock {
		t.Fatalf("成功扣减 %d 次，期望恰好 %d 次（超卖或多扣）", successes, initialStock)
	}
	if insufficient != perRound*rounds-initialStock {
		t.Fatalf("库存不足拒绝 %d 次，期望 %d 次", insufficient, perRound*rounds-initialStock)
	}

	inv, err := service.Inventory().Get(ctx, f.SkuID)
	if err != nil {
		t.Fatalf("读取最终库存失败: %v", err)
	}
	if inv.Quantity != 0 {
		t.Fatalf("最终库存=%d，期望 0", inv.Quantity)
	}
	if inv.Quantity < 0 {
		t.Fatalf("库存出现负数: %d", inv.Quantity)
	}

	rows := loadInventoryLedger(t, f.SkuID)
	increaseQty, deductQty, deductCount := assertLedgerChain(t, rows)
	if increaseQty != initialStock {
		t.Fatalf("增加流水合计=%d，期望 %d", increaseQty, initialStock)
	}
	if deductCount != successes || deductQty != int64(successes) {
		t.Fatalf("扣减流水 %d 条/%d 件，与成功请求 %d 次不一致", deductCount, deductQty, successes)
	}
	// 失败请求不得留下任何流水：流水总条数 = 1 条初始化 + 成功扣减数。
	if len(rows) != 1+successes {
		t.Fatalf("流水总条数=%d，期望 %d（失败请求产生了错误流水）", len(rows), 1+successes)
	}
	if rows[len(rows)-1].AfterQty != inv.Quantity {
		t.Fatalf("最后一条流水 after_qty=%d 与库存 %d 不一致", rows[len(rows)-1].AfterQty, inv.Quantity)
	}
}

// TestInventoryConcurrentDeductInsufficientNoPhantomLog 覆盖 IN-3：
// 库存 10、30 个并发扣减 1 件。10 次成功、20 次 6001，且失败请求一条流水都不写。
func TestInventoryConcurrentDeductInsufficientNoPhantomLog(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		initialStock = 10
		attempts     = 30
	)
	f := seedCatalog(t, "inv-partial", 5000, initialStock)

	errs := make([]error, attempts)
	runConcurrently(attempts, func(i int) {
		_, err := service.Inventory().Deduct(ctx, f.SkuID, 1, nil)
		errs[i] = err
	})

	var successes int
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if code := codes.FromError(err); code != codes.CodeInventoryInsufficient {
			t.Fatalf("非预期错误码 %d: %v", code, err)
		}
	}
	if successes != initialStock {
		t.Fatalf("成功 %d 次，期望 %d 次", successes, initialStock)
	}

	rows := loadInventoryLedger(t, f.SkuID)
	increaseQty, deductQty, deductCount := assertLedgerChain(t, rows)
	if increaseQty != initialStock || deductQty != initialStock || deductCount != initialStock {
		t.Fatalf("流水 increase=%d deduct=%d count=%d，期望均等于 %d",
			increaseQty, deductQty, deductCount, initialStock)
	}
	if len(rows) != 1+initialStock {
		t.Fatalf("流水条数=%d，期望 %d：失败扣减不得写流水", len(rows), 1+initialStock)
	}

	inv, err := service.Inventory().Get(ctx, f.SkuID)
	if err != nil {
		t.Fatalf("读取库存失败: %v", err)
	}
	if inv.Quantity != 0 {
		t.Fatalf("最终库存=%d，期望 0", inv.Quantity)
	}
}

// TestInventoryConcurrentIncreaseAndDeductKeepLedgerChain 覆盖 IN-4/IN-5：
// 同一 SKU 上并发混合「增加 3 件」与「扣减 2 件」，最终库存必须精确等于
// 初始值 + Σ增加 - Σ扣减，且流水严格串行自洽（不存在两笔并发写同一条 before 的错账）。
func TestInventoryConcurrentIncreaseAndDeductKeepLedgerChain(t *testing.T) {
	requireConcurrencyDB(t)
	ctx := context.Background()

	const (
		initialStock = 100
		perRound     = 20
		rounds       = 2
	)
	f := seedCatalog(t, "inv-mixed", 3000, initialStock)

	var increaseOK, deductOK int
	for round := 0; round < rounds; round++ {
		errs := make([]error, perRound*2)
		runConcurrently(perRound*2, func(i int) {
			if i%2 == 0 {
				_, err := service.Inventory().Increase(ctx, f.SkuID, 3, nil)
				errs[i] = err
				return
			}
			_, err := service.Inventory().Deduct(ctx, f.SkuID, 2, nil)
			errs[i] = err
		})
		for i, err := range errs {
			if err == nil {
				if i%2 == 0 {
					increaseOK++
				} else {
					deductOK++
				}
				continue
			}
			if i%2 == 1 && codes.FromError(err) == codes.CodeInventoryInsufficient {
				continue
			}
			t.Fatalf("并发混合操作出现非预期错误: %v", err)
		}
	}

	wantQty := int64(initialStock) + int64(increaseOK)*3 - int64(deductOK)*2
	inv, err := service.Inventory().Get(ctx, f.SkuID)
	if err != nil {
		t.Fatalf("读取最终库存失败: %v", err)
	}
	if inv.Quantity != wantQty {
		t.Fatalf("最终库存=%d，期望 %d（Σ增加/Σ扣减与库存不一致）", inv.Quantity, wantQty)
	}

	rows := loadInventoryLedger(t, f.SkuID)
	increaseQty, deductQty, deductCount := assertLedgerChain(t, rows)
	if increaseQty != int64(initialStock)+int64(increaseOK)*3 {
		t.Fatalf("增加流水合计=%d，期望 %d", increaseQty, int64(initialStock)+int64(increaseOK)*3)
	}
	if deductQty != int64(deductOK)*2 || deductCount != deductOK {
		t.Fatalf("扣减流水=%d 件/%d 条，期望 %d 件/%d 条", deductQty, deductCount, deductOK*2, deductOK)
	}
	if rows[len(rows)-1].AfterQty != inv.Quantity {
		t.Fatalf("流水末值=%d 与库存 %d 不一致", rows[len(rows)-1].AfterQty, inv.Quantity)
	}
}
