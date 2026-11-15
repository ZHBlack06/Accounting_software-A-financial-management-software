package store

// GORM 模型，字段与 docs/ARCHITECTURE.md §3.2 DDL 一一对应（§4.1）。
// int64 表 UTC 秒时间戳；*string / *int64 表可空列。

// Meta 键值表。
type Meta struct {
	Key       string `gorm:"column:key;primaryKey"`
	Value     string `gorm:"column:value"`
	UpdatedAt int64  `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (Meta) TableName() string { return "meta" }

// Account 账户。
type Account struct {
	ID                  string `gorm:"column:id;primaryKey"`
	Name                string `gorm:"column:name"`
	Alias               string `gorm:"column:alias"`
	AccountType         string `gorm:"column:account_type"`
	ValueType           string `gorm:"column:value_type"`
	BankName            string `gorm:"column:bank_name"`
	CardTail            string `gorm:"column:card_tail"`
	Currency            string `gorm:"column:currency"`
	InitialBalanceCents int64  `gorm:"column:initial_balance_cents"`
	InitialBalanceTime  int64  `gorm:"column:initial_balance_time"`
	IsCountInTotal      int    `gorm:"column:is_count_in_total"`
	SortOrder           int    `gorm:"column:sort_order"`
	IsArchived          int    `gorm:"column:is_archived"`
	ArchivedAt          *int64 `gorm:"column:archived_at"`
	Remark              string `gorm:"column:remark"`
	CreatedAt           int64  `gorm:"column:created_at"`
	UpdatedAt           int64  `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (Account) TableName() string { return "accounts" }

// Category 分类（两级）。
type Category struct {
	ID              string  `gorm:"column:id;primaryKey"`
	Name            string  `gorm:"column:name"`
	ParentID        *string `gorm:"column:parent_id"`
	Level           int     `gorm:"column:level"`
	TransactionType string  `gorm:"column:transaction_type"`
	IsSystem        int     `gorm:"column:is_system"`
	IsArchived      int     `gorm:"column:is_archived"`
	Color           string  `gorm:"column:color"`
	Icon            string  `gorm:"column:icon"`
	SortOrder       int     `gorm:"column:sort_order"`
	CreatedAt       int64   `gorm:"column:created_at"`
	UpdatedAt       int64   `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (Category) TableName() string { return "categories" }

// Tag 标签。
type Tag struct {
	ID         string `gorm:"column:id;primaryKey"`
	Name       string `gorm:"column:name"`
	Color      string `gorm:"column:color"`
	SortOrder  int    `gorm:"column:sort_order"`
	UseCount   int    `gorm:"column:use_count"`
	IsArchived int    `gorm:"column:is_archived"`
	CreatedAt  int64  `gorm:"column:created_at"`
	UpdatedAt  int64  `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (Tag) TableName() string { return "tags" }

// TransactionTag 交易-标签关联。
type TransactionTag struct {
	TransactionID string `gorm:"column:transaction_id;primaryKey"`
	TagID         string `gorm:"column:tag_id;primaryKey"`
}

// TableName 指定表名。
func (TransactionTag) TableName() string { return "transaction_tags" }

// Merchant 商户/平台。
type Merchant struct {
	ID           string  `gorm:"column:id;primaryKey"`
	Name         string  `gorm:"column:name"`
	Alias        string  `gorm:"column:alias"`
	MerchantType string  `gorm:"column:merchant_type"`
	PlatformCode string  `gorm:"column:platform_code"`
	RegionID     *string `gorm:"column:region_id"`
	UseCount     int     `gorm:"column:use_count"`
	IsSystem     int     `gorm:"column:is_system"`
	IsArchived   int     `gorm:"column:is_archived"`
	CreatedAt    int64   `gorm:"column:created_at"`
	UpdatedAt    int64   `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (Merchant) TableName() string { return "merchants" }

// Region 行政区划。
type Region struct {
	Code       string `gorm:"column:code;primaryKey" json:"code"`
	Name       string `gorm:"column:name" json:"name"`
	FullName   string `gorm:"column:full_name" json:"full_name"`
	ParentCode string `gorm:"column:parent_code" json:"parent_code"`
	Level      int    `gorm:"column:level" json:"level"`
	Pinyin     string `gorm:"column:pinyin" json:"pinyin"`
	PinyinAbbr string `gorm:"column:pinyin_abbr" json:"pinyin_abbr"`
}

// TableName 指定表名。
func (Region) TableName() string { return "regions" }

// Ledger 账本。
type Ledger struct {
	ID         string `gorm:"column:id;primaryKey"`
	Name       string `gorm:"column:name"`
	Currency   string `gorm:"column:currency"`
	Remark     string `gorm:"column:remark"`
	IsDefault  int    `gorm:"column:is_default"`
	SortOrder  int    `gorm:"column:sort_order"`
	IsArchived int    `gorm:"column:is_archived"`
	ArchivedAt *int64 `gorm:"column:archived_at"`
	CreatedAt  int64  `gorm:"column:created_at"`
	UpdatedAt  int64  `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (Ledger) TableName() string { return "ledgers" }

// LedgerRule 账本绑定规则。
type LedgerRule struct {
	ID        string `gorm:"column:id;primaryKey"`
	LedgerID  string `gorm:"column:ledger_id"`
	MatchMode string `gorm:"column:match_mode"`
	IsActive  int    `gorm:"column:is_active"`
	CreatedAt int64  `gorm:"column:created_at"`
	UpdatedAt int64  `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (LedgerRule) TableName() string { return "ledger_rules" }

// LedgerRuleTarget 规则目标。
type LedgerRuleTarget struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	RuleID     string `gorm:"column:rule_id"`
	TargetType string `gorm:"column:target_type"`
	TargetID   string `gorm:"column:target_id"`
}

// TableName 指定表名。
func (LedgerRuleTarget) TableName() string { return "ledger_rule_targets" }

// Transaction 交易（唯一真相源）。
type Transaction struct {
	ID                   string  `gorm:"column:id;primaryKey"`
	CreatedSeq           int64   `gorm:"column:created_seq;uniqueIndex"`
	TransactionTime      int64   `gorm:"column:transaction_time;index"`
	LocalDate            string  `gorm:"column:local_date;index"`
	TransactionType      string  `gorm:"column:transaction_type"`
	AmountCents          int64   `gorm:"column:amount_cents"`
	Currency             string  `gorm:"column:currency"`
	FxRate               float64 `gorm:"column:fx_rate"`
	AccountID            string  `gorm:"column:account_id;index"`
	TransferDirection    *string `gorm:"column:transfer_direction"`
	TransferGroupID      *string `gorm:"column:transfer_group_id;index"`
	CounterAccountID     *string `gorm:"column:counter_account_id"`
	CategoryID           *string `gorm:"column:category_id;index"`
	SubCategoryID        *string `gorm:"column:sub_category_id;index"`
	MerchantID           *string `gorm:"column:merchant_id;index"`
	MerchantText         string  `gorm:"column:merchant_text"`
	RegionID             *string `gorm:"column:region_id"`
	CounterpartyName     string  `gorm:"column:counterparty_name"`
	CounterpartyAccount  string  `gorm:"column:counterparty_account"`
	Remark               string  `gorm:"column:remark"`
	OCRRawText           string  `gorm:"column:ocr_raw_text"`
	ScreenshotID         string  `gorm:"column:screenshot_id"`
	IsAdjustment         int     `gorm:"column:is_adjustment"`
	AdjustmentType       *string `gorm:"column:adjustment_type"`
	RelatedTransactionID *string `gorm:"column:related_transaction_id"`
	Source               string  `gorm:"column:source"`
	ExternalID           string  `gorm:"column:external_id"`
	AccountBalanceAfter  *int64  `gorm:"column:account_balance_after"`
	TotalBalanceAfter    *int64  `gorm:"column:total_balance_after"`
	MaterializedAt       *int64  `gorm:"column:materialized_at"`
	DeletedAt            *int64  `gorm:"column:deleted_at;index"`
	CreatedAt            int64   `gorm:"column:created_at"`
	UpdatedAt            int64   `gorm:"column:updated_at"`
	// TimePrecision v1.2：'second' | 'day'。日期型源导入的行由系统分布时间，标记为 day。
	TimePrecision string `gorm:"column:time_precision"`
	// FreezeConfirmed v1.2 §9：1 已冻结（受写保护）/ 0 待确认（可写、不物化）。默认 1。
	FreezeConfirmed int `gorm:"column:freeze_confirmed"`
	// FreezeSpecified 标记 FreezeConfirmed 已被调用方**显式**赋值（0 也是有效值）。
	// 不落库：仅用于消除 Go 零值歧义 —— 直接构造 store.Transaction 而未赋该字段的路径
	// （调整分录、未来的新写入路径）不会被误判成「待确认」，而是按历史语义写 1。
	FreezeSpecified bool `gorm:"-"`
	// FreezeConfirmedAt 确认动作时刻（UTC 秒）；NULL 表示从未经历确认动作。
	FreezeConfirmedAt *int64 `gorm:"column:freeze_confirmed_at"`
	// ImportBatchID 产生该行的导入批次（imp_xxx）；手工录入为空。
	ImportBatchID string `gorm:"column:import_batch_id"`
}

// TableName 指定表名。
func (Transaction) TableName() string { return "transactions" }

// SignedCents 返回带符号金额（与 v_transactions_signed 口径一致）。
func (t *Transaction) SignedCents() int64 {
	switch {
	case t.TransactionType == "expense":
		return -t.AmountCents
	case t.TransactionType == "income":
		return t.AmountCents
	case t.TransferDirection != nil && *t.TransferDirection == "out":
		return -t.AmountCents
	case t.TransferDirection != nil && *t.TransferDirection == "in":
		return t.AmountCents
	}
	return 0
}

// TransactionItem 多物品明细（discount_cents 为生成列，写入时必须 Omit）。
type TransactionItem struct {
	ID                  int64   `gorm:"column:id;primaryKey;autoIncrement"`
	TransactionID       string  `gorm:"column:transaction_id"`
	Name                string  `gorm:"column:name"`
	SubCategoryID       *string `gorm:"column:sub_category_id"`
	OriginalAmountCents int64   `gorm:"column:original_amount_cents"`
	PaidAmountCents     int64   `gorm:"column:paid_amount_cents"`
	DiscountCents       int64   `gorm:"column:discount_cents;->"` // 只读（生成列）
	DiscountReason      string  `gorm:"column:discount_reason"`
	SortOrder           int     `gorm:"column:sort_order"`
	CreatedAt           int64   `gorm:"column:created_at"`
}

// TableName 指定表名。
func (TransactionItem) TableName() string { return "transaction_items" }

// Screenshot 截图与 OCR 记录。
type Screenshot struct {
	ID             string  `gorm:"column:id;primaryKey"`
	TransactionID  *string `gorm:"column:transaction_id"`
	OriginalPath   string  `gorm:"column:original_path"`
	CompressedPath string  `gorm:"column:compressed_path"`
	OCRResultPath  string  `gorm:"column:ocr_result_path"`
	OCRStatus      string  `gorm:"column:ocr_status"`
	OCRSource      string  `gorm:"column:ocr_source"`
	OCRModel       string  `gorm:"column:ocr_model"`
	OCRElapsedMs   int64   `gorm:"column:ocr_elapsed_ms"`
	NeedReview     int     `gorm:"column:need_review"`
	FileSize       int64   `gorm:"column:file_size"`
	CreatedAt      int64   `gorm:"column:created_at"`
	UpdatedAt      int64   `gorm:"column:updated_at"`
	DeletedAt      *int64  `gorm:"column:deleted_at"`
}

// TableName 指定表名。
func (Screenshot) TableName() string { return "screenshots" }

// ImportBatch 导入批次审计（v1.2，见 ARCHITECTURE-V12 §3.1/§3.3）。
// 只存批次级信息；文件/工作表级元数据在 data/tmp/import/<id>/manifest.json。
type ImportBatch struct {
	ID           string `gorm:"column:id;primaryKey"`
	Kind         string `gorm:"column:kind"`
	Status       string `gorm:"column:status"`
	FileCount    int    `gorm:"column:file_count"`
	SheetCount   int    `gorm:"column:sheet_count"`
	TotalRows    int64  `gorm:"column:total_rows"`
	CreatedRows  int64  `gorm:"column:created_rows"`
	SkippedRows  int64  `gorm:"column:skipped_rows"`
	ErrorRows    int64  `gorm:"column:error_rows"`
	Encoding     string `gorm:"column:encoding"`
	ManifestPath string `gorm:"column:manifest_path"`
	CreatedAt    int64  `gorm:"column:created_at"`
	CommittedAt  *int64 `gorm:"column:committed_at"`
	UpdatedAt    int64  `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (ImportBatch) TableName() string { return "import_batches" }
