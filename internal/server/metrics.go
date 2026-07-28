package server

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var requestDurationBuckets = [...]float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

type requestKey struct {
	method string
	status int
}

type requestMetric struct {
	count       atomic.Uint64
	durationNS  atomic.Uint64
	bucketCount [len(requestDurationBuckets) + 1]atomic.Uint64
}

type serverMetrics struct {
	inFlight         atomic.Int64
	rescansTotal     atomic.Uint64
	rescanErrors     atomic.Uint64
	rescanDurationNS atomic.Uint64
	lastRescanUnix   atomic.Int64
	requests         sync.Map // requestKey -> *requestMetric
}

func (m *serverMetrics) observeRequest(method string, status int, elapsed time.Duration) {
	key := requestKey{method: method, status: status}
	value, _ := m.requests.LoadOrStore(key, &requestMetric{})
	metric := value.(*requestMetric)
	metric.count.Add(1)
	metric.durationNS.Add(uint64(elapsed))
	seconds := elapsed.Seconds()
	for i, upper := range requestDurationBuckets {
		if seconds <= upper {
			metric.bucketCount[i].Add(1)
		}
	}
	metric.bucketCount[len(requestDurationBuckets)].Add(1)
}

func (m *serverMetrics) observeRescan(elapsed time.Duration, err error) {
	m.rescansTotal.Add(1)
	m.rescanDurationNS.Store(uint64(elapsed))
	if err != nil {
		m.rescanErrors.Add(1)
		return
	}
	m.lastRescanUnix.Store(time.Now().Unix())
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricsResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

// Flush preserves streaming behavior for endpoints such as MCP.
func (w *metricsResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack preserves websocket/stream upgrades supported by wrapped handlers.
func (w *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return h.Hijack()
}

func (w *metricsResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	v := s.vault.Load()
	notes, assets := 0, 0
	if v != nil {
		notes = len(v.Notes)
		assets = len(v.Assets)
	}

	fmt.Fprintln(w, "# HELP amythest_info Amythest build information.")
	fmt.Fprintln(w, "# TYPE amythest_info gauge")
	fmt.Fprintln(w, `amythest_info{version="dev"} 1`)
	fmt.Fprintln(w, "# HELP amythest_vault_notes Number of notes in the current vault snapshot.")
	fmt.Fprintln(w, "# TYPE amythest_vault_notes gauge")
	fmt.Fprintf(w, "amythest_vault_notes %d\n", notes)
	fmt.Fprintln(w, "# HELP amythest_vault_assets Number of assets in the current vault snapshot.")
	fmt.Fprintln(w, "# TYPE amythest_vault_assets gauge")
	fmt.Fprintf(w, "amythest_vault_assets %d\n", assets)
	fmt.Fprintln(w, "# HELP amythest_http_requests_in_flight Requests currently being served.")
	fmt.Fprintln(w, "# TYPE amythest_http_requests_in_flight gauge")
	fmt.Fprintf(w, "amythest_http_requests_in_flight %d\n", s.metrics.inFlight.Load())
	fmt.Fprintln(w, "# HELP amythest_go_goroutines Current number of goroutines.")
	fmt.Fprintln(w, "# TYPE amythest_go_goroutines gauge")
	fmt.Fprintf(w, "amythest_go_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintln(w, "# HELP amythest_go_heap_alloc_bytes Currently allocated heap bytes.")
	fmt.Fprintln(w, "# TYPE amythest_go_heap_alloc_bytes gauge")
	fmt.Fprintf(w, "amythest_go_heap_alloc_bytes %d\n", mem.HeapAlloc)
	fmt.Fprintln(w, "# HELP amythest_rescans_total Total vault rescans.")
	fmt.Fprintln(w, "# TYPE amythest_rescans_total counter")
	fmt.Fprintf(w, "amythest_rescans_total %d\n", s.metrics.rescansTotal.Load())
	fmt.Fprintln(w, "# HELP amythest_rescan_errors_total Total failed vault rescans.")
	fmt.Fprintln(w, "# TYPE amythest_rescan_errors_total counter")
	fmt.Fprintf(w, "amythest_rescan_errors_total %d\n", s.metrics.rescanErrors.Load())
	fmt.Fprintln(w, "# HELP amythest_rescan_duration_seconds Duration of the most recent vault rescan.")
	fmt.Fprintln(w, "# TYPE amythest_rescan_duration_seconds gauge")
	fmt.Fprintf(w, "amythest_rescan_duration_seconds %.9f\n",
		float64(s.metrics.rescanDurationNS.Load())/float64(time.Second))
	fmt.Fprintln(w, "# HELP amythest_last_successful_rescan_timestamp_seconds Unix timestamp of the last successful vault rescan.")
	fmt.Fprintln(w, "# TYPE amythest_last_successful_rescan_timestamp_seconds gauge")
	fmt.Fprintf(w, "amythest_last_successful_rescan_timestamp_seconds %d\n", s.metrics.lastRescanUnix.Load())

	type snapshot struct {
		key    requestKey
		count  uint64
		sumNS  uint64
		bucket [len(requestDurationBuckets) + 1]uint64
	}
	var rows []snapshot
	s.metrics.requests.Range(func(key, value any) bool {
		k := key.(requestKey)
		m := value.(*requestMetric)
		row := snapshot{key: k, count: m.count.Load(), sumNS: m.durationNS.Load()}
		for i := range row.bucket {
			row.bucket[i] = m.bucketCount[i].Load()
		}
		rows = append(rows, row)
		return true
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].key.method == rows[j].key.method {
			return rows[i].key.status < rows[j].key.status
		}
		return rows[i].key.method < rows[j].key.method
	})

	fmt.Fprintln(w, "# HELP amythest_http_requests_total Total HTTP requests.")
	fmt.Fprintln(w, "# TYPE amythest_http_requests_total counter")
	for _, row := range rows {
		labels := requestLabels(row.key)
		fmt.Fprintf(w, "amythest_http_requests_total%s %d\n", labels, row.count)
	}
	fmt.Fprintln(w, "# HELP amythest_http_request_duration_seconds HTTP request duration.")
	fmt.Fprintln(w, "# TYPE amythest_http_request_duration_seconds histogram")
	for _, row := range rows {
		baseLabels := requestLabelsContent(row.key)
		for i, upper := range requestDurationBuckets {
			fmt.Fprintf(w, "amythest_http_request_duration_seconds_bucket{%s,le=%q} %d\n",
				baseLabels, strconv.FormatFloat(upper, 'g', -1, 64), row.bucket[i])
		}
		fmt.Fprintf(w, "amythest_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n",
			baseLabels, row.bucket[len(requestDurationBuckets)])
		fmt.Fprintf(w, "amythest_http_request_duration_seconds_sum%s %.9f\n",
			requestLabels(row.key), float64(row.sumNS)/float64(time.Second))
		fmt.Fprintf(w, "amythest_http_request_duration_seconds_count%s %d\n",
			requestLabels(row.key), row.count)
	}
}

func requestLabels(key requestKey) string {
	return "{" + requestLabelsContent(key) + "}"
}

func requestLabelsContent(key requestKey) string {
	return `method="` + escapeMetricLabel(key.method) + `",code="` + strconv.Itoa(key.status) + `"`
}

func escapeMetricLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s)
}
