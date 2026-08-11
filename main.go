package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
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
	Removed bool   `json:"removed,omitempty"`
	Status  string `json:"status,omitempty"`
}

func main() {
	cfg := loadConfig()
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
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close redis client: %v", err)
		}
	}()

	srv := &server{cfg: cfg, db: db}
	app := fiber.New(fiber.Config{
		AppName:     "garnet-api",
		Concurrency: cfg.HTTPConcurrency,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fiberErr *fiber.Error
			if errors.As(err, &fiberErr) {
				code = fiberErr.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	app.Get("/keys/:key", srv.get)
	app.Put("/keys/:key", srv.set)
	app.Delete("/keys/:key", srv.remove)

	go func() {
		addr := ":" + cfg.Port
		if err := app.Listen(addr); err != nil {
			log.Printf("http server stopped: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	if err := app.ShutdownWithTimeout(5 * time.Second); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func (s *server) get(c fiber.Ctx) error {
	key := c.Params("key")
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RequestTTL)
	defer cancel()

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
	key := c.Params("key")
	var req setRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid json body")
	}
	if req.Value == nil {
		return fiber.NewError(fiber.StatusBadRequest, "value is required")
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
	key := c.Params("key")
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
