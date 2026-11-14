package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	maxChatItems = 256 // texts per batch request
)

// Only the fields the batch translator needs are decoded. Everything else in the
// request (model, temperature, response_format, ...) is ignored.
type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// chatCompletions handles POST /v1/chat/completions for batch translation clients.
//
// The last user message carries the batch as a JSON object {"0": "...", "1": "..."}
// (an array of strings also works). Each value is translated separately. The reply
// is an OpenAI-style completion whose assistant content is the JSON string
// {"translations": ["...", "..."]}, in index order.
func (s *server) chatCompletions(c fiber.Ctx) error {
	if err := s.authorizeTranslate(c); err != nil {
		return err
	}
	if s.translator == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, errTranslatorUnavailable.Error())
	}

	var req chatCompletionRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid json body")
	}

	userContent, ok := lastUserContent(req.Messages)
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, "no user message")
	}
	items, err := parseBatchItems(userContent)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if len(items) > maxChatItems {
		return fiber.NewError(fiber.StatusBadRequest, "too many items")
	}
	for _, text := range items {
		if len(text) > maxTranslateTextLen {
			return fiber.NewError(fiber.StatusBadRequest, "text too large")
		}
	}

	translated, err := s.translateBatch(items)
	if err != nil {
		s.log.Error("translate batch", "err", err.Error())
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	payload, err := json.Marshal(struct {
		Translations []string `json:"translations"`
	}{Translations: translated})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "encode failed")
	}

	now := time.Now()
	return c.JSON(chatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", now.UnixNano()),
		Object:  "chat.completion",
		Created: now.Unix(),
		Model:   req.Model,
		Choices: []chatChoice{{
			Index:        0,
			Message:      chatMessage{Role: "assistant", Content: string(payload)},
			FinishReason: "stop",
		}},
	})
}

// authorizeTranslate checks the Authorization header against TRANSLATE_API_KEY. The
// header may hold the bare key or "Bearer <key>". With no key configured, all requests
// pass.
func (s *server) authorizeTranslate(c fiber.Ctx) error {
	key := s.cfg.TranslateAPIKey
	if key == "" {
		return nil
	}
	got := c.Get(fiber.HeaderAuthorization)
	if subtle.ConstantTimeCompare([]byte(got), []byte(key)) == 1 ||
		subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+key)) == 1 {
		return nil
	}
	return fiber.NewError(fiber.StatusUnauthorized, "unauthorized")
}

// lastUserContent returns the content of the last message with role "user". Content
// may be a plain string or an array of parts; text parts are joined.
func lastUserContent(messages []chatMessage) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		switch v := messages[i].Content.(type) {
		case string:
			return v, true
		case []any:
			var b strings.Builder
			for _, part := range v {
				m, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := m["text"].(string); t != "" {
					b.WriteString(t)
				}
			}
			return b.String(), true
		default:
			return "", false
		}
	}
	return "", false
}

// parseBatchItems reads the batch from the user content. A JSON object keyed by index
// is ordered by its numeric keys. A JSON array of strings is taken in order. Any other
// text is one item.
func parseBatchItems(content string) ([]string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, errors.New("empty user message")
	}

	var obj map[string]string
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &obj) == nil {
		keys := make([]int, 0, len(obj))
		byIndex := make(map[int]string, len(obj))
		for k, v := range obj {
			n, err := strconv.Atoi(k)
			if err != nil {
				return nil, fmt.Errorf("batch key %q is not an index", k)
			}
			keys = append(keys, n)
			byIndex[n] = v
		}
		sort.Ints(keys)
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			out = append(out, byIndex[k])
		}
		return out, nil
	}

	var arr []string
	if strings.HasPrefix(trimmed, "[") && json.Unmarshal([]byte(trimmed), &arr) == nil {
		return arr, nil
	}

	return []string{content}, nil
}

// translateBatch translates each item. Items run concurrently; the engine semaphore in
// translator bounds the number of engine calls. Empty items come back empty.
func (s *server) translateBatch(items []string) ([]string, error) {
	out := make([]string, len(items))
	errs := make([]error, len(items))

	var wg sync.WaitGroup
	for i, text := range items {
		if strings.TrimSpace(text) == "" {
			continue
		}
		wg.Add(1)
		go func(i int, text string) {
			defer wg.Done()
			out[i], errs[i] = s.translator.Translate("", text)
		}(i, text)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
