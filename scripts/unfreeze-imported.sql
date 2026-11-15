-- ============================================================================
-- 可选脚本：把「v1.2 已导入、且早于冻结边界」的历史行改为「待确认冻结」
-- （ARCHITECTURE-V12 §9.9）
--
-- 为什么是「可选」：迁移无法得知用户是否已经核对过这批数据，批量解冻是**用户主动**
-- 的决定，不该由升级替用户做。默认全部保持 freeze_confirmed=1（只读），与升级前一致。
--
-- 用法（服务停止后执行，执行前后各做一次备份）：
--   sqlite3 data/ledger.db < scripts/unfreeze-imported.sql
--
-- 反向恢复（想立刻恢复只读）：
--   UPDATE transactions SET freeze_confirmed = 1 WHERE source = 'import' AND freeze_confirmed = 0;
--   UPDATE meta SET value = '1' WHERE key = 'frozen_write_flag';
-- ============================================================================

-- 0) 先把边界算出来（freeze_days 默认 30），并确保三列存在（否则请先启动一次 v1.2）
--    这里用 strftime 取「now - freeze_days 天」，与应用内 FreezeBoundary 同口径（UTC 秒）。
CREATE TEMP TABLE IF NOT EXISTS _v12_cfg AS
SELECT
  CAST(COALESCE((SELECT value FROM meta WHERE key = 'freeze_days'), '30') AS INTEGER) AS freeze_days,
  CAST(strftime('%s', 'now') AS INTEGER) AS now_unix;

-- 1) 解冻：仅「导入来源 + 早于冻结边界 + 未删除」的行
UPDATE transactions
   SET freeze_confirmed = 0,
       freeze_confirmed_at = NULL,
       -- I3：待确认行不得带物化值（否则 verify.stale_pending > 0、余额口径错乱）
       account_balance_after = NULL,
       total_balance_after = NULL,
       materialized_at = NULL,
       updated_at = CAST(strftime('%s', 'now') AS INTEGER)
 WHERE source = 'import'
   AND deleted_at IS NULL
   AND freeze_confirmed = 1
   AND transaction_time
       < (SELECT now_unix - freeze_days * 86400 FROM _v12_cfg);

-- 2) 让下次查询/启动做一次整表重算（物化边界会被钳制到最早待确认行）
UPDATE meta SET value = '1', updated_at = CAST(strftime('%s', 'now') AS INTEGER)
 WHERE key = 'frozen_write_flag';

-- 3) 结果核对：pending = 解冻行数；随后启动服务访问一次任意余额接口即完成重算
SELECT
  (SELECT COUNT(1) FROM transactions WHERE freeze_confirmed = 0 AND deleted_at IS NULL) AS pending_rows,
  (SELECT COUNT(1) FROM transactions WHERE freeze_confirmed = 0
      AND account_balance_after IS NOT NULL) AS stale_pending_should_be_zero;

DROP TABLE IF EXISTS _v12_cfg;
