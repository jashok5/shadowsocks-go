package runtime

import (
	"context"
	"time"
)

func runPeriodic(ctx context.Context, interval time.Duration, onTick func(time.Time), onStop func()) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if onStop != nil {
				onStop()
			}
			return
		case now := <-ticker.C:
			if onTick != nil {
				onTick(now)
			}
		}
	}
}
