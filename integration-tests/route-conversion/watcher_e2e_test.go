//go:build e2e

package routeconversion

import (
	"context"
	"sync"
	"time"
)

// watcher polls the gateway CR every 500ms and records when it first sees
// kcp's conversion fence on the route (fence onset) and when it first sees the
// route static on the destination (switch done). The checker's windows are
// built from these times; the client tools log in the same pod's clock.
type watcher struct {
	mu         sync.Mutex
	fenceOnset time.Time
	switchDone time.Time
	cancel     context.CancelFunc
	done       chan struct{}
}

func (e *env) startWatcher(ctx context.Context) *watcher {
	ctx, cancel := context.WithCancel(ctx)
	w := &watcher{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
			if err == nil {
				r := FindRoute(raw, e.route)
				now := time.Now()
				w.mu.Lock()
				if w.fenceOnset.IsZero() && HasConvertFence(r) {
					w.fenceOnset = now
				}
				if w.switchDone.IsZero() && StaticOn(r, e.domains.Dest, e.domains.DestID) {
					w.switchDone = now
				}
				w.mu.Unlock()
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return w
}

// windows is what the watcher has seen so far, in Unix milliseconds (0 = not
// yet).
func (w *watcher) windows() Windows {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out Windows
	if !w.fenceOnset.IsZero() {
		out.FenceOnsetMs = w.fenceOnset.UnixMilli()
	}
	if !w.switchDone.IsZero() {
		out.SwitchDoneMs = w.switchDone.UnixMilli()
	}
	return out
}

// stop ends the polling and returns the final windows.
func (w *watcher) stop() Windows {
	w.cancel()
	<-w.done
	return w.windows()
}
