package main

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func doTranslate(t *testing.T, srv *server, body string) (int, string) {
	t.Helper()
	app := newApp(srv)
	req, err := http.NewRequest(http.MethodPost, "/translate", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestTranslateUnavailableWithoutEngine(t *testing.T) {
	srv := &server{log: slog.Default()}
	code, _ := doTranslate(t, srv, `{"text":"今日は本を読みます。"}`)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
}

func TestTranslateRejectsBadRequests(t *testing.T) {
	// Validation runs before the engine is called, so an empty translator is enough.
	srv := &server{log: slog.Default(), translator: &translator{}}

	cases := []struct {
		name string
		body string
	}{
		{"invalid json", `not json`},
		{"missing text", `{}`},
		{"blank text", `{"text":"   \n  "}`},
		{"text too large", `{"text":"` + strings.Repeat("あ", maxTranslateTextLen/3+1) + `"}`},
		{"context too large", `{"text":"今日","context":"` + strings.Repeat("あ", maxTranslateContextLen/3+1) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doTranslate(t, srv, tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", code, body)
			}
		})
	}
}
