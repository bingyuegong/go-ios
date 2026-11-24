package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUIButtonHomeUsesWDAPressButton 保证 `ui button home --driver wda` 发送的是
// /session/<id>/wda/pressButton {"name":"home"}，而不是 /wda/homescreen：
// 后者在 WDA 中是免 session 路由（带 session 前缀会 404），且在 iOS 27.x 上会阻塞约 60s。
func TestUIButtonHomeUsesWDAPressButton(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":null,"sessionId":"S1"}`))
	}))
	defer server.Close()

	c := uiClient{
		driver:        uiDriverWDA,
		wdaURL:        server.URL,
		httpClient:    server.Client(),
		wdaHTTPClient: server.Client(),
		sessionID:     "S1",
	}
	c.button("home")

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if want := "/session/S1/wda/pressButton"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
	if gotBody["name"] != "home" {
		t.Fatalf("body = %v, want name=home", gotBody)
	}
}
