package main

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store persists encrypted secrets in Redis. Each secret is one hash with a
// TTL, so expiry is enforced by Redis itself rather than by application code
// that could be skipped.
type Store struct {
	rdb *redis.Client
}

const keyPrefix = "vault:"

var errNotFound = errors.New("vault: secret not found")

// Record is the metadata a viewer may see before deciding to open a secret.
type Record struct {
	Kind      string
	Views     int
	MaxViews  int
	ExpiresAt time.Time
}

// consume atomically increments the view counter and deletes the secret once
// the final view is used. Doing this in a single Redis script is what makes
// "one-time access" true under concurrency; a read-then-write in Go would let
// two simultaneous requests both pass the check.
var consumeScript = redis.NewScript(`
	if redis.call('EXISTS', KEYS[1]) == 0 then
		return nil
	end
	local views = redis.call('HINCRBY', KEYS[1], 'views', 1)
	local max = tonumber(redis.call('HGET', KEYS[1], 'max'))
	if views >= max then
		redis.call('DEL', KEYS[1])
	end
	return views
`)

func (s *Store) Put(ctx context.Context, id, ciphertext, kind string, maxViews int, ttl time.Duration) error {
	key := keyPrefix + id
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, map[string]any{
		"ct":    ciphertext,
		"kind":  kind,
		"max":   maxViews,
		"views": 0,
		"exp":   time.Now().Add(ttl).Unix(),
	})
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// Peek returns metadata without spending a view.
func (s *Store) Peek(ctx context.Context, id string) (Record, error) {
	vals, err := s.rdb.HMGet(ctx, keyPrefix+id, "kind", "views", "max", "exp").Result()
	if err != nil {
		return Record{}, err
	}
	if vals[0] == nil {
		return Record{}, errNotFound
	}
	return Record{
		Kind:      asString(vals[0]),
		Views:     asInt(vals[1]),
		MaxViews:  asInt(vals[2]),
		ExpiresAt: time.Unix(int64(asInt(vals[3])), 0).UTC(),
	}, nil
}

// Ciphertext reads the stored blob without spending a view, so a wrong key can
// be rejected before the view counter moves.
func (s *Store) Ciphertext(ctx context.Context, id string) (string, error) {
	ct, err := s.rdb.HGet(ctx, keyPrefix+id, "ct").Result()
	if errors.Is(err, redis.Nil) {
		return "", errNotFound
	}
	return ct, err
}

// Consume spends one view and reports how many have now been used.
func (s *Store) Consume(ctx context.Context, id string) (used int, err error) {
	res, err := consumeScript.Run(ctx, s.rdb, []string{keyPrefix + id}).Result()
	if errors.Is(err, redis.Nil) {
		return 0, errNotFound
	}
	if err != nil {
		return 0, err
	}
	n, ok := res.(int64)
	if !ok {
		return 0, errNotFound
	}
	return int(n), nil
}

// Burn deletes a secret immediately.
func (s *Store) Burn(ctx context.Context, id string) error {
	n, err := s.rdb.Del(ctx, keyPrefix+id).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.rdb.Ping(ctx).Err()
}

// Redis hash fields come back as strings; these helpers avoid the unchecked
// type assertions that previously panicked on unexpected data.
func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
