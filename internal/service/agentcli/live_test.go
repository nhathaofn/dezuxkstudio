package agentcli

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
)

// TestLiveOfficialChannels downloads each CLI from its real release channel into a temp folder and runs
// the downloaded program. It needs the network and several hundred MB, so it only runs on request:
//
//	AGENTCLI_LIVE=1 go test ./internal/service/agentcli -run Live -v
func TestLiveOfficialChannels(t *testing.T) {
	if os.Getenv("AGENTCLI_LIVE") == "" {
		t.Skip("set AGENTCLI_LIVE=1 to download the real CLIs")
	}
	m := New(Options{Dir: t.TempDir(), UserAgent: "agentcli-live-test"})
	for _, id := range domainagent.AllProviderIDs() {
		t.Run(string(id), func(t *testing.T) {
			if !m.Supported(id) {
				t.Skip("no managed copy on this platform")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()
			start := time.Now()
			cur, changed, err := m.Install(ctx, id)
			if err != nil || !changed {
				t.Fatalf("install: %+v changed=%v err=%v", cur, changed, err)
			}
			out, err := exec.CommandContext(ctx, cur.Path, domainagent.Registry[id].VersionArgs...).CombinedOutput()
			if err != nil {
				t.Fatalf("the downloaded program does not run: %v\n%s", err, out)
			}
			t.Logf("%s %s installed in %s: %s", id, cur.Version, time.Since(start).Round(time.Second), strings.TrimSpace(string(out)))
			if again, changed, err := m.Install(ctx, id); err != nil || changed || again.Version != cur.Version {
				t.Fatalf("a second install must find it current: %+v changed=%v err=%v", again, changed, err)
			}
		})
	}
}
