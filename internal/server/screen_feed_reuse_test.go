package server

import (
	"testing"
	"time"

	"erbrus/internal/screen"
	"erbrus/internal/spawn"
)

// A run id handed to a new run while a page still watched the old run's
// window must not keep showing the old window.
func TestScreenFeedSwitchesWindowOnSameRunID(t *testing.T) {
	fs := &fakeSpawner{}
	fs.setScreen("s:4", spawn.Screen{Raw: "old window\n", Dead: true, Activity: time.Now()})
	fs.setScreen("s:2", spawn.Screen{Raw: "new window\n", Activity: time.Now()})
	feed := newScreenFeed(fs.Capture)
	feed.interval = 5 * time.Millisecond

	oldCh, cancelOld := feed.Subscribe(16, "s:4", screen.DefaultRules(""))
	defer cancelOld()
	select {
	case f := <-oldCh:
		if !f.Dead {
			t.Fatalf("old window frame = %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("no frame from the old window")
	}

	newCh, cancelNew := feed.Subscribe(16, "s:2", screen.DefaultRules(""))
	defer cancelNew()
	select {
	case f := <-newCh:
		if f.Dead || string(f.HTML) == "" || !contains(string(f.HTML), "new window") {
			t.Fatalf("new window frame = %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("no frame from the new window")
	}
	// Forget stops the poll; a later Subscribe starts a fresh one.
	feed.Forget(16)
	feed.mu.Lock()
	_, still := feed.runs[16]
	feed.mu.Unlock()
	if still {
		t.Fatal("Forget left the poll in place")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
