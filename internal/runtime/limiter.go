package runtime

import (
	"context"
	"math"

	"golang.org/x/time/rate"
)

func newRateLimiter(kbps float64) *rate.Limiter {
	if kbps <= 0 {
		return nil
	}
	bps := kbps * 1024
	burst := int(math.Max(1024, bps))
	return rate.NewLimiter(rate.Limit(bps), burst)
}

func waitLimiter(ctx context.Context, l *rate.Limiter, n int) error {
	if l == nil || n <= 0 {
		return nil
	}
	return l.WaitN(ctx, n)
}
