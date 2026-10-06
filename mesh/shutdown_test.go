package mesh

import (
	"context"
	"testing"
	"time"
)

// BUGLOG RS26: shutdown cancels the poll loop and waits for it before the
// goodbye, so no tick re-announces afterwards.
func TestShutdownWaitsForThePollLoop(t *testing.T) {
	kit := newKit(t, func(o *Options) { o.PollEvery = 10 * time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	kit.dev.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	start := time.Now()
	kit.dev.Wait(2 * time.Second)
	if time.Since(start) > time.Second {
		t.Fatal("Wait did not see the loop end")
	}
	kit.dev.Close()
	n := kit.ann.count()
	time.Sleep(60 * time.Millisecond)
	if kit.ann.count() != n {
		t.Error("the device announced again after shutdown")
	}
}
