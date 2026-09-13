package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"mana/internal/web"
)

//go:embed content web/templates/*.html web/static/*
var assets embed.FS

const (
	adminPasswordEnvironment          = "MANA_ADMIN_PASSWORD"
	adminTrustProxyHTTPSEnvironment   = "MANA_ADMIN_TRUST_PROXY_HTTPS"
	adminTrustedProxyCIDRsEnvironment = "MANA_ADMIN_TRUSTED_PROXY_CIDRS"
	minimumAdminPasswordLength        = 16
)

type adminConfig struct {
	Password          string
	TrustProxyHTTPS   bool
	TrustedProxyCIDRs []netip.Prefix
}

func (config adminConfig) Enabled() bool {
	return config.Password != ""
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	admin, err := loadAdminConfig()
	if err != nil {
		logger.Error("invalid admin configuration", "error", err)
		os.Exit(1)
	}
	if admin.Enabled() {
		logger.Info("admin editing is configured")
	}

	content, err := loadContentFS()
	if err != nil {
		logger.Error("could not load content", "error", err)
		os.Exit(1)
	}
	if closer, ok := content.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	if err := verifyAdminContentWritable(admin, content); err != nil {
		logger.Error("admin content is not writable", "error", err)
		os.Exit(1)
	}

	events, err := newEventRegistry(content)
	if err != nil {
		logger.Error("could not load events", "error", err)
		os.Exit(1)
	}

	staticFiles, err := fs.Sub(assets, "web/static")
	if err != nil {
		logger.Error("could not load static files", "error", err)
		os.Exit(1)
	}

	adminOptions := web.AdminOptions{}
	if admin.Enabled() {
		adminOptions.Password = admin.Password
		adminOptions.Content = newEventAdmin(events)
		adminOptions.TrustProxyHTTPS = admin.TrustProxyHTTPS
		adminOptions.TrustedProxyCIDRs = admin.TrustedProxyCIDRs
	}
	handler, err := web.NewDynamicWithAdmin(events.Current, assets, staticFiles, content, logger, adminOptions)
	if err != nil {
		logger.Error("could not create web server", "error", err)
		os.Exit(1)
	}

	port := envOrDefault("PORT", "8080")
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info("mana is ready", "address", fmt.Sprintf("http://localhost:%s", port))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}

func loadAdminConfig() (adminConfig, error) {
	var trustProxyHTTPS bool
	switch os.Getenv(adminTrustProxyHTTPSEnvironment) {
	case "", "false":
	case "true":
		trustProxyHTTPS = true
	default:
		return adminConfig{}, fmt.Errorf("%s must be true or false", adminTrustProxyHTTPSEnvironment)
	}
	trustedProxyCIDRs, err := loadTrustedProxyCIDRs(trustProxyHTTPS)
	if err != nil {
		return adminConfig{}, err
	}
	password := os.Getenv(adminPasswordEnvironment)
	if password == "" {
		return adminConfig{TrustProxyHTTPS: trustProxyHTTPS, TrustedProxyCIDRs: trustedProxyCIDRs}, nil
	}
	if utf8.RuneCountInString(password) < minimumAdminPasswordLength {
		return adminConfig{}, fmt.Errorf("%s must contain at least %d characters", adminPasswordEnvironment, minimumAdminPasswordLength)
	}
	if os.Getenv("CONTENT_DIR") == "" {
		return adminConfig{}, fmt.Errorf("%s requires CONTENT_DIR to reference writable external content", adminPasswordEnvironment)
	}
	return adminConfig{Password: password, TrustProxyHTTPS: trustProxyHTTPS, TrustedProxyCIDRs: trustedProxyCIDRs}, nil
}

func loadTrustedProxyCIDRs(trustProxyHTTPS bool) ([]netip.Prefix, error) {
	raw := strings.TrimSpace(os.Getenv(adminTrustedProxyCIDRsEnvironment))
	if !trustProxyHTTPS {
		if raw != "" {
			return nil, fmt.Errorf("%s requires %s=true", adminTrustedProxyCIDRsEnvironment, adminTrustProxyHTTPSEnvironment)
		}
		return nil, nil
	}
	if raw == "" {
		return nil, fmt.Errorf("%s=true requires one or more CIDRs in %s", adminTrustProxyHTTPSEnvironment, adminTrustedProxyCIDRsEnvironment)
	}
	values := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("%s contains invalid CIDR %q", adminTrustedProxyCIDRsEnvironment, strings.TrimSpace(value))
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func verifyAdminContentWritable(config adminConfig, content fs.FS) error {
	if !config.Enabled() {
		return nil
	}
	writable, ok := content.(interface{ verifyWritable() error })
	if !ok {
		return fmt.Errorf("configured content filesystem does not support admin writes")
	}
	if err := writable.verifyWritable(); err != nil {
		return fmt.Errorf("CONTENT_DIR must be writable by the Mana process: %w", err)
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
