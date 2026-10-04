package gormadapter

import (
	"fmt"

	"github.com/barluscuda/golang-image-safety/internal/adapter/gorm/sqlite"
	"gorm.io/gorm"
)

func Initialize(dsn string, models ...any) (*gorm.DB, error) {
	db, err := sqlite.Connect(dsn, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect sqlite through gorm: %w", err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		if sqlDB, poolErr := db.DB(); poolErr == nil {
			_ = sqlDB.Close()
		}
		return nil, fmt.Errorf("auto-migrate sqlite schema: %w", err)
	}
	return db, nil
}
