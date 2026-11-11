package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"deployhub/internal/events"
)

// events 是 SSE 事件流：连接后先发 hello，之后定时发 ping，并转发 Hub 里的事件。
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 只订阅当前账号的事件通道，收不到别的账号的任务和日志
	ch, cancel := s.tenant(r).Hub.Subscribe()
	defer cancel()

	send := func(e events.Event) error {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data); err != nil {
			return err
		}
		return rc.Flush()
	}

	if err := send(events.Event{Type: "hello", Time: time.Now(), Data: map[string]string{"version": s.Version}}); err != nil {
		return
	}
	ticker := time.NewTicker(s.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			if err := send(e); err != nil {
				return
			}
		case t := <-ticker.C:
			if err := send(events.Event{Type: "ping", Time: t}); err != nil {
				return
			}
		}
	}
}
