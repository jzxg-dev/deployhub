package events

import "testing"

func TestHubPublishAndUnsubscribe(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	h.Publish(Event{Type: "hello"})
	e := <-ch
	if e.Type != "hello" || e.Time.IsZero() {
		t.Fatalf("收到 %+v", e)
	}
	cancel()
	cancel() // 重复取消不应出错
	if h.Subscribers() != 0 {
		t.Fatal("取消后应该没有订阅者")
	}
	h.Publish(Event{Type: "after"}) // 没有订阅者时不阻塞
}

func TestHubDoesNotBlockOnSlowSubscriber(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe()
	defer cancel()
	for i := 0; i < 1000; i++ {
		h.Publish(Event{Type: "x"}) // 缓冲区满后丢弃，不阻塞
	}
}
