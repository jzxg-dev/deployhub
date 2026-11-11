package auth

import (
	"sync"
	"time"
)

// Limiter 限制失败次数：同一个 key 在 window 时间内失败达到 max 次后，Allow 返回 false，直到最早的失败过期。
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	fails map[string][]time.Time
}

// NewLimiter 创建 Limiter。
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, fails: map[string][]time.Time{}}
}

// recent 去掉过期的失败记录，调用时要持有锁。
func (l *Limiter) recent(key string) []time.Time {
	cutoff := l.now().Add(-l.window)
	list := l.fails[key]
	i := 0
	for i < len(list) && !list[i].After(cutoff) {
		i++
	}
	list = list[i:]
	if len(list) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = list
	}
	return list
}

// Allow 判断 key 现在能不能再试；不能时 wait 是还要等多久。
func (l *Limiter) Allow(key string) (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.recent(key)
	if len(list) < l.max {
		return true, 0
	}
	return false, list[0].Add(l.window).Sub(l.now())
}

// Fail 记一次失败。
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// 顺手清理别的 key 里过期的记录，避免被大量不同的用户名撑大
	if len(l.fails) > 10_000 {
		for k := range l.fails {
			l.recent(k)
		}
	}
	l.fails[key] = append(l.recent(key), l.now())
}

// Reset 清掉 key 的失败记录（登录成功后）。
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}
