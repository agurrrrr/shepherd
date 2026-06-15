package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/names"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

var (
	client *ent.Client
	rawDB  *sql.DB
	dbPath string
	once   sync.Once
)

// Init initializes the database connection and runs migrations.
func Init() error {
	var initErr error
	once.Do(func() {
		dbPath = config.GetString("db_path")
		if dbPath == "" {
			dbPath = config.GetConfigPath()
			// config 경로에서 shepherd.db 경로 추출
			dbPath = dbPath[:len(dbPath)-len("config.yaml")] + "shepherd.db"
		}

		// WAL 모드와 busy_timeout으로 동시성 개선
		sqlDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath))
		if err != nil {
			initErr = fmt.Errorf("failed to open database: %w", err)
			return
		}
		drv := entsql.OpenDB("sqlite3", sqlDB)

		// SQLite는 동시 write를 허용하지 않으므로 커넥션을 1개로 제한하여
		// "database table is locked" 에러 방지
		rawDB = drv.DB()
		rawDB.SetMaxOpenConns(1)

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

// RawDB returns the underlying *sql.DB. Useful for SQLite-specific commands
// such as `VACUUM INTO` that ent does not expose directly. Callers must not
// close this handle.
func RawDB() *sql.DB {
	return rawDB
}

// Path returns the on-disk path of the SQLite database.
func Path() string {
	return dbPath
}

// Close closes the database connection.
func Close() error {
	if client != nil {
		return client.Close()
	}
	return nil
}
