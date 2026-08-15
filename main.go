package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

const (
	maxKeyLen   = 4096
	maxValueLen = 1 << 20 // 1 MiB
)

type config struct {
	Port            string
	GarnetAddr      string
	GarnetPass      string
	GarnetDB        int
	RequestTTL      time.Duration
	LRUIdleTTL      time.Duration
	RedisPoolSize   int
	HTTPConcurrency int
}

type server struct {
	cfg config
	db  *redis.Client
	log *slog.Logger
}

type setRequest struct {
	Value      *string `json:"value"`
	TTLSeconds *int64  `json:"ttl_seconds,omitempty"`
}

type valueResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type statusResponse struct {
	Key     string `json:"key"`
	Status  string `json:"status,omitempty"`
	Removed bool   `json:"removed,omitempty"`
}

type healthResponse struct {
	Status string `json:"status"`
	Garnet string `json:"garnet"`
}

func main() {
	cfg := loadConfig()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	db := redis.NewClient(&redis.Options{
		Addr:         cfg.GarnetAddr,
		Password:     cfg.GarnetPass,
		DB:           cfg.GarnetDB,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  cfg.RequestTTL,
		WriteTimeout: cfg.RequestTTL,
		PoolSize:     cfg.RedisPoolSize,
		MinIdleConns: runtime.GOMAXPROCS(0),
		PoolTimeout:  cfg.RequestTTL,
	})

	srv := &server{cfg: cfg, db: db, log: logger}
	app := newApp(srv)

	go func() {
		addr := ":" + cfg.Port
		logger.Info("http server starting", slog.String("addr", addr))
		if err := app.Listen(addr); err != nil {
			logger.Error("http server stopped", slog.String("err", err.Error()))
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	logger.Info("shutdown signal received")
	shutdownStart := time.Now()
	if err := app.ShutdownWithTimeout(5 * time.Second); err != nil {
		logger.Error("http shutdown error", slog.String("err", err.Error()))
	}
	if err := db.Close(); err != nil {
		logger.Error("close garnet client", slog.String("err", err.Error()))
	}
	logger.Info("shutdown complete", slog.Duration("elapsed", time.Since(shutdownStart)))
}

// newApp builds the Fiber app with the canonical routes and error handler. Used by
// both main() and integration tests so the wiring stays identical.
func newApp(srv *server) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:     "garnet-api",
		Concurrency: srv.cfg.HTTPConcurrency,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fiberErr *fiber.Error
			if errors.As(err, &fiberErr) {
				code = fiberErr.Code
			}
			srv.log.Warn("request error",
				slog.String("method", c.Method()),
				slog.String("path", c.Path()),
				slog.Int("status", code),
				slog.String("err", err.Error()),
			)
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	app.Get("/healthz", srv.health)
	app.Get("/keys/:key", srv.get)
	app.Put("/keys/:key", srv.set)
	app.Delete("/keys/:key", srv.remove)
	return app
}

func (s *server) health(c fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RequestTTL)
	defer cancel()
	if err := s.db.Ping(ctx).Err(); err != nil {
		s.log.Warn("health garnet ping failed", slog.String("err", err.Error()))
		return c.Status(fiber.StatusServiceUnavailable).JSON(healthResponse{Status: "unhealthy", Garnet: err.Error()})
	}
	return c.JSON(healthResponse{Status: "ok", Garnet: "pong"})
}

func (s *server) get(c fiber.Ctx) error {
	key, err := url.PathUnescape(c.Params("key"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid key")
	}
	if err := validateKey(key); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RequestTTL)
	defer cancel()

	// Access-refreshed LRU eviction: when LRUIdleTTL > 0, GETEX refreshes the key's
	// idle TTL on every successful read so recently used keys stay alive and idle keys
	// are expired by Garnet. When LRUIdleTTL is 0, no TTL is applied — keys live
	// until explicitly deleted or expired via the per-request ttl_seconds.
	var cmd *redis.StringCmd
	if s.cfg.LRUIdleTTL > 0 {
		cmd = s.db.GetEx(ctx, key, s.cfg.LRUIdleTTL)
	} else {
		cmd = s.db.Get(ctx, key)
	}
	value, err := cmd.Result()
	if errors.Is(err, redis.Nil) {
		return fiber.NewError(fiber.StatusNotFound, "key not found")
	}
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}

	return c.JSON(valueResponse{Key: key, Value: value})
}

func (s *server) set(c fiber.Ctx) error {
	key, err := url.PathUnescape(c.Params("key"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid key")
	}
	if err := validateKey(key); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	var req setRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid json body")
	}
	if req.Value == nil {
		return fiber.NewError(fiber.StatusBadRequest, "value is required")
	}
	if len(*req.Value) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "value must not be empty")
	}
	if len(*req.Value) > maxValueLen {
		return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("value exceeds %d bytes", maxValueLen))
	}

	ttl, err := s.ttlForSet(req.TTLSeconds)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RequestTTL)
	defer cancel()
	if err := s.db.Set(ctx, key, *req.Value, ttl).Err(); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}

	return c.JSON(statusResponse{Key: key, Status: "ok"})
}

func (s *server) remove(c fiber.Ctx) error {
	key, err := url.PathUnescape(c.Params("key"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid key")
	}
	if err := validateKey(key); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RequestTTL)
	defer cancel()

	count, err := s.db.Del(ctx, key).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}

	return c.JSON(statusResponse{Key: key, Removed: count > 0})
}

func (s *server) ttlForSet(ttlSeconds *int64) (time.Duration, error) {
	if ttlSeconds != nil {
		if *ttlSeconds < 0 {
			return 0, errors.New("ttl_seconds must be greater than or equal to 0")
		}
		return time.Duration(*ttlSeconds) * time.Second, nil
	}
	return s.cfg.LRUIdleTTL, nil
}

func validateKey(key string) error {
	if key == "" {
		return errors.New("key must not be empty")
	}
	if len(key) > maxKeyLen {
		return fmt.Errorf("key exceeds %d bytes", maxKeyLen)
	}
	return nil
}

func loadConfig() config {
	cpus := runtime.GOMAXPROCS(0)
	return config{
		Port:            envString("PORT", "3000"),
		GarnetAddr:      envString("GARNET_ADDR", "127.0.0.1:6379"),
		GarnetPass:      os.Getenv("GARNET_PASSWORD"),
		GarnetDB:        envInt("GARNET_DB", 0),
		RequestTTL:      time.Duration(envInt("REQUEST_TIMEOUT_MS", 500)) * time.Millisecond,
		LRUIdleTTL:      time.Duration(envInt("LRU_IDLE_TTL_SECONDS", 0)) * time.Second,
		RedisPoolSize:   envInt("REDIS_POOL_SIZE", cpus*32),
		HTTPConcurrency: envInt("HTTP_CONCURRENCY", 256*1024),
	}
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}
