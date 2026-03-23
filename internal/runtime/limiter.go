package runtime

import (
	"context"
	"math"

	"golang.org/x/time/rate"
)

func newRateLimiter(mbps float64) *rate.Limiter {
	if mbps <= 0 {
		return nil
	}

	bytesPerSec := mbps * 1_000_000 / 8
	burst := int(math.Max(1024, bytesPerSec))
	return rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

func waitLimiter(ctx context.Context, l *rate.Limiter, n int) error {
	if l == nil || n <= 0 {
		return nil
	}
	return l.WaitN(ctx, n)
}
