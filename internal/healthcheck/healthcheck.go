// Package healthcheck lets a binary check its own HTTP health endpoint, for container HEALTHCHECKs in images that have no shell or curl.
package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Requested reports whether the process was started as `<binary> healthcheck`.
func Requested() bool { return len(os.Args) == 2 && os.Args[1] == "healthcheck" }

// Run GETs http://127.0.0.1:port/path and exits 0 on a 2xx response, 1 otherwise. It never returns.
func Run(port int, path string) {
	if err := Check(context.Background(), fmt.Sprintf("http://127.0.0.1:%d%s", port, path)); err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// Check performs the request with a short timeout.
func Check(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s returned %d", url, res.StatusCode)
	}
	return nil
}
