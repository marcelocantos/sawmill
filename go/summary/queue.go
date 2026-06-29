// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package summary

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// QueueConfig configures one summariser run. WorkDir is the project root
// passed to claudia. CostCapUSD is a hard ceiling — when the running total
// crosses it, the queue stops cleanly and the in-progress symbol's
// (partial) cost is still counted.
type QueueConfig struct {
	WorkDir       string
	Model         string  // "" -> claudia default
	CostCapUSD    float64 // 0 means "no cap" (the QueueRunner will warn)
	MaxRetries    int     // per symbol; 0 -> 2
	BackoffFactor float64 // exponential factor; 0 -> 2.0
	BaseBackoff   time.Duration
}

// QueueRunner sequences Summarise calls. Concurrency is intentionally
// fixed at 1: claude CLI invocations are heavy, and the goal is steady
// background progress, not throughput.
type QueueRunner struct {
	cfg     QueueConfig
	costTot atomic.Uint64 // micro-USD; uint64 lets us use atomic ops cleanly
}

// NewQueueRunner returns a QueueRunner with sane defaults.
func NewQueueRunner(cfg QueueConfig) *QueueRunner {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 2
	}
	if cfg.BackoffFactor == 0 {
		cfg.BackoffFactor = 2.0
	}
	if cfg.BaseBackoff == 0 {
		cfg.BaseBackoff = 2 * time.Second
	}
	return &QueueRunner{cfg: cfg}
}

// CostUSD reports the running total cost in dollars.
func (q *QueueRunner) CostUSD() float64 {
	return float64(q.costTot.Load()) / 1_000_000.0
}

// addCost accumulates the cost spent on one summarisation. Cost is tracked
// in integer micro-USD so the atomic op stays lock-free.
func (q *QueueRunner) addCost(usd float64) {
	if usd <= 0 {
		return
	}
	micro := uint64(usd * 1_000_000)
	q.costTot.Add(micro)
}

// ErrCostCap signals that the queue stopped because the cost cap was hit.
var ErrCostCap = errors.New("summariser cost cap reached")

// Item is one candidate the QueueRunner consumes from the caller. Callbacks
// (OnResult / OnFailure) report outcomes so the caller can persist them.
type Item struct {
	SymbolID  int64
	FilePath  string
	Name      string
	Kind      string
	Signature string
	Body      string
}

// Sink receives outcomes from the queue. Returning a non-nil error from
// OnResult/OnFailure does NOT stop the queue — it's logged and processing
// continues. Use ctx cancellation to stop early.
type Sink interface {
	OnResult(item Item, res Result) error
	OnFailure(item Item, reason string, retryCount int) error
}

// Run consumes items one-by-one until ctx is cancelled, the cost cap is
// reached, or `next` returns nil. `next` is called whenever the queue is
// ready for more work, allowing the caller to dynamically discover new
// candidates (e.g. as new files are indexed by the watcher).
func (q *QueueRunner) Run(ctx context.Context, sink Sink, next func() *Item) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if q.cfg.CostCapUSD > 0 && q.CostUSD() >= q.cfg.CostCapUSD {
			return ErrCostCap
		}
		item := next()
		if item == nil {
			return nil
		}
		if err := q.processOne(ctx, sink, *item); err != nil && errors.Is(err, ErrCostCap) {
			return err
		}
	}
}

func (q *QueueRunner) processOne(ctx context.Context, sink Sink, item Item) error {
	backoff := q.cfg.BaseBackoff
	for attempt := 0; attempt <= q.cfg.MaxRetries; attempt++ {
		req := Request{
			ID:        fmt.Sprintf("sawmill-summary-%d-%d", item.SymbolID, time.Now().UnixNano()),
			WorkDir:   q.cfg.WorkDir,
			Model:     q.cfg.Model,
			FilePath:  item.FilePath,
			Name:      item.Name,
			Kind:      item.Kind,
			Signature: item.Signature,
			Body:      item.Body,
		}
		res, err := Summarise(ctx, req)
		q.addCost(res.CostUSD)
		if q.cfg.CostCapUSD > 0 && q.CostUSD() >= q.cfg.CostCapUSD {
			if err == nil {
				_ = sink.OnResult(item, res)
			}
			return ErrCostCap
		}
		if err == nil {
			if cbErr := sink.OnResult(item, res); cbErr != nil {
				log.Printf("sawmill: persist summary for %s: %v", item.Name, cbErr)
			}
			return nil
		}
		_ = sink.OnFailure(item, err.Error(), attempt)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = time.Duration(float64(backoff) * q.cfg.BackoffFactor)
	}
	return nil
}
