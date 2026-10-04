package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	gormadapter "github.com/barluscuda/golang-image-safety/internal/adapter/gorm"
	httpadapter "github.com/barluscuda/golang-image-safety/internal/adapter/http"
	"github.com/barluscuda/golang-image-safety/internal/adapter/onnx"
	"github.com/barluscuda/golang-image-safety/internal/adapter/storage"
	"github.com/barluscuda/golang-image-safety/internal/config"
	"github.com/barluscuda/golang-image-safety/internal/repository"
	"github.com/barluscuda/golang-image-safety/internal/service"
	"github.com/barluscuda/golang-image-safety/internal/worker"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logConfig := zap.NewProductionConfig()
	if err := logConfig.Level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return fmt.Errorf("invalid log level %q: %w", cfg.LogLevel, err)
	}
	logger, err := logConfig.Build()
	if err != nil {
		return err
	}
	defer logger.Sync()
	policyText, policyHash, err := cfg.Policy.Snapshot()
	if err != nil {
		return err
	}
	moderator, err := onnx.New(onnx.Config{ModelPath: cfg.ModelPath, RuntimeLibrary: cfg.RuntimeLibrary,
		Timeout: cfg.ModelTimeout, MaxImageBytes: cfg.MaxImageBytes, Threads: cfg.ModelThreads})
	if err != nil {
		return err
	}
	defer func() {
		if err := moderator.Close(); err != nil {
			logger.Error("close ONNX classifier", zap.Error(err))
		}
	}()
	logger.Info("local ONNX classifier loaded", zap.String("model", onnx.ModelName), zap.Int("threads", cfg.ModelThreads))
	db, err := gormadapter.Initialize(cfg.DatabaseDSN, &repository.ImageModel{})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sqlite connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	repo := repository.NewImageRepository(db)
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	err = repo.ResetProcessing(recoveryCtx)
	recoveryCancel()
	if err != nil {
		return fmt.Errorf("recover interrupted images: %w", err)
	}
	files, err := storage.NewLocal(cfg.StorageDirectory)
	if err != nil {
		return err
	}
	images := service.NewImageService(repo, files, policyText, policyHash, cfg.MaxImageBytes)
	moderation := service.NewModerationService(repo, files, moderator, logger)
	backgroundWorker := worker.New(moderation, cfg.WorkerPollInterval, logger)
	server := &http.Server{Addr: cfg.Address, Handler: httpadapter.NewRouter(images, cfg.MaxImageBytes, logger),
		ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := backgroundWorker.Run(ctx); err != nil {
			logger.Error("worker stopped", zap.Error(err))
		}
	}()
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", zap.String("address", cfg.Address), zap.String("policy_hash", policyHash))
		serverErr <- server.ListenAndServe()
	}()
	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case <-workerDone:
		runErr = fmt.Errorf("worker stopped unexpectedly")
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		runErr = errors.Join(runErr, err)
	}
	// Native inference is canceled through RunOptions. Join the worker before
	// releasing its session, tensors, database connection, or runtime library.
	<-workerDone
	return runErr
}
