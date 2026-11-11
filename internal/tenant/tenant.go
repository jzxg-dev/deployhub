// Package tenant 给每个登录账号准备一套独立的业务层、部署引擎和事件通道。
//
// 各账号的数据在同一个数据库里按 owner_id 隔离（见 store.For）；构建目录、任务锁、实时事件也各用各的，
// 所以两个账号可以有同名的项目和应用，互相看不到对方的任务和日志。
//
// 注意：隔离的是数据，不是执行环境。构建命令在运行 deployhub 的机器上以同一个系统用户执行。
package tenant

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"deployhub/internal/core"
	"deployhub/internal/driver"
	"deployhub/internal/engine"
	"deployhub/internal/events"
	"deployhub/internal/remote"
	"deployhub/internal/store"
	"deployhub/internal/vault"
)

// Tenant 是一个账号的业务层、部署引擎和事件通道。
type Tenant struct {
	Core   *core.Service
	Engine *engine.Engine
	Hub    *events.Hub
}

// Options 是创建 Manager 需要的依赖。
type Options struct {
	Store   *store.Store // store.Open 返回的不带归属的 Store
	Vault   *vault.Vault
	Drivers *driver.Registry
	Runner  remote.Runner    // 业务层用：测试连接、检查
	SSH     remote.Transport // 部署引擎用
	DataDir string           // 账号的目录是 <DataDir>/accounts/<账号编号>：build/ 构建目录、repos/ 平台克隆的仓库、keys/ 私钥临时文件
	Logger  *slog.Logger
}

// Manager 按账号编号管理 Tenant，第一次用到时创建。
type Manager struct {
	o  Options
	mu sync.Mutex
	m  map[int64]*Tenant
}

// NewManager 创建 Manager。
func NewManager(o Options) *Manager {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Manager{o: o, m: map[int64]*Tenant{}}
}

// Dir 返回账号的数据目录。
func (m *Manager) Dir(userID int64) string {
	return filepath.Join(m.o.DataDir, "accounts", strconv.FormatInt(userID, 10))
}

// Get 返回账号的 Tenant，没有时创建（创建引擎时会把这个账号上次没执行完的任务标记为失败）。
func (m *Manager) Get(ctx context.Context, userID int64) (*Tenant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.m[userID]; ok {
		return t, nil
	}
	svc := core.New(m.o.Store.For(userID), m.o.Vault, m.o.Drivers, m.o.Runner)
	svc.SetDataDir(m.Dir(userID))
	hub := events.NewHub()
	eng, err := engine.New(ctx, engine.Options{Core: svc, SSH: m.o.SSH, Hub: hub, DataDir: m.Dir(userID), Logger: m.o.Logger.With("account", userID)})
	if err != nil {
		return nil, err
	}
	t := &Tenant{Core: svc, Engine: eng, Hub: hub}
	m.m[userID] = t
	return t, nil
}

// Busy 返回账号正在执行的任务数。
func (m *Manager) Busy(userID int64) int {
	m.mu.Lock()
	t := m.m[userID]
	m.mu.Unlock()
	if t == nil {
		return 0
	}
	return t.Engine.Running()
}

// Remove 在账号删除后调用：丢掉它的 Tenant 并删除构建目录。
func (m *Manager) Remove(userID int64) {
	m.mu.Lock()
	delete(m.m, userID)
	m.mu.Unlock()
	if err := os.RemoveAll(m.Dir(userID)); err != nil {
		m.o.Logger.Warn("删除账号的构建目录失败", "account", userID, "err", err)
	}
}

func (m *Manager) engines() []*engine.Engine {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]*engine.Engine, 0, len(m.m))
	for _, t := range m.m {
		list = append(list, t.Engine)
	}
	return list
}

// Running 返回所有账号正在执行的任务数。
func (m *Manager) Running() int {
	n := 0
	for _, e := range m.engines() {
		n += e.Running()
	}
	return n
}

// Wait 等待所有账号的任务结束（服务退出时用）。
func (m *Manager) Wait() {
	for _, e := range m.engines() {
		e.Wait()
	}
}
