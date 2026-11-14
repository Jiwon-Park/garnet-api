package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func doChat(t *testing.T, srv *server, auth, body string) (int, string) {
	t.Helper()
	app := newApp(srv)
	req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func userBody(content string) string {
	b, _ := json.Marshal(map[string]any{
		"model":    "x",
		"messages": []map[string]any{{"role": "user", "content": content}},
	})
	return string(b)
}

func TestParseBatchItemsObject(t *testing.T) {
	got, err := parseBatchItems(`{"10":"c","2":"b","0":"a"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "a|b|c" {
		t.Fatalf("got %q, want a|b|c", got)
	}
}

func TestParseBatchItemsArrayAndPlain(t *testing.T) {
	got, err := parseBatchItems(`["x","y"]`)
	if err != nil || len(got) != 2 || got[1] != "y" {
		t.Fatalf("array: got %q err %v", got, err)
	}
	got, err = parseBatchItems("くそっ！")
	if err != nil || len(got) != 1 || got[0] != "くそっ！" {
		t.Fatalf("plain: got %q err %v", got, err)
	}
}

func TestParseBatchItemsRejectsNonIndexKeys(t *testing.T) {
	if _, err := parseBatchItems(`{"a":"x"}`); err == nil {
		t.Fatal("expected error for non-numeric key")
	}
}

func TestChatCompletionsValidation(t *testing.T) {
	srv := &server{log: slog.Default(), translator: &translator{}}

	big := make(map[string]string, maxChatItems+1)
	for i := 0; i <= maxChatItems; i++ {
		big[strconv.Itoa(i)] = "a"
	}
	bigJSON, _ := json.Marshal(big)

	cases := map[string]string{
		"invalid json":  `not json`,
		"no user":       `{"messages":[{"role":"system","content":"hi"}]}`,
		"bad batch key": userBody(`{"x":"a"}`),
		"too many":      userBody(string(bigJSON)),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			code, out := doChat(t, srv, "", body)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", code, out)
			}
		})
	}
}

func TestChatCompletionsUnavailableWithoutEngine(t *testing.T) {
	srv := &server{log: slog.Default()}
	code, _ := doChat(t, srv, "", userBody(`{"0":"a"}`))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
}

func TestChatCompletionsAPIKey(t *testing.T) {
	srv := &server{log: slog.Default(), cfg: config{TranslateAPIKey: "secret"}}
	body := userBody(`{"0":"a"}`)

	if code, _ := doChat(t, srv, "", body); code != http.StatusUnauthorized {
		t.Fatalf("no key: status = %d, want 401", code)
	}
	if code, _ := doChat(t, srv, "wrong", body); code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status = %d, want 401", code)
	}
	// A correct key passes auth, then fails on the empty translator with 503.
	if code, _ := doChat(t, srv, "Bearer secret", body); code != http.StatusServiceUnavailable {
		t.Fatalf("bearer key: status = %d, want 503", code)
	}
	if code, _ := doChat(t, srv, "secret", body); code != http.StatusServiceUnavailable {
		t.Fatalf("bare key: status = %d, want 503", code)
	}
}
