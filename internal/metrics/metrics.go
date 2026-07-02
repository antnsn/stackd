package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"
)

// labelReplacer escapes characters that are special in Prometheus label values
// (backslash, double-quote, newline) per the exposition format spec, so that a
// repo/stack name containing them cannot corrupt or inject into the output.
var labelReplacer = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escapeLabel(v string) string {
	return labelReplacer.Replace(v)
}

var mu sync.RWMutex

// Counters
var (
	syncTotal  = map[string]map[string]int64{}
	applyTotal = map[string]map[string]int64{}
)

// Gauges
var (
	containersRunning = map[string]int64{}
	lastSyncTS        = map[string]int64{}
)

// Histograms (simplified as sum+count)
var (
	syncDurationSum   = map[string]float64{}
	syncDurationCount = map[string]int64{}
)

func RecordSync(repo, status string, duration time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	if syncTotal[repo] == nil {
		syncTotal[repo] = map[string]int64{}
	}
	syncTotal[repo][status]++
	if status == "success" {
		lastSyncTS[repo] = time.Now().Unix()
		syncDurationSum[repo] += duration.Seconds()
		syncDurationCount[repo]++
	}
}

func RecordApply(stack, status string) {
	mu.Lock()
	defer mu.Unlock()
	if applyTotal[stack] == nil {
		applyTotal[stack] = map[string]int64{}
	}
	applyTotal[stack][status]++
}

func SetContainersRunning(repoStack string, count int64) {
	mu.Lock()
	defer mu.Unlock()
	containersRunning[repoStack] = count
}

// Handler serves Prometheus text format metrics.
func Handler(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	fmt.Fprintf(w, "# HELP stackd_sync_total Total repo sync attempts by status.\n")
	fmt.Fprintf(w, "# TYPE stackd_sync_total counter\n")
	for repo, statuses := range syncTotal {
		for status, count := range statuses {
			fmt.Fprintf(w, "stackd_sync_total{repo=\"%s\",status=\"%s\"} %d\n", escapeLabel(repo), escapeLabel(status), count)
		}
	}

	fmt.Fprintf(w, "# HELP stackd_sync_duration_seconds Repo sync duration summary.\n")
	fmt.Fprintf(w, "# TYPE stackd_sync_duration_seconds summary\n")
	for repo, sum := range syncDurationSum {
		fmt.Fprintf(w, "stackd_sync_duration_seconds_sum{repo=\"%s\"} %.4f\n", escapeLabel(repo), sum)
		fmt.Fprintf(w, "stackd_sync_duration_seconds_count{repo=\"%s\"} %d\n", escapeLabel(repo), syncDurationCount[repo])
	}

	fmt.Fprintf(w, "# HELP stackd_stack_apply_total Total stack apply attempts by status.\n")
	fmt.Fprintf(w, "# TYPE stackd_stack_apply_total counter\n")
	for stack, statuses := range applyTotal {
		for status, count := range statuses {
			fmt.Fprintf(w, "stackd_stack_apply_total{stack=\"%s\",status=\"%s\"} %d\n", escapeLabel(stack), escapeLabel(status), count)
		}
	}

	fmt.Fprintf(w, "# HELP stackd_containers_running Running containers per stack.\n")
	fmt.Fprintf(w, "# TYPE stackd_containers_running gauge\n")
	for key, count := range containersRunning {
		fmt.Fprintf(w, "stackd_containers_running{stack=\"%s\"} %d\n", escapeLabel(key), count)
	}

	fmt.Fprintf(w, "# HELP stackd_last_sync_timestamp Unix timestamp of last successful sync.\n")
	fmt.Fprintf(w, "# TYPE stackd_last_sync_timestamp gauge\n")
	for repo, ts := range lastSyncTS {
		fmt.Fprintf(w, "stackd_last_sync_timestamp{repo=\"%s\"} %d\n", escapeLabel(repo), ts)
	}

	// Runtime gauges — exposed so operators can spot the historical PID/thread
	// leak before it crashes the process. A steadily climbing goroutine or
	// thread count between sync rounds means a subprocess is hanging.
	fmt.Fprintf(w, "# HELP stackd_goroutines Number of goroutines currently in flight.\n")
	fmt.Fprintf(w, "# TYPE stackd_goroutines gauge\n")
	fmt.Fprintf(w, "stackd_goroutines %d\n", runtime.NumGoroutine())

	threads := 0
	if p := pprof.Lookup("threadcreate"); p != nil {
		threads = p.Count()
	}
	fmt.Fprintf(w, "# HELP stackd_threads Number of OS threads created by the Go runtime (cumulative high-water mark).\n")
	fmt.Fprintf(w, "# TYPE stackd_threads gauge\n")
	fmt.Fprintf(w, "stackd_threads %d\n", threads)
}
