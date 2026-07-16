package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/easypay-plus/easypay-plus/internal/config"
	"github.com/easypay-plus/easypay-plus/internal/epay"
	"github.com/easypay-plus/easypay-plus/internal/httpapi"
	"github.com/easypay-plus/easypay-plus/internal/secure"
	"github.com/easypay-plus/easypay-plus/internal/service"
	"github.com/easypay-plus/easypay-plus/internal/store"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration rejected", "error", err)
		os.Exit(1)
	}

	var storage store.Store
	if cfg.DBDriver == "memory" {
		storage = store.NewMemory()
		logger.Warn("using non-persistent memory store")
	} else {
		mysqlStore, openErr := store.OpenMySQL(cfg.MySQLDSN, cfg.AutoMigrate)
		if openErr != nil {
			logger.Error("database startup failed", "error", openErr)
			os.Exit(1)
		}
		storage = mysqlStore
	}
	defer storage.Close()

	vault := secure.NewVault(cfg.MasterKey)
	gateway := epay.New(epay.Config{
		BaseURL: cfg.EpayBaseURL, MerchantID: cfg.EpayMerchantID, Secret: cfg.EpaySecret,
		Mock: cfg.EpayMock, PublicBaseURL: cfg.PublicBaseURL + cfg.BasePath, CallbackSignMode: cfg.EpayCallbackSignMode,
	})
	appService := service.New(storage, gateway, vault, cfg)
	worker := service.NewDeliveryWorker(appService, logger)
	api := httpapi.New(appService, cfg, logger)

	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go worker.Run(ctx)
	go func() {
		logger.Info("easypay-plus started", "addr", cfg.HTTPAddr, "publicURL", cfg.PublicBaseURL, "mock", cfg.EpayMock, "database", cfg.DBDriver)
		if cfg.Environment != "production" {
			logger.Info("development admin token", "token", cfg.AdminToken)
		}
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
