package hostinfo

import (
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHandlerCachesCollection: concurrent and repeated requests inside the
// cache window run the host commands once.
func TestHandlerCachesCollection(t *testing.T) {
	var calls atomic.Int32
	oldCollect := collect
	collect = func() HostInfo {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond) // a slow command
		return HostInfo{PhysicalDisks: []PhysicalDisk{{Name: "d", SizeGB: 7}}}
	}
	cacheMu.Lock()
	cachedAt = time.Time{}
	cacheMu.Unlock()
	t.Cleanup(func() {
		collect = oldCollect
		cacheMu.Lock()
		cachedAt = time.Time{}
		cacheMu.Unlock()
	})

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/host", nil))
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("collect ran %d times for 10 requests, want 1", n)
	}
	if got := summary(); got.DiskTotalGB != 7 {
		t.Errorf("cached summary = %+v", got)
	}

	// Once the entry is older than cacheTTL it is collected again.
	cacheMu.Lock()
	cachedAt = time.Now().Add(-cacheTTL - time.Second)
	cacheMu.Unlock()
	summary()
	if n := calls.Load(); n != 2 {
		t.Errorf("collect ran %d times after expiry, want 2", n)
	}
}
