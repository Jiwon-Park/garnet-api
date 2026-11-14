package main

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const (
	maxTranslateTextLen    = 64 << 10 // 64 KiB of UTF-8 per request
	maxTranslateContextLen = 64 << 10
)

var (
	errTranslatorUnavailable = errors.New("translator unavailable")
	errTranslateFailed       = errors.New("translation failed")
)

type translateRequest struct {
	Text    *string `json:"text"`
	Context string  `json:"context,omitempty"`
}

type translateResponse struct {
	Text string `json:"text"`
}

// translate handles POST /translate. Text may hold several "\n"-separated lines; the
// engine translates them as one document. Optional context gives the lines that come
// before it so that names, speech and quotes carry over.
func (s *server) translate(c fiber.Ctx) error {
	if s.translator == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, errTranslatorUnavailable.Error())
	}

	var req translateRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid json body")
	}
	if req.Text == nil || strings.TrimSpace(*req.Text) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "text is required")
	}
	if len(*req.Text) > maxTranslateTextLen {
		return fiber.NewError(fiber.StatusBadRequest, "text too large")
	}
	if len(req.Context) > maxTranslateContextLen {
		return fiber.NewError(fiber.StatusBadRequest, "context too large")
	}

	out, err := s.translator.Translate(req.Context, *req.Text)
	if err != nil {
		if errors.Is(err, errTranslatorUnavailable) {
			return fiber.NewError(fiber.StatusServiceUnavailable, err.Error())
		}
		s.log.Error("translate", slog.String("err", err.Error()))
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(translateResponse{Text: out})
}
