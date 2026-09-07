package server

import (
	"strings"
	"sync"
	"testing"
	"time"

	"erbrus/internal/spawn"
)

func fastPoll(t *testing.T) {
	t.Helper()
	old := screenPollInterval
	screenPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { screenPollInterval = old })
}

func recvFrame(t *testing.T, ch <-chan screenFrame) screenFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
		return screenFrame{}
	}
}

func TestScreenFeedSendsOnChangeOnly(t *testing.T) {
	fastPoll(t)
	var mu sync.Mutex
	raw, calls := "one", 0
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return spawn.Screen{Raw: raw, Cols: 80, Rows: 24}, nil
	})
	ch, cancel := feed.Subscribe(7, "s:5")
	if f := recvFrame(t, ch); !strings.Contains(string(f.HTML), "one") || f.Cols != 80 {
		t.Fatalf("first frame = %+v", f)
	}
	select {
	case f := <-ch:
		t.Fatalf("frame without change: %+v", f)
	case <-time.After(40 * time.Millisecond):
	}
	mu.Lock()
	raw = "two"
	mu.Unlock()
	if f := recvFrame(t, ch); !strings.Contains(string(f.HTML), "two") {
		t.Fatalf("changed frame = %+v", f)
	}
	cancel()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != n {
		t.Fatalf("still polling after last unsubscribe: %d -> %d", n, calls)
	}
}

func TestScreenFeedLateSubscriberGetsCurrentFrame(t *testing.T) {
	fastPoll(t)
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		return spawn.Screen{Raw: "steady"}, nil
	})
	ch1, cancel1 := feed.Subscribe(7, "s:5")
	defer cancel1()
	recvFrame(t, ch1)
	ch2, cancel2 := feed.Subscribe(7, "s:5")
	defer cancel2()
	if f := recvFrame(t, ch2); !strings.Contains(string(f.HTML), "steady") {
		t.Fatalf("late frame = %+v", f)
	}
}

func TestScreenFeedReportsCaptureError(t *testing.T) {
	fastPoll(t)
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		return spawn.Screen{}, errTest("can't find window")
	})
	ch, cancel := feed.Subscribe(7, "s:5")
	defer cancel()
	f := recvFrame(t, ch)
	if f.Error != "screen unavailable: can't find window" {
		t.Fatalf("error frame = %+v", f)
	}
	select {
	case f := <-ch:
		t.Fatalf("repeated error frame: %+v", f)
	case <-time.After(40 * time.Millisecond):
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
