package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"ledger/internal/config"
	"ledger/internal/pkg/apierr"
	"ledger/internal/service"
	"ledger/internal/store"
)

// Deps 路由装配依赖。
type Deps struct {
	Cfg     *config.Config
	Store   *store.Store
	Meta    *store.MetaRepo
	Master  *store.MasterRepo
	Views   *store.ViewManager
	TxRepo  *store.TxRepo
	SSRepo  *store.ScreenshotRepo
	Mat     *service.Materializer
	Balance *service.BalanceService
	Txn     *service.TxnService
	Adjust  *service.AdjustService
	Stats   *service.StatsService
	Reports *service.ReportService
	Imports *service.ImportService
	OCR     *service.OCRService
	Backup  *service.BackupService
	Maint   *service.MaintenanceService
	SS      *service.ScreenshotStore
	// v1.2 新增
	ImportSheets *service.ImportSheetService
	Merge        *service.AccountMergeService
	Freeze       *service.FreezeService
}

// NewRouter 装配全部路由（T01–T04 端点 1–41 中的已实现子集 + 静态托管占位）。
func NewRouter(d *Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(Logger(), Recovery(), RestoringGate())

	metaH := NewMetaHandler(d.Cfg, d.Store, d.Meta, d.Mat)
	accH := NewAccountHandler(d.Store, d.Master, d.Views, d.Balance, d.Mat, d.Merge)
	masterH := NewMasterHandler(d.Store, d.Master)
	ledgerH := NewLedgerHandler(d.Store, d.Master, d.Views)
	txnH := NewTransactionHandler(d.Store, d.Master, d.TxRepo, d.Txn, d.Adjust, d.Balance, d.Mat, d.Freeze)
	statsH := NewStatsHandler(d.Stats, txnH, d.Mat)
	reportH := NewReportHandler(d.Reports)
	importH := NewImportHandler(d.Imports)
	sheetH := NewImportSheetHandler(d.ImportSheets)
	ocrH := NewOCRHandler(d.OCR)
	backupH := NewBackupHandler(d.Backup)
	sysH := NewSystemHandler(d.Mat, d.Balance, d.Maint, d.SSRepo, d.SS)

	api := r.Group("/api")
	// 免认证
	api.GET("/health", metaH.Health)
	api.POST("/auth/login", metaH.Login)

	// 认证分组
	authed := api.Group("")
	authed.Use(Auth(func() (string, error) { return d.Meta.Get("auth_token") }))
	{
		authed.GET("/auth/check", metaH.AuthCheck)
		authed.GET("/meta", metaH.Meta)
		authed.GET("/settings", metaH.GetSettings)
		authed.PUT("/settings", metaH.PutSettings)

		authed.GET("/accounts", accH.List)
		authed.POST("/accounts", accH.Create)
		authed.POST("/accounts/reorder", accH.Reorder)
		authed.POST("/accounts/merge", accH.Merge) // v1.2 账户合并（需求 R5）
		authed.GET("/accounts/:id", accH.Get)
		authed.PUT("/accounts/:id", accH.Update)
		authed.POST("/accounts/:id/archive", accH.Archive)
		authed.DELETE("/accounts/:id", accH.Delete)

		authed.GET("/categories", masterH.ListCategories)
		authed.POST("/categories", masterH.CreateCategory)
		authed.PUT("/categories/:id", masterH.UpdateCategory)
		authed.DELETE("/categories/:id", masterH.DeleteCategory)

		authed.GET("/tags", masterH.ListTags)
		authed.POST("/tags", masterH.CreateTag)
		authed.PUT("/tags/:id", masterH.UpdateTag)
		authed.DELETE("/tags/:id", masterH.DeleteTag)

		authed.GET("/merchants", masterH.ListMerchants)
		authed.POST("/merchants", masterH.CreateMerchant)
		authed.PUT("/merchants/:id", masterH.UpdateMerchant)
		authed.DELETE("/merchants/:id", masterH.DeleteMerchant)

		authed.GET("/regions/search", masterH.SearchRegions)
		authed.GET("/regions/:code", masterH.GetRegion)

		authed.GET("/ledgers", ledgerH.List)
		authed.POST("/ledgers", ledgerH.Create)
		authed.GET("/ledgers/:id", ledgerH.Get)
		authed.PUT("/ledgers/:id", ledgerH.Update)
		authed.POST("/ledgers/:id/archive", ledgerH.Archive)
		authed.DELETE("/ledgers/:id", ledgerH.Delete)
		authed.GET("/ledgers/:id/rules", ledgerH.GetRules)
		authed.PUT("/ledgers/:id/rules", ledgerH.PutRules)

		authed.GET("/transactions", txnH.List)
		authed.POST("/transactions", txnH.Create)
		authed.POST("/transactions/batch-delete", txnH.BatchDelete)
		authed.GET("/transactions/:id", txnH.Get)
		authed.PUT("/transactions/:id", txnH.Update)
		authed.DELETE("/transactions/:id", txnH.Delete)
		authed.POST("/transactions/:id/restore", txnH.Restore)
		authed.DELETE("/transactions/:id/purge", txnH.Purge)
		authed.POST("/transactions/:id/adjust", txnH.Adjust)

		// v1.2 §9.6 端点 75：确认冻结（transaction_ids / batch_id / all=true 三选一）
		authed.POST("/transactions/freeze-confirm", txnH.FreezeConfirm)

		authed.GET("/stats/summary", statsH.Summary)
		authed.GET("/stats/trend", statsH.Trend)
		authed.GET("/stats/category-breakdown", statsH.Breakdown)
		authed.GET("/stats/account-balance-series", statsH.BalanceSeries)
		authed.GET("/stats/category-ranking", statsH.Ranking)
		authed.GET("/stats/high-amount", statsH.HighAmount)

		authed.GET("/reports/category", reportH.Category)
		authed.GET("/reports/account", reportH.Account)
		authed.GET("/reports/tag", reportH.Tag)
		authed.GET("/reports/period-compare", reportH.PeriodCompare)

		authed.POST("/import/csv/preview", importH.Preview)
		authed.POST("/import/csv/commit", importH.Commit)
		authed.POST("/import/commit", importH.Commit) // 兼容别名

		// v1.2 通用表格导入（Excel/CSV，需求 R1）
		authed.POST("/import/sheet/probe", sheetH.Probe)
		authed.POST("/import/sheet/preview", sheetH.Preview)
		authed.POST("/import/sheet/commit", sheetH.Commit)
		authed.GET("/import/batches", sheetH.Batches)

		authed.POST("/ocr/recognize", ocrH.Recognize)
		authed.POST("/ocr/recognize-batch", ocrH.RecognizeBatch)
		authed.GET("/ocr/tasks/:id", ocrH.Task)
		authed.GET("/ocr/model", ocrH.Model)
		authed.PUT("/ocr/model", ocrH.SwitchModel)

		authed.POST("/backup/create", backupH.Create)
		authed.GET("/backup/list", backupH.List)
		authed.POST("/backup/restore", backupH.Restore)
		authed.DELETE("/backup/*file", backupH.Delete)

		authed.GET("/screenshots/:id", sysH.ScreenshotMeta)
		authed.GET("/screenshots/:id/file", sysH.ScreenshotFile)

		authed.POST("/maintenance/cleanup", sysH.Cleanup)
		authed.POST("/maintenance/rematerialize", sysH.Rematerialize)
		authed.GET("/maintenance/verify", sysH.Verify)

		// API 404 统一 JSON
		authed.Use(func(c *gin.Context) {
			c.JSON(http.StatusNotFound, gin.H{"error": &apierr.Error{
				Code: apierr.CodeNotFound, Message: "接口不存在"}})
		})
	}

	// 静态托管：webDir（默认 web/dist）存在 → SPA fallback；否则内联提示页（T08 定稿，保证不 500）。
	r.NoRoute(staticFallback(d.Cfg.WebDir))
	return r
}
