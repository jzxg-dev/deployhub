// Package backup 备份平台的全部数据：数据库的一致快照，加上主密钥。
//
// 平台的数据就是一个 SQLite 文件和一个主密钥；主密钥丢了，库里加密的密钥、凭据、配置文件内容都无法恢复，
// 所以两样要一起备份。备份目录默认在数据目录下（防误操作和数据库损坏），想防磁盘损坏就指到别的磁盘或同步盘。
//
// 恢复：停掉 deployhub，把某个 deployhub-<时间>.db 复制成数据目录下的 deployhub.db（同时删掉 deployhub.db-wal
// 和 deployhub.db-shm），把 master.key 复制到数据目录，再启动。
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"deployhub/internal/vault"
)

// Store 是能做数据库快照的存储。
type Store interface {
	Backup(ctx context.Context, dest string) error
}

const (
	prefix = "deployhub-"
	suffix = ".db"
	stamp  = "20060102-150405"
	// Interval 是自动备份的间隔。
	Interval = 24 * time.Hour
)

// Options 是备份的设置。
type Options struct {
	Store Store
	Vault *vault.Vault
	Dir   string // 备份目录
	Keep  int    // 自动备份保留最近几份，更早的删掉
}

// Run 立刻做一次备份，返回数据库快照的路径。主密钥写到同一个目录的 master.key（权限 600）。
func Run(ctx context.Context, o Options, now time.Time) (string, error) {
	dest := filepath.Join(o.Dir, prefix+now.Format(stamp)+suffix)
	if err := o.Store.Backup(ctx, dest); err != nil {
		return "", err
	}
	if err := writeKey(o); err != nil {
		return dest, err
	}
	prune(o)
	return dest, nil
}

// writeKey 把主密钥写到备份目录。主密钥不会变，内容一样就不重写。
func writeKey(o Options) error {
	if o.Vault == nil {
		return nil
	}
	file := filepath.Join(o.Dir, vault.KeyFile)
	content := o.Vault.ExportKey() + "\n"
	if b, err := os.ReadFile(file); err == nil && string(b) == content {
		return nil
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		return fmt.Errorf("备份主密钥到 %s：%w", file, err)
	}
	return os.Chmod(file, 0o600)
}

// snapshots 返回目录里按时间命名的自动备份，新的在前。升级前的备份（deployhub-before-upgrade-…）不算，不会被自动删掉。
func snapshots(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, suffix) {
			continue
		}
		if _, err := time.ParseInLocation(stamp, strings.TrimSuffix(strings.TrimPrefix(n, prefix), suffix), time.Local); err == nil {
			names = append(names, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names
}

func prune(o Options) {
	if o.Keep <= 0 {
		return
	}
	names := snapshots(o.Dir)
	for i := o.Keep; i < len(names); i++ {
		_ = os.Remove(filepath.Join(o.Dir, names[i]))
	}
}

// Latest 返回最近一次自动备份的时间，没有时 ok 为 false。
func Latest(dir string) (t time.Time, ok bool) {
	names := snapshots(dir)
	if len(names) == 0 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(stamp, strings.TrimSuffix(strings.TrimPrefix(names[0], prefix), suffix), time.Local)
	return t, err == nil
}

// Loop 定期自动备份，直到 ctx 结束：距离上一次备份超过 Interval 就做一次（启动时也检查），之后每小时检查一次。
// 用「距离上一次」而不是固定时刻：平台不一定一直开着。
func Loop(ctx context.Context, o Options, logger *slog.Logger) {
	check := func() {
		if last, ok := Latest(o.Dir); ok && time.Since(last) < Interval {
			return
		}
		path, err := Run(ctx, o, time.Now())
		if err != nil {
			logger.Error("自动备份失败", "err", err)
			return
		}
		logger.Info("已自动备份", "file", path)
	}
	check()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
