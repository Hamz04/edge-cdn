package cache

import (
	"container/list"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

type Entry struct {
	Key       string
	Value     []byte
	TTL       time.Duration
	CreatedAt time.Time
}

type Stats struct {
	Hits       atomic.Int64
	Misses     atomic.Int64
	Sets       atomic.Int64
	Deletes    atomic.Int64
	Evictions  atomic.Int64
	Errors     atomic.Int64
	EntryCount atomic.Int64
}

type StatsSnapshot struct {
	Hits       int64   `json:"hits"`
	Misses     int64   `json:"misses"`
	Sets       int64   `json:"sets"`
	Deletes    int64   `json:"deletes"`
	Evictions  int64   `json:"evictions"`
	Errors     int64   `json:"errors"`
	HitRatio   float64 `json:"hit_ratio"`
	EntryCount int64   `json:"entry_count"`
}

func (s *Stats) Snapshot() StatsSnapshot {
	hits := s.Hits.Load()
	misses := s.Misses.Load()
	total := hits + misses
	var ratio float64
	if total > 0 {
		ratio = float64(hits) / float64(total)
	}
	return StatsSnapshot{
		Hits: hits, Misses: misses, Sets: s.Sets.Load(),
		Deletes: s.Deletes.Load(), Evictions: s.Evictions.Load(),
		Errors: s.Errors.Load(), HitRatio: ratio, EntryCount: s.EntryCount.Load(),
	}
}

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	GetStats() StatsSnapshot
	Close() error
}

type RedisCache struct {
	client    *redis.Client
	fallback  *LRUCache
	stats     Stats
	logger    *slog.Logger
	redisOK   atomic.Bool
	probeMu   sync.Mutex
	lastProbe time.Time
}

type RedisConfig struct {
	URL          string
	MaxRetries   int
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		URL: "localhost:6379", MaxRetries: 3, PoolSize: 50,
		MinIdleConns: 10, DialTimeout: 5 * time.Second,
		ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
}

func NewRedisCache(cfg RedisConfig, fallbackSize int, logger *slog.Logger) *RedisCache {
	opts, err := redis.ParseURL(fmt.Sprintf("redis://%s", cfg.URL))
	if err != nil {
		opts = &redis.Options{Addr: cfg.URL}
	}
	opts.MaxRetries = cfg.MaxRetries
	opts.PoolSize = cfg.PoolSize
	opts.MinIdleConns = cfg.MinIdleConns
	opts.DialTimeout = cfg.DialTimeout
	opts.ReadTimeout = cfg.ReadTimeout
	opts.WriteTimeout = cfg.WriteTimeout
	client := redis.NewClient(opts)
	rc := &RedisCache{client: client, fallback: NewLRUCache(fallbackSize), logger: logger}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		logger.Warn("redis unavailable at startup, using LRU fallback", "addr", cfg.URL, "error", err)
		rc.redisOK.Store(false)
	} else {
		logger.Info("redis connected", "addr", cfg.URL)
		rc.redisOK.Store(true)
	}
	return rc
}

// Config is a simplified config used by the Stage 3 main.go constructor.
type Config struct {
	RedisURL   string
	LRUSize    int
	DefaultTTL time.Duration
}

// New creates a cache using the simplified Config. This wraps NewRedisCache
// for backward compatibility with the Stage 3 integration.
func New(cfg Config, logger *slog.Logger) *RedisCache {
	rcfg := DefaultRedisConfig()
	if cfg.RedisURL != "" {
		rcfg.URL = cfg.RedisURL
	}
	fallbackSize := cfg.LRUSize
	if fallbackSize <= 0 {
		fallbackSize = 10000
	}
	return NewRedisCache(rcfg, fallbackSize, logger)
}

func (rc *RedisCache) probeRedis() bool {
	rc.probeMu.Lock()
	defer rc.probeMu.Unlock()
	if time.Since(rc.lastProbe) < 5*time.Second {
		return rc.redisOK.Load()
	}
	rc.lastProbe = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := rc.client.Ping(ctx).Err(); err != nil {
		return false
	}
	rc.redisOK.Store(true)
	rc.logger.Info("redis connection restored")
	return true
}

func (rc *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	if rc.redisOK.Load() {
		val, err := rc.client.Get(ctx, key).Bytes()
		if err == nil {
			rc.stats.Hits.Add(1)
			return val, nil
		}
		if err != redis.Nil {
			rc.stats.Errors.Add(1)
			rc.redisOK.Store(false)
			rc.logger.Warn("redis get failed", "key", key, "error", err)
		}
	} else {
		go rc.probeRedis()
	}
	if val, ok := rc.fallback.Get(key); ok {
		rc.stats.Hits.Add(1)
		return val, nil
	}
	rc.stats.Misses.Add(1)
	return nil, ErrCacheMiss
}

func (rc *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	rc.stats.Sets.Add(1)
	evicted := rc.fallback.Set(key, value, ttl)
	if evicted {
		rc.stats.Evictions.Add(1)
	}
	rc.stats.EntryCount.Store(int64(rc.fallback.Len()))
	if rc.redisOK.Load() {
		if err := rc.client.Set(ctx, key, value, ttl).Err(); err != nil {
			rc.stats.Errors.Add(1)
			rc.redisOK.Store(false)
			rc.logger.Warn("redis set failed", "key", key, "error", err)
			return nil
		}
		if size, err := rc.client.DBSize(ctx).Result(); err == nil {
			rc.stats.EntryCount.Store(size)
		}
	}
	return nil
}

func (rc *RedisCache) Delete(ctx context.Context, key string) error {
	rc.stats.Deletes.Add(1)
	rc.fallback.Delete(key)
	if rc.redisOK.Load() {
		if err := rc.client.Del(ctx, key).Err(); err != nil {
			rc.stats.Errors.Add(1)
			rc.logger.Warn("redis delete failed", "key", key, "error", err)
		}
	}
	return nil
}

func (rc *RedisCache) GetStats() StatsSnapshot { return rc.stats.Snapshot() }
func (rc *RedisCache) Close() error            { return rc.client.Close() }

// --- LRU Cache ---

type lruEntry struct {
	key       string
	value     []byte
	expiresAt time.Time
}

type LRUCache struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	order    *list.List
}

func NewLRUCache(capacity int) *LRUCache {
	if capacity <= 0 {
		capacity = 10000
	}
	return &LRUCache{capacity: capacity, items: make(map[string]*list.Element, capacity), order: list.New()}
}

func (l *LRUCache) Get(key string) ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	elem, ok := l.items[key]
	if !ok {
		return nil, false
	}
	entry := elem.Value.(*lruEntry)
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		l.removeElement(elem)
		return nil, false
	}
	l.order.MoveToFront(elem)
	return entry.value, true
}

func (l *LRUCache) Set(key string, value []byte, ttl time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	if elem, ok := l.items[key]; ok {
		entry := elem.Value.(*lruEntry)
		entry.value = value
		entry.expiresAt = expiresAt
		l.order.MoveToFront(elem)
		return false
	}
	evicted := false
	if l.order.Len() >= l.capacity {
		l.evictOldest()
		evicted = true
	}
	entry := &lruEntry{key: key, value: value, expiresAt: expiresAt}
	elem := l.order.PushFront(entry)
	l.items[key] = elem
	return evicted
}

func (l *LRUCache) Delete(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if elem, ok := l.items[key]; ok {
		l.removeElement(elem)
	}
}

func (l *LRUCache) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.order.Len()
}

func (l *LRUCache) evictOldest() {
	oldest := l.order.Back()
	if oldest != nil {
		l.removeElement(oldest)
	}
}

func (l *LRUCache) removeElement(elem *list.Element) {
	entry := elem.Value.(*lruEntry)
	delete(l.items, entry.key)
	l.order.Remove(elem)
}

var ErrCacheMiss = fmt.Errorf("cache miss")
