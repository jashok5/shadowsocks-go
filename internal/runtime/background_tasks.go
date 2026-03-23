package runtime

import (
	"context"
	"sync"
	"time"
)

func startBackgroundTasks(wg *sync.WaitGroup, tasks ...func()) {
	if wg == nil {
		return
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		t := task
		wg.Go(func() {
			t()
		})
	}
}

func startPeriodicBackground(wg *sync.WaitGroup, ctx context.Context, interval time.Duration, onTick func(time.Time), onStop func()) {
	if wg == nil {
		return
	}
	wg.Go(func() {
		runPeriodic(ctx, interval, onTick, onStop)
	})
}
