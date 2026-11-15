package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// WriteWarning 写入提示（§4.2）。
type WriteWarning struct {
	Frozen    bool   `json:"frozen"`
	Duplicate bool   `json:"duplicate"`
	Message   string `json:"message"`
}

// TxnWriteResult 交易写入结果（§4.2）。
type TxnWriteResult struct {
	Primary *store.Transaction
	Linked  []*store.Transaction
	Warning WriteWarning
}

// TxnItemInput 明细输入。
type TxnItemInput struct {
	Name                string
	SubCategoryID       *string
	OriginalAmountCents int64
	PaidAmountCents     int64
	DiscountReason      string
	SortOrder           int
}

// TransferInput 转账输入。
type TransferInput struct {
	FromAccountID string
	ToAccountID   string
}

// CreateTxnInput 创建交易输入（§5.4 端点 36）。
type CreateTxnInput struct {
	TransactionTime     *int64 // UTC 秒；nil → now
	TransactionType     string
	AmountCents         int64
	Currency            string
	AccountID           string
	CategoryID          *string
	SubCategoryID       *string
	MerchantID          *string
	MerchantText        string
	RegionID            *string
	CounterpartyName    string
	CounterpartyAccount string
	TagIDs              []string
	Remark              string
	Source              string
	ExternalID          string
	Items               []TxnItemInput
	Transfer            *TransferInput
	AllowFrozen         bool
	AllowDuplicate      bool
	// TimePrecision v1.2：'second' | 'day'；空 → 'second'（由 Create 归一化）。
	// 日期型源导入的行标为 'day'，表示"时间由系统按当日顺序分布"。
	TimePrecision string
	// FreezeConfirmed v1.2 §9：nil → true（已冻结，历史语义）。
	// 导入「待确认」的行显式传 false：即使早于冻结边界也保持可写、不参与物化。
	FreezeConfirmed *bool
	// ImportBatchID 产生该行的导入批次（imp_xxx），供「按批次确认」使用。
	ImportBatchID string
}

// UpdateTxnInput 更新交易输入（§5.4 端点 38）。
type UpdateTxnInput struct {
	TransactionTime     *int64
	AmountCents         *int64
	AccountID           *string
	CategoryID          **string
	SubCategoryID       **string
	MerchantID          **string
	MerchantText        *string
	RegionID            **string
	CounterpartyName    *string
	CounterpartyAccount *string
	TagIDs              *[]string
	Remark              *string
	Items               *[]TxnItemInput
}

// TxnService 交易核心：创建/更新/软删/恢复/彻底删除/去重/转账（§6.6/§6.8）。
type TxnService struct {
	s       *store.Store
	meta    *store.MetaRepo
	tx      *store.TxRepo
	master  *store.MasterRepo
	balance *BalanceService
}

// NewTxnService 构造。
func NewTxnService(s *store.Store, meta *store.MetaRepo, txr *store.TxRepo,
	master *store.MasterRepo, balance *BalanceService) *TxnService {
	return &TxnService{s: s, meta: meta, tx: txr, master: master, balance: balance}
}

// frozenError 统一 409 FROZEN 错误体（含 details）。
func frozenError(txTime, boundary int64) *apierr.Error {
	return apierr.New(apierr.CodeFrozen, "该交易已进入冻结区，不可直接修改，请使用调整分录").
		WithDetails(map[string]any{
			"transaction_time": timeutil.FormatLocal(txTime),
			"freeze_boundary":  timeutil.FormatLocal(boundary),
			"suggest":          "POST /api/transactions/{id}/adjust",
		})
}

// validate 基础字段校验。
func (s *TxnService) validate(in *CreateTxnInput) error {
	if in.TransactionType != "expense" && in.TransactionType != "income" && in.TransactionType != "transfer" {
		return apierr.New(apierr.CodeValidation, "transaction_type 必须为 expense|income|transfer")
	}
	if in.AmountCents <= 0 {
		return apierr.New(apierr.CodeValidation, "amount_cents 必须为正整数（分）")
	}
	if in.TransactionType == "transfer" {
		if in.Transfer == nil || in.Transfer.FromAccountID == "" || in.Transfer.ToAccountID == "" {
			return apierr.New(apierr.CodeValidation, "转账必须提供 transfer.from_account_id / to_account_id")
		}
		if in.Transfer.FromAccountID == in.Transfer.ToAccountID {
			return apierr.New(apierr.CodeValidation, "转出与转入账户不能相同")
		}
		if len(in.Items) > 0 {
			return apierr.New(apierr.CodeValidation, "转账不支持多物品明细")
		}
	} else if in.AccountID == "" {
		return apierr.New(apierr.CodeValidation, "account_id 必填")
	}
	return nil
}

// Create 创建交易（支出/收入/内部转账）。去重与冻结判定在写事务外预检、事务内落库。
func (s *TxnService) Create(ctx context.Context, in *CreateTxnInput) (*TxnWriteResult, error) {
	if err := s.validate(in); err != nil {
		return nil, err
	}
	now := time.Now()
	txTime := now.Unix()
	if in.TransactionTime != nil {
		txTime = *in.TransactionTime
	}
	boundary := s.balance.FreezeBoundary(now.Unix())
	lastFrozen, err := s.balance.LastFrozenAt()
	if err != nil {
		return nil, err
	}
	// §9.3 写保护唯一判定：待确认行（FreezeConfirmed=false）不受时间影响，永远可写。
	confirmed := in.FreezeConfirmed == nil || *in.FreezeConfirmed
	frozen := confirmed && txTime < boundary
	// I3 防御：待确认行若直接落在「已物化区」之前（该行不会有物化值，也不在动态窗口里），
	// 必须整表重算把边界回退到它。当前无 HTTP 调用方会这么传，但输入里已有该字段，
	// 属于「路径一旦开放就会静默吞钱」的位置，故在此兜住。
	pendingBack := !confirmed && txTime < lastFrozen

	res := &TxnWriteResult{}

	// ---- 冻结区判定 ----
	if frozen && !in.AllowFrozen {
		return nil, frozenError(txTime, boundary)
	}
	if frozen {
		res.Warning.Frozen = true
		res.Warning.Message = "该笔已进入冻结区，不可直接修改"
	}

	// ---- 去重（§6.8）----
	cand := &store.Transaction{
		TransactionTime: txTime,
		TransactionType: in.TransactionType,
		AmountCents:     in.AmountCents,
		AccountID:       in.AccountID,
		MerchantID:      in.MerchantID,
		MerchantText:    in.MerchantText,
		ExternalID:      in.ExternalID,
	}
	if in.TransactionType == "transfer" {
		cand.AccountID = in.Transfer.FromAccountID
	}
	dup, rule, err := s.tx.FindDuplicate(cand)
	if err != nil {
		return nil, err
	}
	if dup != nil && !in.AllowDuplicate {
		return nil, apierr.New(apierr.CodeDuplicate, "发现相同交易（时间/金额/账户/商户一致），确认不是重复记录？").
			WithDetails(map[string]any{
				"matched_transaction_id": dup.ID,
				"rule":                   rule,
			})
	}
	if dup != nil {
		res.Warning.Duplicate = true
		if res.Warning.Message != "" {
			res.Warning.Message += "；"
		}
		res.Warning.Message += "已按确认写入重复记录"
	}

	source := in.Source
	if source == "" {
		source = "manual"
	}
	precision := NormalizeTimePrecision(in.TimePrecision)

	err = s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		loc := timeutil.Local()
		if in.TransactionType == "transfer" {
			gid := ids.ShortID("tg_")
			out := &store.Transaction{
				TransactionType: "transfer", AmountCents: in.AmountCents,
				Currency: in.Currency, AccountID: in.Transfer.FromAccountID,
				TransferDirection: strPtr("out"), TransferGroupID: &gid,
				CounterAccountID: &in.Transfer.ToAccountID,
				CounterpartyName: in.CounterpartyName, CounterpartyAccount: in.CounterpartyAccount,
				Remark: in.Remark, Source: source, ExternalID: in.ExternalID,
				TransactionTime: txTime, LocalDate: timeutil.LocalDate(txTime),
				TimePrecision: precision, FreezeConfirmed: boolToInt(confirmed),
				FreezeSpecified: true, ImportBatchID: in.ImportBatchID,
			}
			if err = allocAndInsert(tx, s.tx, out, loc); err != nil {
				return err
			}
			inRow := &store.Transaction{
				TransactionType: "transfer", AmountCents: in.AmountCents,
				Currency: in.Currency, AccountID: in.Transfer.ToAccountID,
				TransferDirection: strPtr("in"), TransferGroupID: &gid,
				CounterAccountID: &in.Transfer.FromAccountID,
				CounterpartyName: in.CounterpartyName, CounterpartyAccount: in.CounterpartyAccount,
				Remark: in.Remark, Source: source,
				TransactionTime: txTime, LocalDate: timeutil.LocalDate(txTime),
				TimePrecision: precision, FreezeConfirmed: boolToInt(confirmed),
				FreezeSpecified: true, ImportBatchID: in.ImportBatchID,
			}
			if err = allocAndInsert(tx, s.tx, inRow, loc); err != nil {
				return err
			}
			res.Primary = out
			res.Linked = []*store.Transaction{inRow}
		} else {
			t := &store.Transaction{
				TransactionType: in.TransactionType, AmountCents: in.AmountCents,
				Currency: in.Currency, AccountID: in.AccountID,
				CategoryID: in.CategoryID, SubCategoryID: in.SubCategoryID,
				MerchantID: in.MerchantID, MerchantText: in.MerchantText,
				RegionID: in.RegionID, CounterpartyName: in.CounterpartyName,
				CounterpartyAccount: in.CounterpartyAccount,
				Remark:              in.Remark, Source: source, ExternalID: in.ExternalID,
				TransactionTime: txTime, LocalDate: timeutil.LocalDate(txTime),
				TimePrecision: precision, FreezeConfirmed: boolToInt(confirmed),
				FreezeSpecified: true, ImportBatchID: in.ImportBatchID,
			}
			if err = allocAndInsert(tx, s.tx, t, loc); err != nil {
				return err
			}
			if err = s.tx.ReplaceTagsTx(tx, t.ID, in.TagIDs); err != nil {
				return err
			}
			if len(in.Items) > 0 {
				items := make([]store.TransactionItem, len(in.Items))
				for i, it := range in.Items {
					items[i] = store.TransactionItem{
						Name: it.Name, SubCategoryID: it.SubCategoryID,
						OriginalAmountCents: it.OriginalAmountCents,
						PaidAmountCents:     it.PaidAmountCents,
						DiscountReason:      it.DiscountReason,
						SortOrder:           it.SortOrder,
					}
				}
				if err = s.tx.ReplaceItemsTx(tx, t.ID, items); err != nil {
					return err
				}
			}
			res.Primary = t
		}
		// 冻结区写入 → 置标志，下次物化走 ForceFull（§6.4）
		// 待确认行落进物化区之前（pendingBack）同理必须重算，否则金额会静默消失。
		if frozen || pendingBack {
			if err = s.meta.SetTx(tx, "frozen_write_flag", "1"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// allocAndInsert 分配 19 位 ID + created_seq 并插入。
func allocAndInsert(tx *sql.Tx, repo *store.TxRepo, t *store.Transaction, loc *time.Location) error {
	nowUnix := timeutil.NowUnix()
	id, err := ids.NewTransactionID(tx, time.Now(), loc)
	if err != nil {
		return err
	}
	seq, err := ids.AllocGlobalSeq(tx, nowUnix)
	if err != nil {
		return err
	}
	t.ID = id
	t.CreatedSeq = seq
	return repo.InsertTx(tx, t)
}

func strPtr(s string) *string { return &s }

// Get 取单条交易。
func (s *TxnService) Get(id string) (*store.Transaction, error) {
	t, err := s.tx.GetByID(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, apierr.New(apierr.CodeNotFound, "交易不存在")
	}
	return t, nil
}

// Update 更新动态区交易（端点 38）。转账行禁改金额/账户。
func (s *TxnService) Update(ctx context.Context, id string, in *UpdateTxnInput) (*store.Transaction, error) {
	old, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if old.DeletedAt != nil {
		return nil, apierr.New(apierr.CodeNotFound, "交易已删除，请先恢复")
	}
	boundary := s.balance.FreezeBoundary(time.Now().Unix())
	if IsWriteProtected(old, boundary) {
		return nil, frozenError(old.TransactionTime, boundary)
	}
	if old.TransactionType == "transfer" {
		if (in.AmountCents != nil && *in.AmountCents != old.AmountCents) ||
			(in.AccountID != nil && *in.AccountID != old.AccountID) {
			return nil, apierr.New(apierr.CodeTransferImmutable,
				"转账金额与账户不可直接修改，请使用调整分录")
		}
	}
	// 应用字段
	deferPendingRecompute := false
	t := *old
	if in.TransactionTime != nil {
		t.TransactionTime = *in.TransactionTime
		t.LocalDate = timeutil.LocalDate(*in.TransactionTime)
		// 改时间不能把「已冻结」的交易推进冻结区；待确认行允许改到任何历史时间（§9.3 #3）
		if old.FreezeConfirmed == 1 && t.TransactionTime < boundary {
			return nil, frozenError(t.TransactionTime, boundary)
		}
		// 待确认行若被改到当前物化边界之前，会违反 I3：必须整体重算把边界拉回。
		if old.FreezeConfirmed == 0 {
			if last, lerr := s.balance.LastFrozenAt(); lerr == nil && t.TransactionTime < last {
				deferPendingRecompute = true
			}
		}
	}
	if in.AmountCents != nil {
		if *in.AmountCents <= 0 {
			return nil, apierr.New(apierr.CodeValidation, "amount_cents 必须为正整数（分）")
		}
		t.AmountCents = *in.AmountCents
	}
	if in.AccountID != nil {
		t.AccountID = *in.AccountID
	}
	if in.CategoryID != nil {
		t.CategoryID = *in.CategoryID
	}
	if in.SubCategoryID != nil {
		t.SubCategoryID = *in.SubCategoryID
	}
	if in.MerchantID != nil {
		t.MerchantID = *in.MerchantID
	}
	if in.MerchantText != nil {
		t.MerchantText = *in.MerchantText
	}
	if in.RegionID != nil {
		t.RegionID = *in.RegionID
	}
	if in.CounterpartyName != nil {
		t.CounterpartyName = *in.CounterpartyName
	}
	if in.CounterpartyAccount != nil {
		t.CounterpartyAccount = *in.CounterpartyAccount
	}
	if in.Remark != nil {
		t.Remark = *in.Remark
	}
	err = s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		if err = s.tx.UpdateMutableTx(tx, &t); err != nil {
			return err
		}
		if in.TagIDs != nil {
			if err = s.tx.ReplaceTagsTx(tx, id, *in.TagIDs); err != nil {
				return err
			}
		}
		if in.Items != nil {
			items := make([]store.TransactionItem, len(*in.Items))
			for i, it := range *in.Items {
				items[i] = store.TransactionItem{
					Name: it.Name, SubCategoryID: it.SubCategoryID,
					OriginalAmountCents: it.OriginalAmountCents,
					PaidAmountCents:     it.PaidAmountCents,
					DiscountReason:      it.DiscountReason,
					SortOrder:           it.SortOrder,
				}
			}
			if err = s.tx.ReplaceItemsTx(tx, id, items); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if deferPendingRecompute {
		// 待确认行被移到物化区之前 → 必须立刻整表重算（边界回退），
		// 否则该行金额会静默消失且普通物化修不回来
		s.forcePendingBoundaryRefresh(ctx)
	}
	return s.tx.GetByID(id)
}

// SoftDelete 软删除（端点 39）。转账成对删除由 withTransfer 控制（默认 true）。
func (s *TxnService) SoftDelete(ctx context.Context, id string, withTransfer bool) error {
	t, err := s.Get(id)
	if err != nil {
		return err
	}
	if t.DeletedAt != nil {
		return nil // 幂等
	}
	boundary := s.balance.FreezeBoundary(time.Now().Unix())
	if IsWriteProtected(t, boundary) {
		return frozenError(t.TransactionTime, boundary)
	}
	ids := []string{id}
	if t.TransactionType == "transfer" && withTransfer && t.TransferGroupID != nil {
		group, err := s.tx.GetByTransferGroup(*t.TransferGroupID)
		if err != nil {
			return err
		}
		for _, g := range group {
			if g.ID != id && g.DeletedAt == nil {
				if IsWriteProtected(g, boundary) {
					return frozenError(g.TransactionTime, boundary)
				}
				ids = append(ids, g.ID)
			}
		}
	}
	now := timeutil.NowUnix()
	return s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, txID := range ids {
			if err := s.tx.SoftDeleteTx(tx, txID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Restore 恢复软删除（端点 40），转账成对恢复。
func (s *TxnService) Restore(ctx context.Context, id string) error {
	t, err := s.Get(id)
	if err != nil {
		return err
	}
	if t.DeletedAt == nil {
		return nil // 幂等
	}
	ids := []string{id}
	restored := []*store.Transaction{t}
	if t.TransactionType == "transfer" && t.TransferGroupID != nil {
		group, err := s.tx.GetByTransferGroup(*t.TransferGroupID)
		if err != nil {
			return err
		}
		for _, g := range group {
			if g.ID != id && g.DeletedAt != nil {
				ids = append(ids, g.ID)
				restored = append(restored, g)
			}
		}
	}
	// I3（§9.3）：待确认行（freeze_confirmed=0）软删期间物化边界可能已越过它的时间点
	// （同批其它行被确认、或主动 rematerialize 都会推进 last_frozen_at）。此时把它放回来，
	// 该行既不进快照（无物化值）也不进动态窗口（time < B）→ 金额会静默消失，且
	// Materialize 因「边界不后退」永远修不回来。故恢复后必须整表重算（边界回退到该行）。
	last, err := s.balance.LastFrozenAt()
	if err != nil {
		return err
	}
	pendingBack := false
	for _, row := range restored {
		if row.FreezeConfirmed == 0 && row.TransactionTime < last {
			pendingBack = true
			break
		}
	}
	now := timeutil.NowUnix()
	if err := s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, txID := range ids {
			if err := s.tx.RestoreTx(tx, txID, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if pendingBack {
		s.forcePendingBoundaryRefresh(ctx)
	}
	return nil
}

// forcePendingBoundaryRefresh 待确认行回到（或移入）物化区之前时，立刻整表重算。
//
// 为什么必须"立刻"：这类行既不进快照也不进动态窗口，金额会静默消失；而
// Materialize 有「边界不后退」保护，普通补偿永远修不回来（只有 ForceFull 会先
// 把 last_frozen_at 归零）。故置 frozen_write_flag 后立即调一次 EnsureFresh
// （见 flag=1 即 ForceFull，不受 30s 节流），不把错误余额留给下一次查询（§9.4 同款要求）。
func (s *TxnService) forcePendingBoundaryRefresh(ctx context.Context) {
	if err := s.meta.Set("frozen_write_flag", "1"); err != nil {
		slog.Error("置 frozen_write_flag 失败", "err", err)
		return
	}
	if _, err := s.balance.Current(ctx); err != nil {
		slog.Error("待确认行回到物化区后重算失败（等待下次查询补偿）", "err", err)
	}
}

// Purge 物理删除（端点 41）：仅允许 deleted_at < now-180d。
func (s *TxnService) Purge(ctx context.Context, id string) error {
	t, err := s.Get(id)
	if err != nil {
		return err
	}
	if t.DeletedAt == nil {
		return apierr.New(apierr.CodeValidation, "仅已软删除的交易可以彻底删除")
	}
	cutoff := time.Now().AddDate(0, 0, -180).Unix()
	if *t.DeletedAt >= cutoff {
		return apierr.New(apierr.CodeRetention, "软删除未满 180 天，不可彻底删除").
			WithDetails(map[string]any{
				"deleted_at":    timeutil.FormatLocal(*t.DeletedAt),
				"purge_allowed": timeutil.FormatLocal(*t.DeletedAt + 180*86400),
			})
	}
	ids := []string{id}
	if t.TransactionType == "transfer" && t.TransferGroupID != nil {
		group, err := s.tx.GetByTransferGroup(*t.TransferGroupID)
		if err != nil {
			return err
		}
		for _, g := range group {
			if g.ID != id && g.DeletedAt != nil && *g.DeletedAt < cutoff {
				ids = append(ids, g.ID)
			}
		}
	}
	return s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, txID := range ids {
			if err := s.tx.PurgeTx(tx, txID); err != nil {
				return err
			}
		}
		return nil
	})
}

// String 便于调试。
func (r *TxnWriteResult) String() string {
	return fmt.Sprintf("TxnWriteResult{primary=%s, linked=%d}", r.Primary.ID, len(r.Linked))
}

// 时间精度取值（与 sheetparse / API 契约一致）。
const (
	precisionSecond = "second"
	precisionDay    = "day"
)

// NormalizeTimePrecision 归一化时间精度：仅接受 'day'，其余一律 'second'。
func NormalizeTimePrecision(p string) string {
	if p == precisionDay {
		return precisionDay
	}
	return precisionSecond
}

// BatchSkipReason 批量写入跳过原因。
const (
	BatchSkipInvalid   = "invalid"
	BatchSkipDuplicate = "duplicate"
)

// BatchSkip 批量写入中被跳过的一行。
type BatchSkip struct {
	Index                int
	Reason               string // invalid | duplicate
	Detail               string
	MatchedTransactionID string
	IsSoftDup            bool
}

// BatchResult 批量写入结果。
type BatchResult struct {
	Created []*store.Transaction
	Skipped []BatchSkip
	// FrozenCreated 落在冻结区的新建笔数（写入后置 frozen_write_flag=1）。
	FrozenCreated int
}

// CreateBatch 在单个写事务内批量创建交易（§4.8：每 200 行一个事务，由调用方分块）。
//
// 规则：
//   - 单行校验失败 / 落在冻结区且未允许 → 计入 Skipped(invalid)，不中断整批；
//   - external_id 命中（硬重复）→ 一律计入 Skipped(duplicate)（唯一索引决定，不可写入）；
//   - 内容重复（软重复）→ **照常写入**，由调用方 skip_soft_duplicates 在进批前决定是否跳过；
//   - 其余错误直接回滚整个块（调用方负责重试或标记 partial）。
//
// skipDuplicates 仅为签名兼容保留：external_id 命中始终跳过、内容重复始终写入。
//
// 必须在同一连接内复用 allocAndInsert / FindDuplicateTx，不得新开连接。
func (s *TxnService) CreateBatch(ctx context.Context, items []*CreateTxnInput,
	skipDuplicates bool) (*BatchResult, error) {
	res := &BatchResult{Created: make([]*store.Transaction, 0, len(items)), Skipped: []BatchSkip{}}
	if len(items) == 0 {
		return res, nil
	}
	now := time.Now()
	boundary := s.balance.FreezeBoundary(now.Unix())
	lastFrozen, lerr := s.balance.LastFrozenAt()
	if lerr != nil {
		return nil, lerr
	}
	loc := timeutil.Local()

	err := s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		frozenHit := false
		for i, in := range items {
			if in == nil {
				res.Skipped = append(res.Skipped, BatchSkip{Index: i, Reason: BatchSkipInvalid, Detail: "空行"})
				continue
			}
			if verr := s.validate(in); verr != nil {
				res.Skipped = append(res.Skipped, BatchSkip{Index: i, Reason: BatchSkipInvalid, Detail: verr.Error()})
				continue
			}
			txTime := now.Unix()
			if in.TransactionTime != nil {
				txTime = *in.TransactionTime
			}
			rowConfirmed := in.FreezeConfirmed == nil || *in.FreezeConfirmed
			frozen := rowConfirmed && txTime < boundary
			if frozen && !in.AllowFrozen {
				res.Skipped = append(res.Skipped, BatchSkip{Index: i, Reason: BatchSkipInvalid,
					Detail: "该行交易时间落在冻结区（需允许写入冻结区）"})
				continue
			}
			source := in.Source
			if source == "" {
				source = "import"
			}
			row := &store.Transaction{
				TransactionType: in.TransactionType, AmountCents: in.AmountCents,
				Currency: in.Currency, AccountID: in.AccountID,
				CategoryID: in.CategoryID, SubCategoryID: in.SubCategoryID,
				MerchantID: in.MerchantID, MerchantText: in.MerchantText,
				RegionID: in.RegionID, CounterpartyName: in.CounterpartyName,
				CounterpartyAccount: in.CounterpartyAccount,
				Remark:              in.Remark, Source: source, ExternalID: in.ExternalID,
				TransactionTime: txTime, LocalDate: timeutil.LocalDate(txTime),
				TimePrecision:   NormalizeTimePrecision(in.TimePrecision),
				FreezeConfirmed: boolToInt(rowConfirmed),
				FreezeSpecified: true,
				ImportBatchID:   in.ImportBatchID,
			}
			// 只查 external_id —— 它是**硬重复**：ux_tx_external 唯一索引不允许写入同一外部行，
			// 无论 skipDuplicates 取值都只能跳过（否则整块违反唯一约束而回滚）。
			// 注意顺序：不能再用 FindDuplicateTx 的"内容 + external_id"组合规则来判重，
			// 因为内容规则排在前面会先命中，导致 external_id 重复的行被当成软重复写入而撞索引。
			if in.ExternalID != "" {
				exists, eerr := s.tx.ExistsExternalIDTx(tx, in.ExternalID)
				if eerr != nil {
					return eerr
				}
				if exists {
					res.Skipped = append(res.Skipped, BatchSkip{Index: i, Reason: BatchSkipDuplicate,
						Detail: "同批次/历史已导入同一行（external_id 相同）"})
					continue
				}
			}
			// 内容重复（时间/金额/账户/商户一致）是**软重复**（§4.6 L2）：只提示不拦截。
			// 是否跳过由调用方（导入服务 skip_soft_duplicates）在进批之前决定，这里绝不静默丢弃。
			if err := allocAndInsert(tx, s.tx, row, loc); err != nil {
				return err
			}
			if len(in.TagIDs) > 0 {
				if err := s.tx.ReplaceTagsTx(tx, row.ID, in.TagIDs); err != nil {
					return err
				}
			}
			if frozen {
				frozenHit = true
				res.FrozenCreated++
			}
			// I3 防御：待确认行落到物化区之前 → 同样需要整表重算
			if !rowConfirmed && txTime < lastFrozen {
				frozenHit = true
			}
			res.Created = append(res.Created, row)
		}
		if frozenHit {
			return s.meta.SetTx(tx, "frozen_write_flag", "1")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// boolToInt 冻结状态 → 列值（1 已冻结 / 0 待确认）。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
