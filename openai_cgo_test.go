//go:build cgo

package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func TestChatCompletionsBatchTranslation(t *testing.T) {
	srv := newTestServer(t)
	// Out-of-order keys and an empty item: the reply keeps index order and the empty item stays empty.
	body := userBody(`{"1":"くそっ！","0":"今日は本を読みます。","2":""}`)
	code, out := doChat(t, srv, "", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %s", code, out)
	}
	var r chatResp
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Choices) != 1 {
		t.Fatalf("bad response %s: %v", out, err)
	}
	var content struct {
		Translations []string `json:"translations"`
	}
	if err := json.Unmarshal([]byte(r.Choices[0].Message.Content), &content); err != nil {
		t.Fatalf("content is not translations JSON: %q: %v", r.Choices[0].Message.Content, err)
	}
	want := []string{"오늘은 책을 읽습니다.", "젠장!", ""}
	if len(content.Translations) != len(want) {
		t.Fatalf("got %q, want %q", content.Translations, want)
	}
	for i := range want {
		if content.Translations[i] != want[i] {
			t.Fatalf("item %d: got %q, want %q", i, content.Translations[i], want[i])
		}
	}
}
