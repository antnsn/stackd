package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

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

	for repo, statuses := range syncTotal {
		for status, count := range statuses {
			fmt.Fprintf(w, "stackd_sync_total{repo=%q,status=%q} %d\n", repo, status, count)
		}
	}

	for repo, sum := range syncDurationSum {
		fmt.Fprintf(w, "stackd_sync_duration_seconds_sum{repo=%q} %.4f\n", repo, sum)
		fmt.Fprintf(w, "stackd_sync_duration_seconds_count{repo=%q} %d\n", repo, syncDurationCount[repo])
	}

	for stack, statuses := range applyTotal {
		for status, count := range statuses {
			fmt.Fprintf(w, "stackd_stack_apply_total{stack=%q,status=%q} %d\n", stack, status, count)
		}
	}

	for key, count := range containersRunning {
		fmt.Fprintf(w, "stackd_containers_running{stack=%q} %d\n", key, count)
	}

	for repo, ts := range lastSyncTS {
		fmt.Fprintf(w, "stackd_last_sync_timestamp{repo=%q} %d\n", repo, ts)
	}
}
