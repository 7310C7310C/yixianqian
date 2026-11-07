package httpserver

import (
	"fmt"
	"net/http"
	"runtime"
)

// handleMetrics is intentionally dependency-free: a small Prometheus text
// exposition of the counters the process already keeps. Nginx only exposes
// /metrics to localhost, so it is not a public fingerprint.
func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Fprintf(w, "# HELP yxq_http_requests_total HTTP requests handled.\n")
	fmt.Fprintf(w, "# TYPE yxq_http_requests_total counter\n")
	fmt.Fprintf(w, "yxq_http_requests_total %d\n", a.metricRequests.Load())
	fmt.Fprintf(w, "# HELP yxq_http_4xx_total HTTP 4xx responses.\n")
	fmt.Fprintf(w, "# TYPE yxq_http_4xx_total counter\n")
	fmt.Fprintf(w, "yxq_http_4xx_total %d\n", a.metric4xx.Load())
	fmt.Fprintf(w, "# HELP yxq_http_5xx_total HTTP 5xx responses.\n")
	fmt.Fprintf(w, "# TYPE yxq_http_5xx_total counter\n")
	fmt.Fprintf(w, "yxq_http_5xx_total %d\n", a.metric5xx.Load())
	fmt.Fprintf(w, "# HELP yxq_sse_connections_active Active notification SSE connections.\n")
	fmt.Fprintf(w, "# TYPE yxq_sse_connections_active gauge\n")
	fmt.Fprintf(w, "yxq_sse_connections_active %d\n", a.metricSSE.Load())
	fmt.Fprintf(w, "# HELP yxq_thumb_cache_hits_total Thumbnail disk-cache hits.\n")
	fmt.Fprintf(w, "# TYPE yxq_thumb_cache_hits_total counter\n")
	fmt.Fprintf(w, "yxq_thumb_cache_hits_total %d\n", a.thumbCacheHits.Load())
	fmt.Fprintf(w, "# HELP yxq_thumb_cache_misses_total Thumbnail disk-cache misses.\n")
	fmt.Fprintf(w, "# TYPE yxq_thumb_cache_misses_total counter\n")
	fmt.Fprintf(w, "yxq_thumb_cache_misses_total %d\n", a.thumbCacheMisses.Load())
	fmt.Fprintf(w, "# HELP yxq_goroutines Current goroutines.\n")
	fmt.Fprintf(w, "# TYPE yxq_goroutines gauge\n")
	fmt.Fprintf(w, "yxq_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "# HELP yxq_memory_alloc_bytes Current Go heap allocation.\n")
	fmt.Fprintf(w, "# TYPE yxq_memory_alloc_bytes gauge\n")
	fmt.Fprintf(w, "yxq_memory_alloc_bytes %d\n", mem.Alloc)
}
