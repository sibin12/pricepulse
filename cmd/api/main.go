// Command api is the entry point of the PricePulse HTTP server. It only wires
// things together: configuration, the database pool, the router and middleware.
// The actual request handling lives in internal/httpapi.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	// Blank import: we never call lib/pq directly. Importing it runs its init(),
	// which registers the "postgres" driver with database/sql.
	_ "github.com/lib/pq"

	"pricepulse/internal/alerts"
	"pricepulse/internal/auth"
	"pricepulse/internal/config"
	"pricepulse/internal/httpapi"
	"pricepulse/internal/repository"
	"pricepulse/internal/scraper"
	"pricepulse/internal/worker"
)

// Defaults for local development. Each can be overridden with an env var
// (DATABASE_URL, ADDR, CORS_ORIGIN) so the same binary runs in other environments.
// Settings can also live in a .env file in the working directory (see
// .env.example); real environment variables take precedence over the file,
// and ENV_FILE points at a different file.
// Scraper settings: SCRAPE_POLL (1m),
// SCRAPE_CONCURRENCY (4), SCRAPER_ENABLED (true), SCRAPER_ALLOW_PRIVATE (false).
// Email alerts (sent to each product's owner): SMTP_HOST, SMTP_PORT (587),
// SMTP_USERNAME, SMTP_PASSWORD, SMTP_FROM.
// Accounts: APP_URL (http://localhost:3000), COOKIE_SECURE (false; set true
// behind HTTPS), GOOGLE_CLIENT_ID + GOOGLE_CLIENT_SECRET to enable Google
// sign-in, GOOGLE_REDIRECT_URL (http://localhost:8088/auth/google/callback),
// ADMIN_EMAIL + ADMIN_PASSWORD to create (or promote) an admin account.
// Per-user limits and re-check intervals are managed in the admin dashboard.
const (
	defaultDatabaseURL = "postgres://postgres:postgres@localhost:5433/pricepulse?sslmode=disable"
	defaultAddr        = ":8088"
	allowedOrigin      = "http://localhost:3000" // local React dev server; override with CORS_ORIGIN
)

func main() {
	// Logger with a prefix so our lines are easy to spot in shared output.
	logger := log.New(os.Stdout, "[pricepulse] ", log.LstdFlags|log.Lmsgprefix)

	// Load .env before reading any setting below. Values are never logged.
	loadEnvFile(logger, envOr("ENV_FILE", ".env"))

	// Fail fast: if the database is unreachable at boot, there is no point serving.
	db, err := openDB(envOr("DATABASE_URL", defaultDatabaseURL))
	if err != nil {
		logger.Fatalf("database setup failed: %v", err)
	}
	defer db.Close() // release all pooled connections on exit
	logger.Println("connected to PostgreSQL")

	// ctx is cancelled when the process gets Ctrl+C (SIGINT) or SIGTERM (what
	// Docker/Kubernetes send on stop), letting us shut down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// One repository and one scraper are shared by the HTTP handlers and the
	// background worker; all of them are safe for concurrent use.
	repo := repository.NewProductRepository(db)
	users := repository.NewUserRepository(db)
	ensureAdmin(logger, users)
	if envOr("SCRAPE_INTERVAL", "") != "" {
		logger.Println("note: SCRAPE_INTERVAL is no longer used; set re-check intervals in the admin dashboard")
	}
	scr := scraper.New(scraper.Options{AllowPrivate: envBool("SCRAPER_ALLOW_PRIVATE", false)})
	notifier := newNotifier(logger, repo)
	priceWorker := worker.New(repo, scr, notifier, logger, worker.Config{
		Poll:        envDuration(logger, "SCRAPE_POLL", time.Minute),
		Concurrency: envInt(logger, "SCRAPE_CONCURRENCY", 4),
	})

	// The background loop stops when ctx is cancelled; the WaitGroup lets main
	// wait for it so in-flight checks finish saving before the DB pool closes.
	var bg sync.WaitGroup
	if envBool("SCRAPER_ENABLED", true) {
		bg.Add(1)
		go func() {
			defer bg.Done()
			priceWorker.Run(ctx)
		}()
	} else {
		logger.Println("background price worker disabled (SCRAPER_ENABLED=false)")
	}
	bg.Add(1)
	go func() {
		defer bg.Done()
		notifier.Run(ctx) // returns immediately if email isn't configured
	}()

	router := httpapi.NewRouter(httpapi.Deps{
		Logger:        logger,
		DB:            db,
		Products:      repo,
		Users:         users,
		Checker:       priceWorker,
		Alerts:        notifier,
		Google:        newGoogle(logger),
		AppURL:        envOr("APP_URL", "http://localhost:3000"),
		SecureCookies: envBool("COOKIE_SECURE", false),
	})
	origin := envOr("CORS_ORIGIN", allowedOrigin)

	srv := &http.Server{
		Addr: envOr("ADDR", defaultAddr),
		// Middleware order (outermost first): logging -> cors -> origin check -> router.
		// Logging wraps everything so it also records CORS preflight responses.
		Handler: httpapi.Logging(logger, httpapi.CORS(origin, httpapi.CheckOrigin(origin, router))),

		// Timeouts protect against slow or malicious clients holding connections
		// open forever (e.g. the "slowloris" attack). http.ListenAndServe has none.
		// WriteTimeout is 30s because POST /products/{id}/check waits for a store.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// ListenAndServe blocks, so run it in a goroutine and report its result on a channel.
	// Buffer of 1 means the goroutine can always send and exit, even if nobody receives.
	errCh := make(chan error, 1)
	go func() {
		logger.Printf("listening on %s", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()

	// Wait for whichever happens first: the server failing, or a shutdown signal.
	select {
	case err := <-errCh:
		// ErrServerClosed is the normal result of Shutdown, not a real failure.
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("server error: %v", err)
		}
	case <-ctx.Done():
		logger.Println("shutdown signal received, draining requests...")
		// Shutdown stops accepting new connections and waits for in-flight requests
		// to finish, but gives up after 10s so we can never hang forever.
		// A fresh context is used because ctx is already cancelled.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Printf("graceful shutdown failed: %v", err)
		}
	}
	stop()    // make sure the worker sees cancellation even if the server failed
	bg.Wait() // let the worker finish its current saves
	logger.Println("server stopped")
}

// openDB builds the connection pool, verifies it, and ensures the schema exists.
func openDB(dsn string) (*sql.DB, error) {
	// sql.Open does not connect; it only validates arguments and returns a pool handle.
	// Connections are created lazily when first needed.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	// Pool tuning. Postgres has a limited number of connections (default 100),
	// so we cap how many this service may hold open at once.
	db.SetMaxOpenConns(25)                  // hard ceiling; extra requests wait for a free connection
	db.SetMaxIdleConns(25)                  // keep warm connections ready to avoid reconnect cost
	db.SetConnMaxLifetime(30 * time.Minute) // recycle so dead/stale connections (proxies, failovers) are replaced
	db.SetConnMaxIdleTime(5 * time.Minute)  // close connections nobody has used for a while

	// One shared deadline for the startup checks below.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ping forces a real connection, proving the DSN, credentials and server are OK.
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := repository.Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// envOr returns the environment variable's value, or fallback if unset/blank.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// envBool parses true/false-style env vars ("1", "true", "false", ...).
func envBool(key string, fallback bool) bool {
	if b, err := strconv.ParseBool(envOr(key, "")); err == nil {
		return b
	}
	return fallback
}

// envInt reads a positive integer, warning and falling back on bad input.
func envInt(logger *log.Logger, key string, fallback int) int {
	raw := envOr(key, "")
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		logger.Printf("ignoring invalid %s=%q, using %d", key, raw, fallback)
		return fallback
	}
	return n
}

// envDuration reads values like "30m" or "6h", warning and falling back on bad input.
func envDuration(logger *log.Logger, key string, fallback time.Duration) time.Duration {
	raw := envOr(key, "")
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		logger.Printf("ignoring invalid %s=%q, using %s", key, raw, fallback)
		return fallback
	}
	return d
}

// newNotifier builds the email alert sender from env vars. Without SMTP_HOST
// it is created disabled: everything else still works.
func newNotifier(logger *log.Logger, repo *repository.ProductRepository) *alerts.Notifier {
	if envOr("ALERT_EMAIL_TO", "") != "" {
		logger.Println("note: ALERT_EMAIL_TO is no longer used; alerts go to each product owner's email")
	}

	var mailer alerts.Mailer
	if host := envOr("SMTP_HOST", ""); host != "" {
		user := envOr("SMTP_USERNAME", "")
		mailer = alerts.NewSMTPMailer(alerts.SMTPConfig{
			Host:     host,
			Port:     envInt(logger, "SMTP_PORT", 587),
			Username: user,
			Password: os.Getenv("SMTP_PASSWORD"), // not trimmed: spaces can be part of a password
			From:     envOr("SMTP_FROM", "PricePulse <"+envOr("SMTP_USERNAME", "pricepulse@localhost")+">"),
		})
	}

	return alerts.New(repo, mailer, logger, alerts.Config{
		AppURL: envOr("APP_URL", "http://localhost:3000"),
	})
}

// newGoogle enables "Sign in with Google" when client credentials are set,
// otherwise returns nil and the login page shows only email + password.
func newGoogle(logger *log.Logger) *auth.Google {
	id, secret := envOr("GOOGLE_CLIENT_ID", ""), envOr("GOOGLE_CLIENT_SECRET", "")
	if id == "" || secret == "" {
		logger.Println("Google sign-in disabled (set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET to enable)")
		return nil
	}
	logger.Println("Google sign-in enabled")
	return auth.NewGoogle(auth.GoogleConfig{
		ClientID:     id,
		ClientSecret: secret,
		RedirectURL:  envOr("GOOGLE_REDIRECT_URL", "http://localhost:8088/auth/google/callback"),
		// Test-only overrides (point at a fake provider); never set in production.
		AuthURL:  envOr("GOOGLE_AUTH_URL", ""),
		TokenURL: envOr("GOOGLE_TOKEN_URL", ""),
	})
}

// loadEnvFile applies a .env file. No file is fine (env vars or defaults are
// used); a malformed file stops startup so we never run half-configured.
func loadEnvFile(logger *log.Logger, path string) {
	res, err := config.Load(path)
	if config.IsNotExist(err) {
		if os.Getenv("ENV_FILE") != "" {
			logger.Fatalf("ENV_FILE=%s does not exist", path)
		}
		return
	}
	if err != nil {
		logger.Fatalf("reading settings file: %v", err)
	}
	logger.Printf("loaded %d settings from %s (%d overridden by real environment variables)", res.Loaded, path, res.Skipped)
	if res.Exposed {
		logger.Printf("warning: %s is readable by other users on this machine; run: chmod 600 %s", path, path)
	}
}

// ensureAdmin creates (or promotes) the admin account named in ADMIN_EMAIL,
// with ADMIN_PASSWORD as its password if it has to be created.
func ensureAdmin(logger *log.Logger, users *repository.UserRepository) {
	rawEmail, password := envOr("ADMIN_EMAIL", ""), os.Getenv("ADMIN_PASSWORD")
	if rawEmail == "" {
		return
	}
	email, msg := auth.NormalizeEmail(rawEmail)
	if msg != "" {
		logger.Fatalf("ADMIN_EMAIL: %s", msg)
	}
	if msg := auth.ValidatePassword(password); msg != "" {
		logger.Fatalf("ADMIN_PASSWORD: %s", msg)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		logger.Fatalf("hash admin password: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch result, err := users.EnsureAdmin(ctx, email, hash); {
	case err != nil:
		logger.Fatalf("admin account: %v", err)
	case result == "created":
		logger.Printf("created admin account %s (password from ADMIN_PASSWORD)", email)
	case result == "promoted":
		logger.Printf("made %s an admin (its existing password, if any, is unchanged)", email)
	}
}
