package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// CacheExporter serves the registry cache's disk usage, as recorded by CachePrune, in
// the Prometheus text format (the control plane reads it; jobs cannot reach it).
func CacheExporter(statusPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b, err := os.ReadFile(statusPath)
		if err != nil {
			http.Error(w, "no cache status yet", http.StatusServiceUnavailable)
			return
		}
		var out bytes.Buffer
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			name, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
			if !ok || (name != "used_bytes" && name != "budget_bytes") {
				continue
			}
			metric := "ghrm_cache_disk_" + name
			fmt.Fprintf(&out, "# TYPE %s gauge\n%s %s\n", metric, metric, strings.TrimSpace(value))
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write(out.Bytes())
	})
}
