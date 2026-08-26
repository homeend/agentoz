package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestForward(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chans := p["channels"].([]any)
	src := int64(chans[0].(map[string]any)["id"].(float64))
	dst := int64(chans[1].(map[string]any)["id"].(float64))

	mresp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, src),
		map[string]string{"kind": "report", "body": "the report"})
	m := decode[map[string]any](t, mresp)
	msgID := int64(m["id"].(float64))

	fresp := postJSON(t, fmt.Sprintf("%s/api/messages/%d/forward", ts.URL, msgID),
		map[string]int64{"channel_id": dst})
	if fresp.StatusCode != http.StatusCreated {
		t.Fatalf("forward status = %d", fresp.StatusCode)
	}
	copyMsg := decode[map[string]any](t, fresp)
	if int64(copyMsg["origin_message_id"].(float64)) != msgID {
		t.Error("copy must reference origin")
	}
	if int64(copyMsg["channel_id"].(float64)) != dst {
		t.Error("copy in wrong channel")
	}
	if copyMsg["body"] != "the report" {
		t.Error("body not copied")
	}
	_ = st
}

func TestForwardUnknownTargets(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	src := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	mresp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, src),
		map[string]string{"kind": "report", "body": "x"})
	m := decode[map[string]any](t, mresp)
	msgID := int64(m["id"].(float64))

	r1 := postJSON(t, fmt.Sprintf("%s/api/messages/%d/forward", ts.URL, msgID), map[string]int64{"channel_id": 999})
	if r1.StatusCode != http.StatusNotFound {
		t.Errorf("bad channel: %d, want 404", r1.StatusCode)
	}
	r1.Body.Close()
	r2 := postJSON(t, ts.URL+"/api/messages/999/forward", map[string]int64{"channel_id": src})
	if r2.StatusCode != http.StatusNotFound {
		t.Errorf("bad message: %d, want 404", r2.StatusCode)
	}
	r2.Body.Close()
}

func TestSSEDeliversMessageEvent(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	req, _ := http.NewRequest("GET", ts.URL+"/events", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer sseResp.Body.Close()
	if ct := sseResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	got := make(chan map[string]any, 1)
	go func() {
		sc := bufio.NewScanner(sseResp.Body)
		var event string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") && event == "message" {
				var v map[string]any
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v)
				got <- v
				return
			}
		}
	}()

	time.Sleep(100 * time.Millisecond) // let the subscription register
	postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID),
		map[string]string{"kind": "message", "body": "live"}).Body.Close()

	select {
	case v := <-got:
		if v["body"] != "live" {
			t.Errorf("event body = %v", v["body"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no SSE event within 3s")
	}
}
