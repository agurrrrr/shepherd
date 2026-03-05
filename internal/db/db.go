package db

import (
	"context"
	"fmt"
	"sync"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/names"

	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/mattn/go-sqlite3"
)

var (
	client *ent.Client
	once   sync.Once
)

// Init initializes the database connection and runs migrations.
func Init() error {
	var initErr error
	once.Do(func() {
		dbPath := config.GetString("db_path")
		if dbPath == "" {
			dbPath = config.GetConfigPath()
			// config 경로에서 shepherd.db 경로 추출
			dbPath = dbPath[:len(dbPath)-len("config.yaml")] + "shepherd.db"
		}

		// WAL 모드와 busy_timeout으로 동시성 개선
		drv, err := entsql.Open("sqlite3", fmt.Sprintf("file:%s?cache=shared&_fk=1&_journal_mode=WAL&_busy_timeout=5000", dbPath))
		if err != nil {
			initErr = fmt.Errorf("failed to open database: %w", err)
			return
		}

		// SQLite는 동시 write를 허용하지 않으므로 커넥션을 1개로 제한하여
		// "database table is locked" 에러 방지
		db := drv.DB()
		db.SetMaxOpenConns(1)

		client = ent.NewClient(ent.Driver(drv))

		// Run auto migration
		if err := client.Schema.Create(context.Background()); err != nil {
			initErr = fmt.Errorf("failed to create schema: %w", err)
			return
		}

		// names 패키지에 클라이언트 설정 및 기본 이름 초기화
		names.SetClient(client)
		if err := names.InitializeDefaults(); err != nil {
			initErr = fmt.Errorf("failed to initialize sheep names: %w", err)
			return
		}
	})
	return initErr
}

// Client returns the database client.
func Client() *ent.Client {
	return client
}

// Close closes the database connection.
func Close() error {
	if client != nil {
		return client.Close()
	}
	return nil
}
