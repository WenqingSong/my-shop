package legacymigrate

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// legacyBalance 是 eshop_user 的资金/积分期初快照字段。
type legacyBalance struct {
	UID          int64
	NowMoney     string
	Integral     string
	CreateTime   sql.NullTime
	UserCreateAt sql.NullTime
}

// walletDomain 迁移用户钱包余额期初快照与期初入账流水。
// 严格只迁当前余额快照：user_bill / user_recharge 等历史流水仅作归档证据，
// 不进入新账本重新入账，避免余额重复累计。
type walletDomain struct{}

func (walletDomain) name() string { return "wallets" }

func (walletDomain) run(ctx context.Context, dc *domainContext) error {
	balances, err := loadLegacyBalances(ctx, dc.source)
	if err != nil {
		return err
	}
	accountStat := dc.stat("wallets")
	txStat := dc.stat("wallet_transactions")
	accountStat.SourceTotal = len(balances)
	txStat.SourceTotal = len(balances)

	existing, err := loadExistingAccounts(ctx, dc.target, "wallet_accounts")
	if err != nil {
		return err
	}

	for _, b := range balances {
		balance, err := YuanToCents(b.NowMoney)
		if err != nil {
			dc.skip("wallets", b.UID, "余额金额非法: "+err.Error())
			continue
		}
		if balance <= 0 {
			continue
		}
		accountStat.Eligible++
		txStat.Eligible++

		userNewID, ok := dc.mapped(entityUser, b.UID)
		if !ok {
			dc.skip("wallets", b.UID, "引用不存在或未迁移的用户")
			continue
		}
		key := userNewID
		if dc.dryRun && userNewID == plannedID {
			key = -b.UID
		}
		if _, done := existing[key]; done {
			dc.skipAlready("wallets")
			dc.skipAlready("wallet_transactions")
			continue
		}
		if dc.dryRun {
			accountStat.Migrated++
			txStat.Migrated++
			existing[key] = struct{}{}
			continue
		}

		if err := writeWalletOpening(ctx, dc, b, userNewID, balance); err != nil {
			dc.fail("wallets", "用户 old_id=%d 钱包期初迁移失败（已回滚）: %v", b.UID, err)
			continue
		}
		existing[key] = struct{}{}
		accountStat.Migrated++
		txStat.Migrated++
	}
	return nil
}

// writeWalletOpening 在同一事务内创建钱包账户并写入一条期初入账流水。
func writeWalletOpening(ctx context.Context, dc *domainContext, b *legacyBalance, userNewID, balance int64) error {
	tx, err := dc.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var createdAt any
	if b.CreateTime.Valid {
		createdAt = b.CreateTime.Time
	}
	res, err := tx.ExecContext(ctx,
		"INSERT INTO wallet_accounts (user_id, balance, created_at) VALUES (?, ?, COALESCE(?, CURRENT_TIMESTAMP))",
		userNewID, balance, createdAt)
	if err != nil {
		return fmt.Errorf("插入 wallet_accounts: %w", err)
	}
	accountID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取钱包账户新 ID: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO wallet_transactions
			(account_id, user_id, tx_type, direction, amount, balance_before, balance_after,
			 biz_type, biz_id, idempotency_key, remark, created_at)
		 VALUES (?, ?, 'legacy_opening', 1, ?, 0, ?, 'legacy_opening', ?, ?, '历史余额期初快照', COALESCE(?, CURRENT_TIMESTAMP))`,
		accountID, userNewID, balance, balance, b.UID,
		fmt.Sprintf("legacy:wallet_opening:%d", userNewID), createdAt); err != nil {
		return fmt.Errorf("插入 wallet_transactions: %w", err)
	}
	return tx.Commit()
}

// pointsDomain 迁移用户积分期初快照与期初积分流水。
// 只迁当前积分快照，user_bill 中的历史积分流水不重复累计。
type pointsDomain struct{}

func (pointsDomain) name() string { return "points" }

func (pointsDomain) run(ctx context.Context, dc *domainContext) error {
	balances, err := loadLegacyBalances(ctx, dc.source)
	if err != nil {
		return err
	}
	accountStat := dc.stat("points_accounts")
	logStat := dc.stat("points_logs")
	accountStat.SourceTotal = len(balances)
	logStat.SourceTotal = len(balances)

	existing, err := loadExistingAccounts(ctx, dc.target, "user_points_accounts")
	if err != nil {
		return err
	}

	for _, b := range balances {
		points, err := decimalToInteger(b.Integral)
		if err != nil {
			dc.skip("points_accounts", b.UID, "积分非法: "+err.Error())
			continue
		}
		if points <= 0 {
			continue
		}
		accountStat.Eligible++
		logStat.Eligible++

		userNewID, ok := dc.mapped(entityUser, b.UID)
		if !ok {
			dc.skip("points_accounts", b.UID, "引用不存在或未迁移的用户")
			continue
		}
		key := userNewID
		if dc.dryRun && userNewID == plannedID {
			key = -b.UID
		}
		if _, done := existing[key]; done {
			dc.skipAlready("points_accounts")
			dc.skipAlready("points_logs")
			continue
		}
		if dc.dryRun {
			accountStat.Migrated++
			logStat.Migrated++
			existing[key] = struct{}{}
			continue
		}

		if err := writePointsOpening(ctx, dc, b, userNewID, points); err != nil {
			dc.fail("points_accounts", "用户 old_id=%d 积分期初迁移失败（已回滚）: %v", b.UID, err)
			continue
		}
		existing[key] = struct{}{}
		accountStat.Migrated++
		logStat.Migrated++
	}
	return nil
}

func writePointsOpening(ctx context.Context, dc *domainContext, b *legacyBalance, userNewID, points int64) error {
	tx, err := dc.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var createdAt any
	if b.CreateTime.Valid {
		createdAt = b.CreateTime.Time
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO user_points_accounts (user_id, points, created_at) VALUES (?, ?, COALESCE(?, CURRENT_TIMESTAMP))",
		userNewID, points, createdAt)
	if err != nil {
		return fmt.Errorf("插入 user_points_accounts: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_points_logs
			(user_id, change_amount, points_before, points_after, change_type, biz_type, biz_id,
			 idempotency_key, remark, created_at)
		 VALUES (?, ?, 0, ?, 'legacy_opening', 'legacy_opening', ?, ?, '历史积分期初快照', COALESCE(?, CURRENT_TIMESTAMP))`,
		userNewID, points, points, b.UID,
		fmt.Sprintf("legacy:points_opening:%d", userNewID), createdAt); err != nil {
		return fmt.Errorf("插入 user_points_logs: %w", err)
	}
	return tx.Commit()
}

// loadLegacyBalances 读取未删除用户的余额与积分快照。
func loadLegacyBalances(ctx context.Context, source *sql.DB) ([]*legacyBalance, error) {
	rows, err := source.QueryContext(ctx,
		"SELECT uid, now_money, integral, create_time FROM eshop_user WHERE is_del = 0 ORDER BY uid")
	if err != nil {
		return nil, fmt.Errorf("读取 eshop_user 资金快照: %w", err)
	}
	defer rows.Close()

	var out []*legacyBalance
	for rows.Next() {
		var b legacyBalance
		var nowMoney, integral sql.NullString
		if err := rows.Scan(&b.UID, &nowMoney, &integral, &b.CreateTime); err != nil {
			return nil, fmt.Errorf("扫描 eshop_user 资金快照: %w", err)
		}
		b.NowMoney = nowMoney.String
		b.Integral = integral.String
		out = append(out, &b)
	}
	return out, rows.Err()
}

// loadExistingAccounts 读取目标账户表的 user_id 集合（重复执行幂等判断）。
func loadExistingAccounts(ctx context.Context, target *sql.DB, table string) (map[int64]struct{}, error) {
	rows, err := target.QueryContext(ctx, "SELECT user_id FROM "+table)
	if err != nil {
		if isTableNotExist(err) {
			return nil, fmt.Errorf("目标库缺少 %s 表，请先执行 `my-shop migrate up`", table)
		}
		return nil, fmt.Errorf("读取 %s: %w", table, err)
	}
	defer rows.Close()

	out := make(map[int64]struct{})
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("扫描 %s: %w", table, err)
		}
		out[userID] = struct{}{}
	}
	return out, rows.Err()
}

// decimalToInteger 把 decimal 字符串精确转换为整数（积分不允许小数）。
// 例："978.00"→978；"978.5"→错误。全程不使用 float。
func decimalToInteger(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("负值 %q", raw)
	}
	s = strings.TrimPrefix(s, "+")
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	if !isDigits(intPart) || (fracPart != "" && !isDigits(fracPart)) {
		return 0, fmt.Errorf("非法数值 %q", raw)
	}
	for _, c := range fracPart {
		if c != '0' {
			return 0, fmt.Errorf("积分含小数 %q", raw)
		}
	}
	v, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("非法数值 %q", raw)
	}
	return v, nil
}
