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
	client     *ent.Client
	rawDB      *sql.DB
	readClient *ent.Client
	dbPath     string
	once       sync.Once
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

		// SQLite는 동시 write를 허용하지 않으므로 쓰기 커넥션은 1개로 제한하여
		// "database table is locked" 에러 방지. 읽기는 별도 풀(ReadClient)로
		// 분리해 무거운 읽기 쿼리가 쓰기/다른 읽기를 막지 않게 한다.
		rawDB = drv.DB()
		rawDB.SetMaxOpenConns(1)

		client = ent.NewClient(ent.Driver(drv))

		// Run auto migration
		if err := client.Schema.Create(context.Background()); err != nil {
			initErr = fmt.Errorf("failed to create schema: %w", err)
			return
		}

		// Read-only pool: WAL lets readers run concurrently with the single
		// writer. query_only(1) makes accidental writes on this handle fail
		// loudly instead of corrupting the locking assumptions above.
		readSQLDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=query_only(1)&_pragma=busy_timeout(5000)", dbPath))
		if err != nil {
			initErr = fmt.Errorf("failed to open read database: %w", err)
			return
		}
		readSQLDB.SetMaxOpenConns(8)
		readSQLDB.SetMaxIdleConns(8)
		readClient = ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", readSQLDB)))

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

// ReadClient returns a client backed by a multi-connection, read-only pool so
// expensive reads do not queue behind the single write connection. It falls
// back to Client when Init has not opened the read pool (e.g. tests that inject
// an in-memory client via ReplaceClient).
func ReadClient() *ent.Client {
	if readClient != nil {
		return readClient
	}
	return client
}

// ReplaceClient swaps the global Ent client and returns the previous one.
// Tests use this to inject an in-memory database; they must restore the
// previous client (and not Close a live production handle).
func ReplaceClient(c *ent.Client) *ent.Client {
	old := client
	client = c
	return old
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
	if readClient != nil && readClient != client {
		_ = readClient.Close()
	}
	if client != nil {
		return client.Close()
	}
	return nil
}
