package stream

import "testing"

func TestSizeQueueKeepsLatest(t *testing.T) {
	done := make(chan struct{})
	queue := NewSizeQueue(done)
	queue.Set(80, 24)
	queue.Set(120, 40)

	size := queue.Next()
	if size == nil || size.Width != 120 || size.Height != 40 {
		t.Errorf("size = %+v", size)
	}
}

func TestSizeQueueStopsWhenDone(t *testing.T) {
	done := make(chan struct{})
	queue := NewSizeQueue(done)
	close(done)
	if size := queue.Next(); size != nil {
		t.Errorf("size = %+v", size)
	}
}
