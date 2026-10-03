//go:build integration

// Live check of RecentErrors against a real midPoint. Compiled only under
// -tags=integration; it skips unless the environment names a midPoint. It only
// reads, and logs what each section saw.
//
// Environment:
//
//	MIDPOINT_URL, MIDPOINT_USERNAME, MIDPOINT_PASSWORD
//	MIDPOINT_IT_PRINCIPAL_OID  # optional: the person to act as (Switch-To-Principal)
//	MIDPOINT_IT_ERROR_HOURS    # optional: the window, default 24
//
// Example:
//
//	MIDPOINT_URL=http://localhost:8080/midpoint MIDPOINT_USERNAME=… MIDPOINT_PASSWORD=… \
//	go test -tags=integration ./internal/midpoint -run LiveRecentErrors -v
package midpoint

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLiveRecentErrors(t *testing.T) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Skipf("skipping live recent-errors test: %v", err)
	}
	c := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if p := strings.TrimSpace(os.Getenv("MIDPOINT_IT_PRINCIPAL_OID")); p != "" {
		ctx = WithPrincipal(ctx, p)
	}
	hours, _ := strconv.Atoi(os.Getenv("MIDPOINT_IT_ERROR_HOURS"))

	start := time.Now()
	res := c.RecentErrors(ctx, RecentErrorsQuery{Hours: hours})
	t.Logf("took %s", time.Since(start).Round(time.Millisecond))
	b, _ := json.MarshalIndent(res, "", "  ")
	t.Logf("%s", b)
	for name, head := range map[string]SectionHead{
		"failedTasks": res.FailedTasks.SectionHead, "failedAccounts": res.FailedAccounts.SectionHead,
		"brokenAccounts": res.BrokenAccounts.SectionHead, "systems": res.Systems.SectionHead,
		"audit": res.Audit.SectionHead,
	} {
		switch head.Status {
		case SectionOK, SectionRefused, SectionFailed, SectionSkipped:
		default:
			t.Errorf("%s: status %q", name, head.Status)
		}
	}
}
