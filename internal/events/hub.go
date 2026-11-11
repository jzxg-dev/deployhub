// Package events 在进程内分发事件，Web 服务通过 SSE 推给打开的页面。
// M2 起部署引擎的任务事件也经由这里推送（同时写入数据库，页面刷新后从库里补齐）。
package events

import (
	"sync"
	"time"
)

// Event 是推给页面的一条事件。
type Event struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
}

// Hub 管理订阅者。发布不阻塞：某个订阅者处理不过来时丢弃发给它的事件。
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewHub 创建 Hub。
func NewHub() *Hub {
	return &Hub{subs: map[chan Event]struct{}{}}
}

// Subscribe 订阅事件，返回事件通道和取消订阅的函数。
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}

// Publish 发布事件给所有订阅者。
func (h *Hub) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribers 返回当前订阅者数量。
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
