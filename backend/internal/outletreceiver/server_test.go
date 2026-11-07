package outletreceiver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordsPostAndListsCalls(t *testing.T) {
	srv := httptest.NewServer(New().Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/hook", "text/plain", strings.NewReader("silent"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("post status %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/calls")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(raw), "silent") {
		t.Fatalf("calls %s", raw)
	}
	var body struct {
		Calls []call `json:"calls"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Calls) != 1 || body.Calls[0].Path != "/hook" {
		t.Fatalf("%+v", body.Calls)
	}
}

func TestStatusAndTelegramReplies(t *testing.T) {
	srv := httptest.NewServer(New().Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/status/503")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp, err = http.Post(srv.URL+"/bot1:x/sendMessage", "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(raw) != `{"ok":true}` {
		t.Fatalf("reply %s", raw)
	}
}
