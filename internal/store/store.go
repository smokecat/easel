// Package store 管 SQLite 里的元数据：迁移（goose，SQL 文件嵌入二进制）和查询（sqlc 生成，见 db/）。
//
// 写连接只开一个，避免 SQLITE_BUSY；读走另一个连接池。
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // 注册 "sqlite" 驱动

	"github.com/smokecat/easel/internal/store/db"
)

//go:generate sh -c "cd ../.. && CGO_ENABLED=1 go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate"

//go:embed migrations/*.sql
var migrations embed.FS

// Store 持有读写两个连接池。
type Store struct {
	write *sql.DB
	read  *sql.DB
	// Q 是只读查询；写操作用 W（单条语句）或 Tx（多条语句）。
	Q *db.Queries
	// W 走唯一的写连接，单条写语句用它，免得在读连接池上写库撞 SQLITE_BUSY。
	W *db.Queries
}

// Open 打开（必要时创建）数据库文件并执行迁移。
func Open(ctx context.Context, path string) (*Store, error) {
	pragmas := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		pragmas.Add("_pragma", p)
	}
	base := "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + pragmas.Encode()

	write, err := sql.Open("sqlite", base+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)
	read, err := sql.Open("sqlite", base)
	if err != nil {
		_ = write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(4)

	s := &Store{write: write, read: read, Q: db.New(read), W: db.New(write)}
	if err := s.migrate(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	p, err := goose.NewProvider(goose.DialectSQLite3, s.write, mustSub(migrations, "migrations"))
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Tx 在一个写事务里执行 fn；fn 返回错误时回滚。
func (s *Store) Tx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(db.New(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err) // 编译期固定的目录
	}
	return sub
}

// Close 关闭连接池。
func (s *Store) Close() error {
	err1 := s.read.Close()
	err2 := s.write.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
