// Package bootstrap 显式装配连接池、仓储、服务与处理器，并控制进程生命周期。
// 它是唯一同时知道业务接口与具体适配器实现的位置。
package bootstrap

import (
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/geo"
	"tripfolio/server/internal/adapters/imaging"
	"tripfolio/server/internal/adapters/mail"
	"tripfolio/server/internal/adapters/objectstore"
	accountpg "tripfolio/server/internal/adapters/postgres/account"
	assetspg "tripfolio/server/internal/adapters/postgres/assets"
	financepg "tripfolio/server/internal/adapters/postgres/finance"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/adapters/queue"
	"tripfolio/server/internal/adapters/ratelimit"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/config"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/modules/finance"
	geoservice "tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/modules/travel/routeplan"
	"tripfolio/server/internal/modules/travel/share"
	"tripfolio/server/internal/modules/travel/todo"
	"tripfolio/server/internal/modules/travel/trip"
)

// Services 是 API 用到的全部业务服务；测试也用它在内存或真实数据库上组装。
type Services struct {
	Identity   *account.IdentityService
	Sessions   *account.SessionService
	Profile    *account.ProfileService
	Categories *finance.CategoryService
	Trips      *trip.Service
	Itinerary  *itinerary.Service
	RoutePlans *routeplan.Service
	Packing    *packing.Service
	Todos      *todo.Service
	Shares     *share.Service
	Ledger     *finance.LedgerService
	Statistics *finance.StatisticsService
	// Assets 始终装配；对象存储未配置时其授权类用例返回 503，读取类用例照常工作。
	Assets *assets.Service
	// AssetVerifier 是 worker 侧校验器；对象存储未配置时为 nil，校验任务被推迟。
	AssetVerifier *assets.Verifier
	// ObjectStore 未配置时为 nil；Geo 服务在没有供应商时返回 503。
	ObjectStore *objectstore.S3Store
	Geo         *geoservice.Service
}

// BuildServices 用连接池装配服务。mailer 为 nil 时按配置创建。
func BuildServices(pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger, mailer mail.Mailer) (Services, error) {
	keyring, err := loadKeyring(cfg, logger)
	if err != nil {
		return Services{}, err
	}
	if mailer == nil {
		mailer = buildMailer(cfg, logger)
	}
	clk := clock.Real{}
	policy := account.DefaultPolicy()
	store := accountpg.NewStore(pool)
	sessions := account.NewSessionService(store, security.NewTokenIssuer(keyring, policy.AccessTokenTTL), clk, policy)
	identity := account.NewIdentityService(account.IdentityDeps{
		Store: store, Sessions: sessions, Hasher: security.NewPasswordHasher(cfg.PasswordHashConcurrency), Keyring: keyring,
		Mailer: mailer, Limiter: ratelimit.New(), Clock: clk, Policy: policy, Logger: logger,
	})

	insertOnly, err := queue.NewInsertOnlyClient(pool, logger)
	if err != nil {
		return Services{}, fmt.Errorf("创建任务客户端: %w", err)
	}
	writer := pgcore.NewWriter(pool, insertOnly, clk, logger)
	categories := finance.NewCategoryService(financepg.NewCategoryUnitOfWork(writer), financepg.NewCategoryReader(pool), clk)
	cursors := security.NewCursorCodec(keyring)
	trips := trip.NewService(travelpg.NewTripUnitOfWork(writer), travelpg.NewTripReader(pool), cursors, clk, policy.ReauthWindow)
	itineraries := itinerary.NewService(travelpg.NewItineraryUnitOfWork(writer), travelpg.NewItineraryReader(pool), cursors, clk)
	packings := packing.NewService(travelpg.NewPackingUnitOfWork(writer), travelpg.NewPackingReader(pool), cursors, clk)
	todos := todo.NewService(travelpg.NewTodoUnitOfWork(writer), travelpg.NewTodoReader(pool), cursors, clk)
	ledgerReader := financepg.NewLedgerReader(pool)
	ledger := finance.NewLedgerService(financepg.NewLedgerUnitOfWork(writer), ledgerReader, cursors, clk)
	statistics := finance.NewStatisticsService(ledgerReader, cursors)

	objects, err := buildObjectStore(cfg, logger)
	if err != nil {
		return Services{}, err
	}
	places, err := buildGeo(cfg, logger)
	if err != nil {
		return Services{}, err
	}
	geoSvc := geoservice.NewService(nil)
	if places != nil {
		geoSvc = geoservice.NewService(places)
	}
	routeStore := travelpg.NewRoutePlanStore(pool)
	routePlans := routeplan.NewService(travelpg.NewRoutePlanUnitOfWork(writer), routeStore, routeStore, geoSvc, clk)

	shares := share.NewService(share.Deps{
		Store: travelpg.NewShareStore(pool), Trips: travelpg.NewTripReader(pool), Itinerary: travelpg.NewItineraryReader(pool),
		Routes: geoSvc, Limiter: ratelimit.New(), Cursors: cursors, Clock: clk, WebBaseURL: cfg.WebBaseURL,
	})

	// 对象键推导、授权与 worker 读写都由同一个 S3Store 经 AssetsStore 适配提供；
	// 未配置时保持接口为 nil（不能把 nil 指针赋给接口），服务按 503 降级、校验任务被推迟。
	limits := uploadLimits(metadata.Current().UploadLimits)
	assetDeps := assets.Deps{
		UnitOfWork: assetspg.NewUnitOfWork(writer), Reader: assetspg.NewReader(pool),
		Cursors: cursors, Clock: clk, Limits: limits,
	}
	var verifier *assets.Verifier
	if objects != nil {
		adapted := objectstore.NewAssetsStore(objects)
		assetDeps.Keys, assetDeps.Objects = adapted, adapted
		verifier = assets.NewVerifier(assets.VerifierDeps{
			UnitOfWork: assetDeps.UnitOfWork, Reader: assetDeps.Reader, Keys: adapted, Objects: adapted,
			Images: imaging.AssetsProcessor{}, Limits: limits, Clock: clk, Logger: logger,
		})
	}
	assetSvc := assets.NewService(assetDeps)
	// 头像校验由 assets 提供：资料服务必须在资产服务之后装配。
	profile := account.NewProfileService(store, assetSvc, clk)

	return Services{
		Identity: identity, Sessions: sessions, Profile: profile, Categories: categories,
		Trips: trips, Itinerary: itineraries, RoutePlans: routePlans, Packing: packings, Todos: todos, Ledger: ledger, Statistics: statistics,
		Assets: assetSvc, AssetVerifier: verifier, ObjectStore: objects, Geo: geoSvc, Shares: shares,
	}, nil
}

// uploadLimits 把 metadata 的上传限制转为 assets 模块的类型：两者字段一致，只是避免模块反向依赖 metadata。
func uploadLimits(l metadata.UploadLimits) assets.UploadLimits {
	return assets.UploadLimits{
		ImageMaxBytes: l.ImageMaxBytes, PDFMaxBytes: l.PDFMaxBytes,
		ImageMediaTypes: l.ImageMediaTypes, PDFMediaTypes: l.PDFMediaTypes,
	}
}

// buildObjectStore 按配置创建对象存储客户端。未配置时返回 nil：
// 所有环境都允许不接对象存储，文件授权接口返回依赖不可用。
func buildObjectStore(cfg config.Config, logger *slog.Logger) (*objectstore.S3Store, error) {
	if !cfg.ObjectStore.Configured() {
		logger.Warn("未配置对象存储，文件相关接口将返回依赖不可用")
		return nil, nil
	}
	store, err := objectstore.NewS3Store(objectstore.Config{
		Endpoint:        cfg.ObjectStore.Endpoint,
		Region:          cfg.ObjectStore.Region,
		Bucket:          cfg.ObjectStore.Bucket,
		AccessKeyID:     cfg.ObjectStore.AccessKeyID,
		SecretAccessKey: cfg.ObjectStore.SecretAccessKey,
		UsePathStyle:    cfg.ObjectStore.UsePathStyle,
	})
	if err != nil {
		return nil, fmt.Errorf("创建对象存储客户端: %w", err)
	}
	logger.Info("对象存储已装配", "endpoint", cfg.ObjectStore.Endpoint, "bucket", cfg.ObjectStore.Bucket, "path_style", cfg.ObjectStore.UsePathStyle)
	return store, nil
}

// buildGeo 按配置创建高德客户端。未配置时返回 nil，地点接口返回依赖不可用。
func buildGeo(cfg config.Config, logger *slog.Logger) (*geo.AmapClient, error) {
	if !cfg.Geo.Configured() {
		logger.Warn("未配置高德 Web 服务 key，地点接口将返回依赖不可用")
		return nil, nil
	}
	client, err := geo.NewAmapClient(geo.Config{
		Key:                 cfg.Geo.AmapKey,
		Timeout:             cfg.Geo.Timeout,
		PerAccountPerMinute: cfg.Geo.PerAccountPerMinute,
		GlobalDailyLimit:    cfg.Geo.GlobalDailyLimit,
		CacheTTL:            cfg.Geo.CacheTTL,
	}, ratelimit.New())
	if err != nil {
		return nil, fmt.Errorf("创建高德客户端: %w", err)
	}
	logger.Info("地点服务已装配", "per_account_per_minute", cfg.Geo.PerAccountPerMinute, "global_daily_limit", cfg.Geo.GlobalDailyLimit)
	return client, nil
}

func loadKeyring(cfg config.Config, logger *slog.Logger) (*security.Keyring, error) {
	if cfg.Keyring != "" {
		kr, err := security.ParseKeyring(cfg.Keyring)
		if err != nil {
			return nil, fmt.Errorf("TRIPFOLIO_KEYRING: %w", err)
		}
		return kr, nil
	}
	raw, err := security.RandomBytes(32)
	if err != nil {
		return nil, err
	}
	logger.Warn("未设置 TRIPFOLIO_KEYRING，使用本次启动的临时密钥；重启后所有会话失效")
	return security.ParseKeyring("ephemeral=" + base64.StdEncoding.EncodeToString(raw))
}

// buildMailer 按配置创建投递实现；禁用时返回 nil，log 驱动只在开发与测试使用。
func buildMailer(cfg config.Config, logger *slog.Logger) mail.Mailer {
	switch cfg.Mail.Driver {
	case "smtp":
		return mail.NewSMTPMailer(mail.SMTPConfig{
			Host: cfg.Mail.SMTPHost, Port: cfg.Mail.SMTPPort, Username: cfg.Mail.SMTPUsername, Password: cfg.Mail.SMTPPassword,
			From: cfg.Mail.From, FromName: cfg.Mail.FromName, ImplicitTLS: cfg.Mail.ImplicitTLS,
		})
	case "log":
		return mail.LogMailer{Logger: logger}
	default:
		logger.Warn("邮件服务未启用，注册与找回密码的验证码接口将返回依赖不可用；已有账号仍可用密码登录")
		return nil
	}
}
