// Package bootstrap 显式装配连接池、仓储、服务与处理器，并控制进程生命周期。
// 它是唯一同时知道业务接口与具体适配器实现的位置。
package bootstrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/mail"
	accountpg "tripfolio/server/internal/adapters/postgres/account"
	financepg "tripfolio/server/internal/adapters/postgres/finance"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/adapters/queue"
	"tripfolio/server/internal/adapters/ratelimit"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/config"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/modules/travel/todo"
	"tripfolio/server/internal/modules/travel/trip"
	"tripfolio/server/internal/transport/httpapi"
)

// Services 是 API 用到的全部业务服务；测试也用它在内存或真实数据库上组装。
type Services struct {
	Identity   *account.IdentityService
	Sessions   *account.SessionService
	Profile    *account.ProfileService
	Categories *finance.CategoryService
	Trips      *trip.Service
	Itinerary  *itinerary.Service
	Packing    *packing.Service
	Todos      *todo.Service
	Ledger     *finance.LedgerService
	Statistics *finance.StatisticsService
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
	profile := account.NewProfileService(store, nil, clk)

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

	return Services{
		Identity: identity, Sessions: sessions, Profile: profile, Categories: categories,
		Trips: trips, Itinerary: itineraries, Packing: packings, Todos: todos, Ledger: ledger, Statistics: statistics,
	}, nil
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

func buildMailer(cfg config.Config, logger *slog.Logger) mail.Mailer {
	if cfg.Mail.Driver == "smtp" {
		return mail.NewSMTPMailer(mail.SMTPConfig{
			Host: cfg.Mail.SMTPHost, Port: cfg.Mail.SMTPPort, Username: cfg.Mail.SMTPUsername, Password: cfg.Mail.SMTPPassword,
			From: cfg.Mail.From, FromName: cfg.Mail.FromName, ImplicitTLS: cfg.Mail.ImplicitTLS,
		})
	}
	return mail.LogMailer{Logger: logger}
}

// RunAPI 启动 HTTP 服务并阻塞到 ctx 结束。启动时先检查数据库可达与迁移版本，不满足则直接失败。
func RunAPI(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := pgcore.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	readiness, err := newDBReadiness(pool)
	if err != nil {
		return err
	}
	if err := readiness.Check(ctx); err != nil {
		return fmt.Errorf("启动检查失败: %w", err)
	}
	services, err := BuildServices(pool, cfg, logger, nil)
	if err != nil {
		return err
	}

	router := httpapi.NewRouter(httpapi.Deps{
		Logger: logger, Metadata: metadata.Current(), Readiness: readiness, CORSOrigins: cfg.CORSOrigins,
		Cookies:  httpapi.CookieSettings{Secure: cfg.CookieSecure},
		Identity: services.Identity, Sessions: services.Sessions, Profile: services.Profile, Categories: services.Categories,
		Trips: services.Trips, Itinerary: services.Itinerary, Packing: services.Packing, Todos: services.Todos,
		Ledger: services.Ledger, Statistics: services.Statistics,
	})
	srv := httpapi.NewServer(cfg.HTTPAddr, router)
	logger.Info("api 启动", "env", cfg.Env, "addr", cfg.HTTPAddr, "cors_origins", cfg.CORSOrigins, "cookie_secure", cfg.CookieSecure, "mail_driver", cfg.Mail.Driver)
	return httpapi.Serve(ctx, srv, cfg.ShutdownTimeout, logger)
}
