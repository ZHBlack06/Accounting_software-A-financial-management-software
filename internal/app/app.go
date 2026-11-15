// Package app 装配后端全部组件（store → service → http），并负责监听、启动任务与优雅关闭。
//
// 该包被 cmd/server（浏览器模式）与 cmd/desktop（桌面模式）共用，
// 保证两种入口的迁移/种子/维护任务/路由完全一致，不会随时间漂移。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"ledger/internal/config"
	httpapi "ledger/internal/http"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// 启动任务（§6.12）编号，与文档及日志保持一致。
const (
	stepMigrate = "启动任务 1/7: migrate 完成"
	stepSeed    = "启动任务 2/7: seed + 视图自检 + schema_version 校验完成"
)

// App 是装配完成、可监听的应用实例（New 之后尚未监听）。
type App struct {
	Cfg     *config.Config
	DB      *store.Store
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
	Batches      *store.ImportBatchRepo
	ImportSheets *service.ImportSheetService
	Merge        *service.AccountMergeService
	Freeze       *service.FreezeService

	router   *gin.Engine
	serveErr chan error

	mu      sync.Mutex
	ln      net.Listener
	addr    string
	srv     *http.Server
	started bool
	closed  bool

	bgOnce sync.Once
	bgWG   sync.WaitGroup
	bgCtx  context.Context
	bgStop context.CancelFunc
}

// New 打开数据库、执行迁移/种子/视图自检、装配 services 与 router。不监听端口。
// 返回错误时已完成资源清理，调用方无需再 Close。
func New(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, errors.New("config 不能为空")
	}
	if cfg.Version == "" {
		cfg.Version = config.Version
	}
	if cfg.WebDir == "" {
		cfg.WebDir = config.DefaultWebDir
	}

	if err := cfg.EnsureDirs(); err != nil {
		slog.Error("数据目录创建失败", "err", err)
		return nil, err
	}
	listenDesc := cfg.Addr()
	if cfg.Port == 0 {
		listenDesc = fmt.Sprintf("%s:0(系统分配)", cfg.Host)
	}
	slog.Info("配置就绪", "listen", listenDesc, "data_dir", cfg.DataDir,
		"db", cfg.DBPath(), "ocr", cfg.OCREndpoint, "web_dir", cfg.WebDir,
		"version", cfg.Version)

	s, err := store.Open(cfg.DBPath())
	if err != nil {
		slog.Error("数据库打开失败", "err", err)
		return nil, fmt.Errorf("数据库打开失败: %w", err)
	}
	// fail 统一收尾：关闭已打开的资源后返回错误。
	fail := func(msg string, cause error) (*App, error) {
		if s != nil {
			_ = s.Close()
		}
		slog.Error(msg, "err", cause)
		return nil, fmt.Errorf("%s: %w", msg, cause)
	}

	// 1. migrate
	if err = s.Migrate(); err != nil {
		return fail("启动任务 1/7: migrate 失败", err)
	}
	slog.Info(stepMigrate)

	// 2. seed + 视图自检 + schema_version 校验
	views := store.NewViewManager(s)
	if err = s.Seed(); err != nil {
		return fail("启动任务 2/7: seed 失败", err)
	}
	if err = views.StartReconcile(); err != nil {
		return fail("启动任务 2/7: 视图自检失败", err)
	}
	meta := store.NewMetaRepo(s)
	if v, _ := meta.Get("schema_version"); v != store.SchemaVersion {
		return fail("启动任务 2/7: schema_version 不匹配",
			fmt.Errorf("schema_version=%q，期望 %q", v, store.SchemaVersion))
	}
	slog.Info(stepSeed)

	// 2.5 seq:global 自愈（恢复旧库/外部改库后计数器可能落后 → 新建交易会撞 UNIQUE）
	if fixed, from, to, serr := s.EnsureGlobalSeq(); serr != nil {
		slog.Warn("启动任务 2.5/7: 检查 seq:global 失败", "err", serr)
	} else if fixed {
		slog.Warn("启动任务 2.5/7: 检测到 seq:global 落后于 max(created_seq)，已自动修正",
			"from", from, "to", to)
	} else {
		slog.Info("启动任务 2.5/7: seq:global 自检通过", "seq_global", from)
	}

	// ---- 服务装配 ----
	master := store.NewMasterRepo(s)
	txRepo := store.NewTxRepo(s, views)
	ssRepo := store.NewScreenshotRepo(s)
	mat := service.NewMaterializer(s, meta, txRepo)
	balance := service.NewBalanceService(s, meta, txRepo, mat)
	txn := service.NewTxnService(s, meta, txRepo, master, balance)
	adjust := service.NewAdjustService(s, meta, txRepo)
	stats := service.NewStatsService(s, txRepo, master, balance, meta)
	reports := service.NewReportService(s, txRepo, master, balance, stats)
	imports := service.NewImportService(s, meta, master, txn, balance, cfg.DataDir)
	ssStore := service.NewScreenshotStore(cfg.DataDir)
	ocrSvc := service.NewOCRService(cfg, s, ssStore, ssRepo, master, balance)
	backup := service.NewBackupService(s, meta, balance, mat, cfg.DataDir, cfg.DBPath())
	maint := service.NewMaintenanceService(cfg.DataDir, s)

	// ---- v1.2 新增服务 ----
	batches := store.NewImportBatchRepo(s)
	freeze := service.NewFreezeService(s, meta, txRepo, balance, mat)
	importSheets := service.NewImportSheetService(s, meta, master, txn, balance, batches, mat, cfg.DataDir)
	merge := service.NewAccountMergeService(s, meta, master, views, balance, mat, backup, cfg.DataDir)

	// v1.2 首次启动迁移：自动备份 → 把全部账户并入「总账户」（幂等；日志见 logs/v12-account-merge.log）
	if err = merge.AutoMergeOnUpgrade(context.Background()); err != nil {
		return fail("启动任务 2.5/7: v1.2 账户合并迁移失败", err)
	}

	router := httpapi.NewRouter(&httpapi.Deps{
		Cfg: cfg, Store: s, Meta: meta, Master: master, Views: views,
		TxRepo: txRepo, SSRepo: ssRepo, Mat: mat, Balance: balance, Txn: txn,
		Adjust: adjust, Stats: stats, Reports: reports, Imports: imports,
		OCR: ocrSvc, Backup: backup, Maint: maint, SS: ssStore,
		ImportSheets: importSheets, Merge: merge, Freeze: freeze,
	})

	bgCtx, bgStop := context.WithCancel(context.Background())
	a := &App{
		Cfg: cfg, DB: s, Meta: meta, Master: master, Views: views,
		TxRepo: txRepo, SSRepo: ssRepo, Mat: mat, Balance: balance, Txn: txn,
		Adjust: adjust, Stats: stats, Reports: reports, Imports: imports,
		OCR: ocrSvc, Backup: backup, Maint: maint, SS: ssStore,
		Batches: batches, ImportSheets: importSheets, Merge: merge, Freeze: freeze,
		router:   router,
		serveErr: make(chan error, 1),
		bgCtx:    bgCtx,
		bgStop:   bgStop,
	}
	return a, nil
}

// Start 监听端口并开始提供服务，同时拉起启动任务与每日定时任务。
// 端口被占用时返回错误且不改变实例状态，调用方可以修改 Cfg.Port 后重试。
func (a *App) Start() error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil
	}
	if a.closed {
		a.mu.Unlock()
		return errors.New("应用已关闭，无法再次启动")
	}
	ln, err := net.Listen("tcp", a.Cfg.Addr())
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("监听 %s 失败: %w", a.Cfg.Addr(), err)
	}
	srv := &http.Server{Handler: a.router, ReadHeaderTimeout: 10 * time.Second}
	a.ln = ln
	a.srv = srv
	a.addr = ln.Addr().String()
	a.started = true
	addr := a.addr
	a.mu.Unlock()

	a.bgOnce.Do(a.startBackground)

	go func() {
		slog.Info("启动任务 7/7: HTTP 服务启动", "addr", addr, "version", a.Cfg.Version)
		if errServe := srv.Serve(ln); errServe != nil && !errors.Is(errServe, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常退出", "err", errServe)
			select {
			case a.serveErr <- errServe:
			default:
			}
		}
	}()
	return nil
}

// Addr 返回实际监听地址（Port==0 时为内核分配的真实端口）；未启动时返回配置地址。
func (a *App) Addr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.addr != "" {
		return a.addr
	}
	return a.Cfg.Addr()
}

// URL 返回可供本机客户端访问的基址（http://host:port）。
func (a *App) URL() string {
	a.mu.Lock()
	addr := a.addr
	a.mu.Unlock()
	if addr == "" {
		addr = a.Cfg.Addr()
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// Handler 返回装配好的 HTTP 处理器（供 httptest / 桌面壳内嵌复用）。
func (a *App) Handler() http.Handler { return a.router }

// Started 报告是否已完成监听。
func (a *App) Started() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.started
}

// ServeErr 在 HTTP 服务异常退出（非 ErrServerClosed）时收到错误。
func (a *App) ServeErr() <-chan error { return a.serveErr }

// Shutdown 停止 HTTP 服务、等待后台任务收尾（以 ctx 为上限）并关闭数据库。
// 幂等，可重复调用。
func (a *App) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	srv, ln, s := a.srv, a.ln, a.DB
	a.mu.Unlock()

	var errs []error
	if srv != nil {
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("HTTP 服务关闭: %w", err))
		}
	} else if ln != nil {
		_ = ln.Close()
	}
	if ln != nil {
		_ = ln.Close()
	}

	a.bgStop()
	done := make(chan struct{})
	go func() {
		a.bgWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("后台任务未在期限内结束，继续关闭")
	}

	if s != nil {
		if err := s.Close(); err != nil {
			errs = append(errs, fmt.Errorf("数据库关闭: %w", err))
		}
	}
	return errors.Join(errs...)
}

// startBackground 拉起 §6.12 启动任务（3/7–6/7）与每日 03:00 定时任务。
func (a *App) startBackground() {
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		a.startupTasks()
	}()
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		a.dailyLoop()
	}()
}

// startupTasks 串行执行物化补偿、全量校验、周备份、每日清理；失败只记日志。
func (a *App) startupTasks() {
	ctx := context.Background()
	meta, mat, balance, backup, maint := a.Meta, a.Mat, a.Balance, a.Backup, a.Maint

	if a.bgCtx.Err() != nil {
		return
	}
	flag, _ := meta.Get("frozen_write_flag")
	last, _ := meta.Get("last_frozen_at")
	lastUnix, _ := strconv.ParseInt(last, 10, 64)
	switch {
	case flag == "1":
		slog.Info("启动任务 3/7: frozen_write_flag=1，强制全量重物化")
		if res, err := mat.ForceFull(ctx, time.Now()); err != nil {
			slog.Error("强制重物化失败", "err", err)
		} else {
			slog.Info("强制重物化完成", "rows", res.RowsProcessed, "elapsed_ms", res.ElapsedMS)
		}
	case lastUnix == 0 || time.Now().Unix()-lastUnix > 24*3600:
		if res, err := mat.Materialize(ctx, time.Now()); err != nil {
			slog.Error("启动物化失败", "err", err)
		} else {
			slog.Info("启动任务 3/7: 物化推进完成", "rows", res.RowsProcessed)
		}
	default:
		slog.Info("启动任务 3/7: 物化边界无需推进")
	}
	if a.bgCtx.Err() != nil {
		return
	}

	// 4. 全量校验
	if res, err := balance.Verify(); err != nil {
		slog.Error("启动任务 4/7: 全量校验执行失败", "err", err)
	} else if !res.OK {
		slog.Error("启动任务 4/7: 全量校验不一致", "mismatches", len(res.Mismatches))
		_ = meta.Set("verify_failed", "1")
	} else {
		_ = meta.Set("verify_failed", "0")
		slog.Info("启动任务 4/7: 全量校验通过", "checked_rows", res.CheckedRows)
	}
	if a.bgCtx.Err() != nil {
		return
	}

	// 5. 每周一备份
	backup.EnsureWeekly(ctx)
	slog.Info("启动任务 5/7: 周备份检测完成")
	if a.bgCtx.Err() != nil {
		return
	}

	// 6. 每日清理
	if cres, err := maint.Cleanup(ctx, time.Now()); err != nil {
		slog.Error("启动任务 6/7: 清理失败", "err", err)
	} else {
		slog.Info("启动任务 6/7: 清理完成", "originals", cres.ScreenshotOriginals,
			"backups", cres.Backups, "import_tmp", cres.ImportTmp)
	}
}

// dailyLoop 每小时检查：跨过本地 03:00 且当天未跑过 → 物化 + 清理（周一加备份）。
func (a *App) dailyLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	lastRunDay := time.Now().In(timeutil.Local()).Format("2006-01-02")
	for {
		select {
		case <-a.bgCtx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now().In(timeutil.Local())
		today := now.Format("2006-01-02")
		if now.Hour() < 3 || today == lastRunDay {
			continue
		}
		lastRunDay = today
		if res, err := a.Mat.Materialize(context.Background(), time.Now()); err != nil {
			slog.Error("每日物化失败", "err", err)
		} else {
			slog.Info("每日物化完成", "rows", res.RowsProcessed, "elapsed_ms", res.ElapsedMS)
		}
		if _, err := a.Maint.Cleanup(context.Background(), time.Now()); err != nil {
			slog.Error("每日清理失败", "err", err)
		}
		a.Backup.EnsureWeekly(context.Background())
	}
}
