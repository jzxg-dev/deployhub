package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations 读取嵌入的迁移文件，文件名格式为 <四位序号>_<说明>.sql，按序号排序。
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var list []migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		prefix, _, ok := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil || version <= 0 || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("迁移文件名 %s 不符合 <序号>_<说明>.sql", name)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("迁移文件 %s 和 %s 序号重复", name, other)
		}
		seen[version] = name
		body, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		list = append(list, migration{version: version, name: name, sql: string(body)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	return list, nil
}

// migrate 依次执行还没执行过的迁移，每个迁移在一个事务里完成。
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("创建迁移记录表：%w", err)
	}

	list, err := loadMigrations()
	if err != nil {
		return err
	}
	known := map[int]bool{}
	for _, m := range list {
		known[m.version] = true
	}

	applied := map[int]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("读取迁移记录：%w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("读取迁移记录：%w", err)
		}
		applied[v] = true
		if !known[v] {
			rows.Close()
			return fmt.Errorf("数据库里有第 %d 号迁移，但当前程序没有，数据库版本比程序新，请升级 deployhub", v)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("读取迁移记录：%w", err)
	}

	for _, m := range list {
		if applied[m.version] {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

// noForeignKeysMark 写在迁移文件第一行，表示执行这个迁移时关闭外键检查。
// SQLite 改不了已有的约束，只能重建表；重建被别的表引用的表时必须先关外键（事务里改不了这个开关）。
const noForeignKeysMark = "-- migrate:no-foreign-keys"

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	// 外键开关是连接级的，所以整个迁移固定在一个连接上执行
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("执行迁移 %s：%w", m.name, err)
	}
	defer conn.Close()
	noFK := strings.HasPrefix(m.sql, noForeignKeysMark)
	if noFK {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return fmt.Errorf("执行迁移 %s：%w", m.name, err)
		}
		defer conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("执行迁移 %s：%w", m.name, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("执行迁移 %s：%w", m.name, err)
	}
	if noFK {
		// 关着外键执行完，提交前确认没有留下指向不存在记录的引用
		rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			return fmt.Errorf("执行迁移 %s：%w", m.name, err)
		}
		broken := rows.Next()
		rows.Close()
		if broken {
			return fmt.Errorf("执行迁移 %s：迁移后有记录引用了不存在的数据", m.name)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, time.Now().Unix()); err != nil {
		return fmt.Errorf("记录迁移 %s：%w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("执行迁移 %s：%w", m.name, err)
	}
	return nil
}
