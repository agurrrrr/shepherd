package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// The read pool must allow concurrent reads without ever accepting writes;
// writes stay on the single-connection write pool.
func TestReadOnlyPoolRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read-pool.db")

	write, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open write db: %v", err)
	}
	t.Cleanup(func() { _ = write.Close() })
	write.SetMaxOpenConns(1)

	if _, err := write.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := write.Exec(`INSERT INTO t (v) VALUES ('x')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	read, err := sql.Open("sqlite", "file:"+path+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open read db: %v", err)
	}
	t.Cleanup(func() { _ = read.Close() })
	read.SetMaxOpenConns(8)

	var n int
	if err := read.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatalf("read query: %v", err)
	}
	if n != 1 {
		t.Fatalf("read query count = %d, want 1", n)
	}

	if _, err := read.Exec(`INSERT INTO t (v) VALUES ('y')`); err == nil {
		t.Fatal("read-only pool accepted a write")
	}

	if _, err := write.Exec(`INSERT INTO t (v) VALUES ('z')`); err != nil {
		t.Fatalf("write pool blocked by read pool: %v", err)
	}
}
