package write

import (
	"context"
	"time"
)

type timingKey struct{}

// Timings collects sequential stages of one write request for diagnostic logging.
type Timings struct {
	stages map[string]time.Duration
}

func WithTimings(ctx context.Context) (context.Context, *Timings) {
	t := &Timings{stages: make(map[string]time.Duration)}
	return context.WithValue(ctx, timingKey{}, t), t
}

func RecordTiming(ctx context.Context, stage string, elapsed time.Duration) {
	if t, ok := ctx.Value(timingKey{}).(*Timings); ok {
		t.stages[stage] += elapsed
	}
}

func (t *Timings) Stages() map[string]time.Duration { return t.stages }
