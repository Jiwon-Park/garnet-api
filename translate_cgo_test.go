//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
)

// The engine loads once for the whole test binary; newTranslator is safe to call again.
func newTestServer(t *testing.T) *server {
	t.Helper()
	tr, err := newTranslator(4)
	if err != nil {
		t.Fatalf("newTranslator: %v", err)
	}
	return &server{log: slog.Default(), translator: tr}
}

func TestTranslateReturnsKorean(t *testing.T) {
	srv := newTestServer(t)

	cases := map[string]string{
		"今日は本を読みます。":   "오늘은 책을 읽습니다.",
		"肉がジュージューと焼ける": "고기가 지글지글 구워진다",
		"くそっ！":         "젠장!",
	}
	for in, want := range cases {
		code, body := doTranslate(t, srv, fmt.Sprintf(`{"text":%q}`, in))
		if code != http.StatusOK {
			t.Fatalf("%q: status = %d, body %s", in, code, body)
		}
		var got translateResponse
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("%q: bad json %s: %v", in, body, err)
		}
		if got.Text != want {
			t.Fatalf("%q: got %q, want %q", in, got.Text, want)
		}
	}
}

func TestTranslateMultiLineKeepsLineCount(t *testing.T) {
	srv := newTestServer(t)
	code, body := doTranslate(t, srv, `{"text":"今日は本を読みます。\n肉がジュージューと焼ける\nくそっ！"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %s", code, body)
	}
	var got translateResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	want := "오늘은 책을 읽습니다.\n고기가 지글지글 구워진다\n젠장!"
	if got.Text != want {
		t.Fatalf("got %q, want %q", got.Text, want)
	}
}

func TestTranslateWithContext(t *testing.T) {
	srv := newTestServer(t)
	code, body := doTranslate(t, srv, `{"text":"くそっ！","context":"今日は本を読みます。"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %s", code, body)
	}
	var got translateResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Text == "" {
		t.Fatal("empty translation with context")
	}
}

// Many goroutines share one engine and one translator. Every answer must match the
// single-threaded answer, which shows the calls are safe to run side by side.
func TestTranslateConcurrentRequests(t *testing.T) {
	srv := newTestServer(t)
	const input = "今日は本を読みます。"
	const want = "오늘은 책을 읽습니다."

	const workers = 32
	const perWorker = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				out, err := srv.translator.Translate("", input)
				if err != nil {
					errs <- err
					return
				}
				if out != want {
					errs <- fmt.Errorf("got %q, want %q", out, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
