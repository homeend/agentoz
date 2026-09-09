package server

import (
	"strings"
	"testing"

	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// checkedFake records the SendChecked call the server makes.
type checkedFake struct {
	*fakeSpawner
	screens   []string // what the submitted callback was asked about
	submitted []bool
}

func (c *checkedFake) SendChecked(h spawn.Handle, text string, submitted func(string) bool) error {
	c.sent = append(c.sent, string(h)+"|"+text)
	for _, sc := range c.screens {
		c.submitted = append(c.submitted, submitted(sc))
	}
	return nil
}

// agy keeps the sent text in its input box while it works: the heuristic
// alone reads that as "not submitted"; the provider's rules must win.
const agyWorkingWithText = "some transcript\n" +
	"────────────────────────────────────────\n" +
	"> please refactor the parser\n" +
	"────────────────────────────────────────\n" +
	"esc to cancel\n"

const agyIdleWithText = "some transcript\n" +
	"────────────────────────────────────────\n" +
	"> please refactor the parser\n" +
	"────────────────────────────────────────\n" +
	"? for shortcuts\n"

func TestDeliverUsesProviderRulesForSubmitted(t *testing.T) {
	ts, _, _ := newTestServer(t)
	_ = ts
	fs := &checkedFake{fakeSpawner: &fakeSpawner{}, screens: []string{agyWorkingWithText, agyIdleWithText}}
	fs.setScreen("erbrus:2", spawn.Screen{Raw: agyIdleWithText}) // idle: deliver now, not queued
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	rules, err := screen.Compile([]string{`^─{8,}\n>[^\n]*\n─{8,}\nesc to cancel`}, []string{`^─{8,}\n>[^\n]*\n─{8,}\n\? for shortcuts`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	testSrv.setRules("antigravity", &rules)

	run := store.AgentRun{Provider: "antigravity", TmuxTarget: "erbrus:2", AgentName: "antigravity", Status: "running"}
	if w := testSrv.sendToRun(run, "please refactor the parser"); w != "" {
		t.Fatalf("warning: %s", w)
	}
	if len(fs.sent) != 1 || !strings.HasPrefix(fs.sent[0], "erbrus:2|please refactor") {
		t.Fatalf("sent = %v", fs.sent)
	}
	if len(fs.submitted) != 2 || !fs.submitted[0] || fs.submitted[1] {
		t.Fatalf("submitted verdicts = %v, want [true false] (working yes, idle with text no)", fs.submitted)
	}

	// A spawner without SendChecked still gets plain Send.
	plain := &fakeSpawner{}
	plain.setScreen("erbrus:2", spawn.Screen{Raw: agyIdleWithText})
	testSrv.SetRuntime(plain, "/abs/erbrus", ts.URL)
	if w := testSrv.sendToRun(run, "hi"); w != "" || len(plain.sent) != 1 {
		t.Fatalf("plain send: %q %v", w, plain.sent)
	}
}
