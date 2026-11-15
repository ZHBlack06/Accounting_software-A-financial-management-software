package service

import (
	"context"
	"database/sql"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// AdjustInput 调整分录输入（§5.4 端点 42）。
type AdjustInput struct {
	Mode             string  // adjust | reverse
	NewAmountCents   int64   // 新金额（分，正数）
	NewCategoryID    *string // 为空沿用原值
	NewSubCategoryID *string
	NewMerchantID    *string
	NewMerchantText  *string
	Remark           string // 必填
}

// AdjustResult 调整结果。
type AdjustResult struct {
	Created    []*store.Transaction
	DeltaCents int64 // new_amount - old_amount（原交易口径）
}

// AdjustService 调整分录 / 反冲重录（§6.7）。
type AdjustService struct {
	s      *store.Store
	meta   *store.MetaRepo
	tx     *store.TxRepo
	shared *txnInserter
}

// NewAdjustService 构造。
func NewAdjustService(s *store.Store, meta *store.MetaRepo, txr *store.TxRepo) *AdjustService {
	return &AdjustService{s: s, meta: meta, tx: txr, shared: &txnInserter{txr: txr}}
}

// txnInserter 复用 allocAndInsert（与 TxnService 同一事务写入路径）。
type txnInserter struct{ txr *store.TxRepo }

func (h *txnInserter) insert(tx *sql.Tx, t *store.Transaction) error {
	return allocAndInsert(tx, h.txr, t, timeutil.Local())
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Adjust 对任意交易做调整（冻结区修正闭环；动态区同样允许，语义一致）。
// 禁止链式调整（is_adjustment=1 的交易不能再被调整）；remark 必填；时间强制 now。
func (s *AdjustService) Adjust(ctx context.Context, id string, in *AdjustInput) (*AdjustResult, error) {
	old, err := s.tx.GetByID(id)
	if err != nil {
		return nil, err
	}
	if old == nil || old.DeletedAt != nil {
		return nil, apierr.New(apierr.CodeNotFound, "交易不存在或已删除")
	}
	if old.IsAdjustment == 1 {
		return nil, apierr.New(apierr.CodeValidation, "调整项不可再次调整（禁止链式调整）")
	}
	if in.Remark == "" {
		return nil, apierr.New(apierr.CodeValidation, "remark 必填（说明调整原因）")
	}
	if in.Mode != "adjust" && in.Mode != "reverse" {
		return nil, apierr.New(apierr.CodeValidation, "mode 必须为 adjust|reverse")
	}
	if in.NewAmountCents <= 0 {
		return nil, apierr.New(apierr.CodeValidation, "new_amount_cents 必须为正整数（分）")
	}
	if old.TransactionType == "transfer" {
		if in.Mode != "adjust" {
			return nil, apierr.New(apierr.CodeValidation, "转账调整仅支持 adjust 模式")
		}
		return s.adjustTransfer(ctx, old, in)
	}
	if in.NewAmountCents == old.AmountCents {
		return nil, apierr.New(apierr.CodeNoEffect, "金额未变化")
	}
	if in.Mode == "adjust" {
		return s.adjustSingle(ctx, old, in)
	}
	return s.reverseRecast(ctx, old, in)
}

// pickStr 新值优先，否则回退原值。
func pickStr(newV, oldV *string) *string {
	if newV != nil {
		return newV
	}
	return oldV
}

// baseAdjustment 调整行公共字段。
func baseAdjustment(old *store.Transaction, typ string, amount int64, adjType, remark string, nowUnix int64) *store.Transaction {
	return &store.Transaction{
		TransactionType:      typ,
		AmountCents:          amount,
		Currency:             old.Currency,
		FxRate:               1,
		AccountID:            old.AccountID,
		TransactionTime:      nowUnix,
		LocalDate:            timeutil.LocalDate(nowUnix),
		IsAdjustment:         1,
		AdjustmentType:       &adjType,
		RelatedTransactionID: &old.ID,
		Source:               "adjustment",
		Remark:               remark,
		// 调整分录是「用户已经在场确认过」的写入，永远属于已冻结（§9.3），
		// 绝不能落成「待确认冻结」——否则列表会挂错标签、verify.pending_rows 被虚增。
		FreezeConfirmed: 1,
		FreezeSpecified: true,
	}
}

// adjustSingle mode=adjust：只补差额一笔。
func (s *AdjustService) adjustSingle(ctx context.Context, old *store.Transaction, in *AdjustInput) (*AdjustResult, error) {
	delta := in.NewAmountCents - old.AmountCents // 正=原记少了
	var typ string
	switch {
	case old.TransactionType == "expense":
		if delta > 0 {
			typ = "expense" // 少记支出 → 补支出
		} else {
			typ = "income" // 多记支出 → 冲收入
		}
	case old.TransactionType == "income":
		if delta > 0 {
			typ = "income"
		} else {
			typ = "expense"
		}
	}
	nowUnix := timeutil.NowUnix()
	t := baseAdjustment(old, typ, abs64(delta), "adjust", in.Remark, nowUnix)
	t.CategoryID = pickStr(in.NewCategoryID, old.CategoryID)
	t.SubCategoryID = pickStr(in.NewSubCategoryID, old.SubCategoryID)
	t.MerchantID = old.MerchantID
	t.MerchantText = old.MerchantText
	if in.NewMerchantText != nil {
		t.MerchantText = *in.NewMerchantText
	}
	err := s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		return s.shared.insert(tx, t)
	})
	if err != nil {
		return nil, err
	}
	return &AdjustResult{Created: []*store.Transaction{t}, DeltaCents: delta}, nil
}

// reverseRecast mode=reverse：①全额冲销（反向同额）②按新值重录。
func (s *AdjustService) reverseRecast(ctx context.Context, old *store.Transaction, in *AdjustInput) (*AdjustResult, error) {
	nowUnix := timeutil.NowUnix()
	inverted := "expense"
	if old.TransactionType == "expense" {
		inverted = "income"
	}
	// ① 冲销：与原交易完全相反方向、同金额
	revert := baseAdjustment(old, inverted, old.AmountCents, "reverse", "冲销："+old.Remark, nowUnix)
	revert.CategoryID = old.CategoryID
	revert.SubCategoryID = old.SubCategoryID
	revert.MerchantID = old.MerchantID
	revert.MerchantText = old.MerchantText
	// ② 重录：按新值、原方向
	recast := baseAdjustment(old, old.TransactionType, in.NewAmountCents, "reverse", in.Remark, nowUnix)
	recast.CategoryID = pickStr(in.NewCategoryID, old.CategoryID)
	recast.SubCategoryID = pickStr(in.NewSubCategoryID, old.SubCategoryID)
	recast.MerchantID = pickStr(in.NewMerchantID, old.MerchantID)
	recast.MerchantText = old.MerchantText
	if in.NewMerchantText != nil {
		recast.MerchantText = *in.NewMerchantText
	}
	err := s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.shared.insert(tx, revert); err != nil {
			return err
		}
		return s.shared.insert(tx, recast)
	})
	if err != nil {
		return nil, err
	}
	return &AdjustResult{
		Created:    []*store.Transaction{revert, recast},
		DeltaCents: in.NewAmountCents - old.AmountCents,
	}, nil
}

// adjustTransfer 内部转账双边调整（净 0，不污染收支统计）。
func (s *AdjustService) adjustTransfer(ctx context.Context, old *store.Transaction, in *AdjustInput) (*AdjustResult, error) {
	if old.TransferGroupID == nil {
		return nil, apierr.New(apierr.CodeInternal, "转账组缺失")
	}
	group, err := s.tx.GetByTransferGroup(*old.TransferGroupID)
	if err != nil {
		return nil, err
	}
	var out, inRow *store.Transaction
	for _, g := range group {
		if g.TransferDirection != nil && *g.TransferDirection == "out" {
			out = g
		} else {
			inRow = g
		}
	}
	if out == nil || inRow == nil {
		return nil, apierr.New(apierr.CodeInternal, "转账组不完整")
	}
	delta := in.NewAmountCents - out.AmountCents // 正=原转少了
	if delta == 0 {
		return nil, apierr.New(apierr.CodeNoEffect, "金额未变化")
	}
	amt := abs64(delta)
	nowUnix := timeutil.NowUnix()
	gid := ids.ShortID("tg_")
	// delta>0：转出账户补扣(out)、转入账户补收(in)；delta<0：转出账户补回(in)、转入账户扣回(out)
	dirA, dirB := "out", "in"
	if delta < 0 {
		dirA, dirB = "in", "out"
	}
	a := baseAdjustment(old, "transfer", amt, "adjust", in.Remark, nowUnix)
	a.AccountID = out.AccountID
	a.TransferDirection = &dirA
	a.TransferGroupID = &gid
	a.CounterAccountID = &inRow.AccountID
	b := baseAdjustment(old, "transfer", amt, "adjust", in.Remark, nowUnix)
	b.AccountID = inRow.AccountID
	b.TransferDirection = &dirB
	b.TransferGroupID = &gid
	b.CounterAccountID = &out.AccountID
	err = s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.shared.insert(tx, a); err != nil {
			return err
		}
		return s.shared.insert(tx, b)
	})
	if err != nil {
		return nil, err
	}
	return &AdjustResult{Created: []*store.Transaction{a, b}, DeltaCents: delta}, nil
}
