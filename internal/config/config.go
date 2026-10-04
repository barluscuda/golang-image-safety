package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/barluscuda/golang-image-safety/internal/domain"
	"github.com/spf13/viper"
)

type Config struct {
	Address            string
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	ShutdownTimeout    time.Duration
	DatabaseDSN        string
	StorageDirectory   string
	MaxImageBytes      int64
	ModelPath          string
	RuntimeLibrary     string
	ModelTimeout       time.Duration
	ModelThreads       int
	Policy             domain.Policy
	WorkerPollInterval time.Duration
	LogLevel           string
}

func Load() (Config, error) {
	v := viper.New()
	v.SetConfigFile("config.yaml")
	v.SetEnvPrefix("IMAGE_SAFETY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config.yaml: %w", err)
	}
	cfg := Config{
		Address:          v.GetString("server.address"),
		ReadTimeout:      v.GetDuration("server.read_timeout"),
		WriteTimeout:     v.GetDuration("server.write_timeout"),
		ShutdownTimeout:  v.GetDuration("server.shutdown_timeout"),
		DatabaseDSN:      v.GetString("database.dsn"),
		StorageDirectory: v.GetString("storage.directory"),
		MaxImageBytes:    v.GetInt64("storage.max_image_bytes"),
		ModelPath:        v.GetString("moderation.model_path"),
		RuntimeLibrary:   v.GetString("moderation.runtime_library"),
		ModelTimeout:     v.GetDuration("moderation.timeout"),
		ModelThreads:     v.GetInt("moderation.threads"),
		Policy: domain.Policy{
			NSFWThreshold: float32(v.GetFloat64("policy.nsfw_threshold")),
			NSFLThreshold: float32(v.GetFloat64("policy.nsfl_threshold")),
		},
		WorkerPollInterval: v.GetDuration("worker.poll_interval"),
		LogLevel:           v.GetString("logging.level"),
	}
	if cfg.Address == "" || cfg.DatabaseDSN == "" || cfg.StorageDirectory == "" || cfg.ModelPath == "" || cfg.RuntimeLibrary == "" {
		return Config{}, fmt.Errorf("server address, database DSN, storage directory, model path, and runtime library are required")
	}
	if cfg.MaxImageBytes <= 0 || cfg.MaxImageBytes > 1<<30 || cfg.ModelThreads <= 0 || cfg.ReadTimeout <= 0 || cfg.WriteTimeout <= 0 || cfg.ShutdownTimeout <= 0 || cfg.ModelTimeout <= 0 || cfg.WorkerPollInterval <= 0 {
		return Config{}, fmt.Errorf("image size must be 1 byte to 1 GiB; threads and all timeouts must be positive")
	}
	if err := cfg.Policy.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
