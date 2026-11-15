-- ============ 元数据 / 计数器 ============
CREATE TABLE IF NOT EXISTS meta (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);

-- ============ 账户 ============
CREATE TABLE IF NOT EXISTS accounts (
  id                    TEXT PRIMARY KEY,
  name                  TEXT NOT NULL,
  alias                 TEXT NOT NULL DEFAULT '',
  account_type          TEXT NOT NULL DEFAULT 'asset'
                          CHECK (account_type IN ('asset')),
  value_type            TEXT NOT NULL DEFAULT 'wallet'
                          CHECK (value_type IN ('wallet','wealth','yuebao','current','fixed','other')),
  bank_name             TEXT NOT NULL DEFAULT '',
  card_tail             TEXT NOT NULL DEFAULT ''
                          CHECK (card_tail = '' OR length(card_tail) = 4),
  currency              TEXT NOT NULL DEFAULT 'CNY',
  initial_balance_cents INTEGER NOT NULL DEFAULT 0,
  initial_balance_time  INTEGER NOT NULL DEFAULT 0,
  is_count_in_total     INTEGER NOT NULL DEFAULT 1 CHECK (is_count_in_total IN (0,1)),
  sort_order            INTEGER NOT NULL DEFAULT 0,
  is_archived           INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  archived_at           INTEGER,
  remark                TEXT NOT NULL DEFAULT '',
  created_at            INTEGER NOT NULL,
  updated_at            INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_accounts_sort ON accounts(is_archived, sort_order);

-- ============ 分类（两级） ============
CREATE TABLE IF NOT EXISTS categories (
  id               TEXT PRIMARY KEY,
  name             TEXT NOT NULL,
  parent_id        TEXT REFERENCES categories(id) ON DELETE RESTRICT,
  level            INTEGER NOT NULL DEFAULT 1 CHECK (level IN (1,2)),
  transaction_type TEXT NOT NULL DEFAULT 'expense'
                     CHECK (transaction_type IN ('expense','income','both')),
  is_system        INTEGER NOT NULL DEFAULT 0,
  is_archived      INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  color            TEXT NOT NULL DEFAULT '',
  icon             TEXT NOT NULL DEFAULT '',
  sort_order       INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_categories_name
  ON categories(IFNULL(parent_id,''), name);
CREATE INDEX IF NOT EXISTS ix_categories_parent ON categories(parent_id, sort_order);

-- ============ 标签 ============
CREATE TABLE IF NOT EXISTS tags (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  color       TEXT NOT NULL DEFAULT '',
  sort_order  INTEGER NOT NULL DEFAULT 0,
  use_count   INTEGER NOT NULL DEFAULT 0,
  is_archived INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS transaction_tags (
  transaction_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
  tag_id         TEXT NOT NULL REFERENCES tags(id) ON DELETE RESTRICT,
  PRIMARY KEY (transaction_id, tag_id)
);
CREATE INDEX IF NOT EXISTS ix_tt_tag ON transaction_tags(tag_id, transaction_id);

-- ============ 商户 / 平台 ============
CREATE TABLE IF NOT EXISTS merchants (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,
  alias         TEXT NOT NULL DEFAULT '',
  merchant_type TEXT NOT NULL DEFAULT 'offline'
                  CHECK (merchant_type IN ('offline','online')),
  platform_code TEXT NOT NULL DEFAULT '',
  region_id     TEXT,
  use_count     INTEGER NOT NULL DEFAULT 0,
  is_system     INTEGER NOT NULL DEFAULT 0,
  is_archived   INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_merchants_type ON merchants(merchant_type, is_archived, name);

-- ============ 地区（县级以上行政区划字典） ============
CREATE TABLE IF NOT EXISTS regions (
  code        TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  full_name   TEXT NOT NULL,
  parent_code TEXT NOT NULL DEFAULT '',
  level       INTEGER NOT NULL,
  pinyin      TEXT NOT NULL DEFAULT '',
  pinyin_abbr TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS ix_regions_parent ON regions(parent_code);
CREATE INDEX IF NOT EXISTS ix_regions_pinyin ON regions(pinyin, pinyin_abbr);

-- ============ 账本 + 绑定规则 ============
CREATE TABLE IF NOT EXISTS ledgers (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  currency    TEXT NOT NULL DEFAULT 'CNY',
  remark      TEXT NOT NULL DEFAULT '',
  is_default  INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0,1)),
  sort_order  INTEGER NOT NULL DEFAULT 0,
  is_archived INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  archived_at INTEGER,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ledger_rules (
  id         TEXT PRIMARY KEY,
  ledger_id  TEXT NOT NULL REFERENCES ledgers(id) ON DELETE CASCADE,
  match_mode TEXT NOT NULL DEFAULT 'or' CHECK (match_mode IN ('or','all')),
  is_active  INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0,1)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_rules_ledger ON ledger_rules(ledger_id);

CREATE TABLE IF NOT EXISTS ledger_rule_targets (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id     TEXT NOT NULL REFERENCES ledger_rules(id) ON DELETE CASCADE,
  target_type TEXT NOT NULL
                CHECK (target_type IN ('category_l1','category_l2','tag','account','merchant')),
  target_id   TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_lrt
  ON ledger_rule_targets(rule_id, target_type, target_id);
CREATE INDEX IF NOT EXISTS ix_lrt_target ON ledger_rule_targets(target_type, target_id);

-- ============ 交易总表（唯一真相源） ============
CREATE TABLE IF NOT EXISTS transactions (
  id                     TEXT PRIMARY KEY CHECK (length(id) = 19),
  created_seq            INTEGER NOT NULL UNIQUE,
  transaction_time       INTEGER NOT NULL,
  local_date             TEXT NOT NULL,
  transaction_type       TEXT NOT NULL
                           CHECK (transaction_type IN ('expense','income','transfer')),
  amount_cents           INTEGER NOT NULL CHECK (amount_cents > 0),
  currency               TEXT NOT NULL DEFAULT 'CNY',
  fx_rate                REAL NOT NULL DEFAULT 1,
  account_id             TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
  transfer_direction     TEXT CHECK (transfer_direction IN ('out','in')),
  transfer_group_id      TEXT,
  counter_account_id     TEXT REFERENCES accounts(id) ON DELETE RESTRICT,
  category_id            TEXT REFERENCES categories(id) ON DELETE RESTRICT,
  sub_category_id        TEXT REFERENCES categories(id) ON DELETE RESTRICT,
  merchant_id            TEXT REFERENCES merchants(id) ON DELETE SET NULL,
  merchant_text          TEXT NOT NULL DEFAULT '',
  region_id              TEXT REFERENCES regions(code) ON DELETE SET NULL,
  counterparty_name      TEXT NOT NULL DEFAULT '',
  counterparty_account   TEXT NOT NULL DEFAULT '',
  remark                 TEXT NOT NULL DEFAULT '',
  ocr_raw_text           TEXT NOT NULL DEFAULT '',
  screenshot_id          TEXT NOT NULL DEFAULT '',
  is_adjustment          INTEGER NOT NULL DEFAULT 0 CHECK (is_adjustment IN (0,1)),
  adjustment_type        TEXT CHECK (adjustment_type IN ('adjust','reverse')),
  related_transaction_id TEXT,
  source                 TEXT NOT NULL DEFAULT 'manual'
                           CHECK (source IN ('manual','csv','ocr','adjustment','import')),
  external_id            TEXT NOT NULL DEFAULT '',
  account_balance_after  INTEGER,
  total_balance_after    INTEGER,
  materialized_at        INTEGER,
  deleted_at             INTEGER,
  created_at             INTEGER NOT NULL,
  updated_at             INTEGER NOT NULL,
  -- v1.2：行级时间精度（'second' | 'day'）。日期型源导入的行"时间"由系统分布，
  -- 后续编辑/重算/导出需要知道它本来没有时分秒（ARCHITECTURE-V12 §3.1）。
  -- 注意：schema.sql 与 migrate.go 的 v12Columns 必须保持一致。
  time_precision         TEXT NOT NULL DEFAULT 'second'
                           CHECK (time_precision IN ('second','day')),
  -- v1.2 §9：延迟冻结（R6）。freeze_confirmed 默认 1 → 既有行/手工行语义完全不变；
  -- 只有「导入且早于冻结边界、用户尚未确认」的行显式为 0（可写、不物化）。
  freeze_confirmed       INTEGER NOT NULL DEFAULT 1 CHECK (freeze_confirmed IN (0,1)),
  freeze_confirmed_at    INTEGER,
  import_batch_id        TEXT NOT NULL DEFAULT '',
  CHECK (transaction_type <> 'transfer'
         OR (transfer_group_id IS NOT NULL AND transfer_direction IS NOT NULL
             AND counter_account_id IS NOT NULL)),
  CHECK (is_adjustment = 0
         OR (adjustment_type IS NOT NULL AND related_transaction_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS ix_tx_order        ON transactions(transaction_time, created_seq);
CREATE INDEX IF NOT EXISTS ix_tx_account_time ON transactions(account_id, transaction_time, created_seq);
CREATE INDEX IF NOT EXISTS ix_tx_cat_time     ON transactions(category_id, transaction_time);
CREATE INDEX IF NOT EXISTS ix_tx_subcat_time  ON transactions(sub_category_id, transaction_time);
CREATE INDEX IF NOT EXISTS ix_tx_merchant     ON transactions(merchant_id, transaction_time);
CREATE INDEX IF NOT EXISTS ix_tx_region       ON transactions(region_id);
CREATE INDEX IF NOT EXISTS ix_tx_tgroup       ON transactions(transfer_group_id);
CREATE INDEX IF NOT EXISTS ix_tx_related      ON transactions(related_transaction_id);
CREATE INDEX IF NOT EXISTS ix_tx_adjust       ON transactions(is_adjustment, transaction_time);
CREATE INDEX IF NOT EXISTS ix_tx_deleted      ON transactions(deleted_at);
CREATE INDEX IF NOT EXISTS ix_tx_local_date   ON transactions(local_date);
CREATE INDEX IF NOT EXISTS ix_tx_source       ON transactions(source);
CREATE UNIQUE INDEX IF NOT EXISTS ux_tx_external
  ON transactions(external_id) WHERE external_id <> '';

-- v1.2 §9：待确认行的部分索引不写在这里 —— 它们依赖 v1.2 新增列，而 schema.sql 会先于
-- ensureV12Columns() 执行，老库（无该列）执行到这里会直接报 "no such column"。
-- 因此这两条索引统一放在 migrate.go 的 v9Indexes 常量里，补列之后每次都幂等执行。

-- ============ 多物品明细 ============
CREATE TABLE IF NOT EXISTS transaction_items (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  transaction_id        TEXT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
  name                  TEXT NOT NULL DEFAULT '',
  sub_category_id       TEXT REFERENCES categories(id) ON DELETE SET NULL,
  original_amount_cents INTEGER NOT NULL DEFAULT 0 CHECK (original_amount_cents >= 0),
  paid_amount_cents     INTEGER NOT NULL DEFAULT 0 CHECK (paid_amount_cents >= 0),
  discount_cents        INTEGER GENERATED ALWAYS AS
                          (original_amount_cents - paid_amount_cents) VIRTUAL,
  discount_reason       TEXT NOT NULL DEFAULT '',
  sort_order            INTEGER NOT NULL DEFAULT 0,
  created_at            INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_items_tx ON transaction_items(transaction_id, sort_order);

-- ============ 截图与 OCR ============
CREATE TABLE IF NOT EXISTS screenshots (
  id               TEXT PRIMARY KEY,
  transaction_id   TEXT,
  original_path    TEXT NOT NULL,
  compressed_path  TEXT NOT NULL DEFAULT '',
  ocr_result_path  TEXT NOT NULL DEFAULT '',
  ocr_status       TEXT NOT NULL DEFAULT 'pending'
                     CHECK (ocr_status IN ('pending','processing','done','failed','skipped')),
  ocr_source       TEXT NOT NULL DEFAULT 'pc_local',
  ocr_model        TEXT NOT NULL DEFAULT '',
  ocr_elapsed_ms   INTEGER NOT NULL DEFAULT 0,
  need_review      INTEGER NOT NULL DEFAULT 0 CHECK (need_review IN (0,1)),
  file_size        INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  deleted_at       INTEGER
);
CREATE INDEX IF NOT EXISTS ix_ss_status ON screenshots(ocr_status, created_at);
CREATE INDEX IF NOT EXISTS ix_ss_tx ON screenshots(transaction_id);

-- ============ 导入批次审计（v1.2，只存批次级，不存逐行） ============
-- 文件/工作表级元数据（sheet_name、header_row、mapping、每表统计）体积小且只在
-- 一次导入会话内有意义，放 data/tmp/import/<id>/manifest.json 随 tmp 24h 清理。
CREATE TABLE IF NOT EXISTS import_batches (
  id             TEXT PRIMARY KEY,                                -- 'imp_' + 8
  kind           TEXT NOT NULL DEFAULT 'sheet'
                   CHECK (kind IN ('sheet','csv_wechat','csv_alipay','ocr')),
  status         TEXT NOT NULL DEFAULT 'previewed'
                   CHECK (status IN ('previewed','committed','aborted','expired')),
  file_count     INTEGER NOT NULL DEFAULT 0,
  sheet_count    INTEGER NOT NULL DEFAULT 0,
  total_rows     INTEGER NOT NULL DEFAULT 0,
  created_rows   INTEGER NOT NULL DEFAULT 0,
  skipped_rows   INTEGER NOT NULL DEFAULT 0,
  error_rows     INTEGER NOT NULL DEFAULT 0,
  encoding       TEXT NOT NULL DEFAULT '',
  manifest_path  TEXT NOT NULL DEFAULT '',                        -- data/tmp/import/<id>/manifest.json
  created_at     INTEGER NOT NULL,
  committed_at   INTEGER,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_impb_status ON import_batches(status, created_at);

-- ============ 基础视图（所有余额与统计 SQL 的唯一入口） ============
DROP VIEW IF EXISTS v_transactions_signed;
CREATE VIEW v_transactions_signed AS
SELECT
  t.*,
  CASE
    WHEN t.transaction_type = 'expense'  THEN -t.amount_cents
    WHEN t.transaction_type = 'income'   THEN  t.amount_cents
    WHEN t.transfer_direction = 'out'    THEN -t.amount_cents
    WHEN t.transfer_direction = 'in'     THEN  t.amount_cents
    ELSE 0
  END AS signed_cents,
  CASE WHEN t.is_adjustment = 1 THEN
    CASE
      WHEN t.transaction_type = 'expense' THEN -t.amount_cents
      WHEN t.transaction_type = 'income'  THEN  t.amount_cents
      WHEN t.transfer_direction = 'out'   THEN -t.amount_cents
      WHEN t.transfer_direction = 'in'    THEN  t.amount_cents
      ELSE 0
    END
  ELSE 0 END AS adjustment_signed_cents,
  CASE WHEN t.transaction_type = 'expense' AND t.is_adjustment = 0
       THEN t.amount_cents ELSE 0 END AS stat_expense_cents,
  CASE WHEN t.transaction_type = 'income' AND t.is_adjustment = 0
       THEN t.amount_cents ELSE 0 END AS stat_income_cents
FROM transactions t
WHERE t.deleted_at IS NULL;

-- ============ 触发器：updated_at 自动维护 ============
CREATE TRIGGER IF NOT EXISTS trg_accounts_updated AFTER UPDATE ON accounts
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN UPDATE accounts SET updated_at = CAST(strftime('%s','now') AS INTEGER) WHERE id = NEW.id; END;

CREATE TRIGGER IF NOT EXISTS trg_categories_updated AFTER UPDATE ON categories
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN UPDATE categories SET updated_at = CAST(strftime('%s','now') AS INTEGER) WHERE id = NEW.id; END;

CREATE TRIGGER IF NOT EXISTS trg_tags_updated AFTER UPDATE ON tags
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN UPDATE tags SET updated_at = CAST(strftime('%s','now') AS INTEGER) WHERE id = NEW.id; END;

CREATE TRIGGER IF NOT EXISTS trg_merchants_updated AFTER UPDATE ON merchants
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN UPDATE merchants SET updated_at = CAST(strftime('%s','now') AS INTEGER) WHERE id = NEW.id; END;

CREATE TRIGGER IF NOT EXISTS trg_ledgers_updated AFTER UPDATE ON ledgers
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN UPDATE ledgers SET updated_at = CAST(strftime('%s','now') AS INTEGER) WHERE id = NEW.id; END;

-- ============ 触发器：标签使用次数 ============
CREATE TRIGGER IF NOT EXISTS trg_tt_ins AFTER INSERT ON transaction_tags
FOR EACH ROW BEGIN
  UPDATE tags SET use_count = use_count + 1 WHERE id = NEW.tag_id;
END;
CREATE TRIGGER IF NOT EXISTS trg_tt_del AFTER DELETE ON transaction_tags
FOR EACH ROW BEGIN
  UPDATE tags SET use_count = use_count - 1 WHERE id = OLD.tag_id AND use_count > 0;
END;

-- ============ 触发器：禁止物理删除已被引用的账户 ============
CREATE TRIGGER IF NOT EXISTS trg_accounts_no_delete BEFORE DELETE ON accounts
FOR EACH ROW
WHEN EXISTS (SELECT 1 FROM transactions WHERE account_id = OLD.id)
BEGIN SELECT RAISE(ABORT, 'ACCOUNT_IN_USE'); END;
