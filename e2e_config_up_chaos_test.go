package devport_test

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"testing"
	"time"
)

type upConfigSpec struct {
	PortRangeStart int
	PortRangeEnd   int
	WebPort        int
	WebMessage     string
	WorkerMessage  string
	IncludeCron    bool
	CronMessage    string
}

func TestEndToEndUpAfterConfigMutation(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required")
	}

	t.Run("message_changes_and_revert_stay_coherent", func(t *testing.T) {
		h := newHarness(t)
		t.Cleanup(func() {
			_, _, _ = h.runDetailed("down", "--file", h.configPath)
		})

		start, portA, _ := reserveTCPPortRange(t, 20)
		base := upConfigSpec{
			PortRangeStart: start,
			PortRangeEnd:   start + 19,
			WebPort:        portA,
			WebMessage:     "web-v1",
			WorkerMessage:  "worker-v1",
		}
		mutated := base
		mutated.WebMessage = "web-v2"
		mutated.WorkerMessage = "worker-v2"

		h.writeUpConfig(base)
		h.runOK("up", "--file", h.configPath)
		web1 := h.findStatus("app/web")
		worker1 := h.findStatus("jobs/worker")
		h.assertStatusMatches(web1, "running", "healthy", nil)
		h.assertStatusMatches(worker1, "running", "healthy", nil)
		if body := fetchHTTPBody(t, portA); body != "web-v1" {
			t.Fatalf("expected original web response, got %q", body)
		}

		h.writeUpConfig(mutated)
		h.runOK("up", "--file", h.configPath)
		web2 := h.findStatus("app/web")
		worker2 := h.findStatus("jobs/worker")
		h.assertStatusMatches(web2, "running", "healthy", []string{"spec changed since last start"})
		h.assertStatusMatches(worker2, "running", "healthy", []string{"spec changed since last start"})
		if web2.PID != web1.PID {
			t.Fatalf("expected web pid to remain stable after no-op up: before=%d after=%d", web1.PID, web2.PID)
		}
		if worker2.PID != worker1.PID {
			t.Fatalf("expected worker pid to remain stable after no-op up: before=%d after=%d", worker1.PID, worker2.PID)
		}
		if body := fetchHTTPBody(t, portA); body != "web-v1" {
			t.Fatalf("expected runtime to stay on original message after no-op up, got %q", body)
		}

		h.writeUpConfig(base)
		h.runOK("up", "--file", h.configPath)
		web3 := h.findStatus("app/web")
		worker3 := h.findStatus("jobs/worker")
		h.assertStatusMatches(web3, "running", "healthy", nil)
		h.assertStatusMatches(worker3, "running", "healthy", nil)
		if web3.PID != web1.PID {
			t.Fatalf("expected web pid to stay stable after revert: before=%d after=%d", web1.PID, web3.PID)
		}
		if worker3.PID != worker1.PID {
			t.Fatalf("expected worker pid to stay stable after revert: before=%d after=%d", worker1.PID, worker3.PID)
		}
		if body := fetchHTTPBody(t, portA); body != "web-v1" {
			t.Fatalf("expected original web response after revert, got %q", body)
		}
	})

	t.Run("port_changes_and_revert_stay_coherent", func(t *testing.T) {
		h := newHarness(t)
		t.Cleanup(func() {
			_, _, _ = h.runDetailed("down", "--file", h.configPath)
		})

		start, portA, portB := reserveTCPPortRange(t, 20)
		base := upConfigSpec{
			PortRangeStart: start,
			PortRangeEnd:   start + 19,
			WebPort:        portA,
			WebMessage:     "web-port-a",
			WorkerMessage:  "worker-steady",
		}
		mutated := base
		mutated.WebPort = portB
		mutated.WebMessage = "web-port-b"

		h.writeUpConfig(base)
		h.runOK("up", "--file", h.configPath)
		web1 := h.findStatus("app/web")
		worker1 := h.findStatus("jobs/worker")
		h.assertStatusMatches(web1, "running", "healthy", nil)
		h.assertStatusMatches(worker1, "running", "healthy", nil)

		h.writeUpConfig(mutated)
		h.runOK("up", "--file", h.configPath)
		web2 := h.findStatus("app/web")
		worker2 := h.findStatus("jobs/worker")
		h.assertStatusMatches(web2, "running", "unhealthy", []string{
			"health check failing",
			"spec changed since last start",
			"wrong port listening",
		})
		h.assertStatusMatches(worker2, "running", "healthy", nil)
		if web2.PID != web1.PID {
			t.Fatalf("expected web pid to remain stable after no-op up: before=%d after=%d", web1.PID, web2.PID)
		}
		if worker2.PID != worker1.PID {
			t.Fatalf("expected worker pid to remain stable after unrelated config change: before=%d after=%d", worker1.PID, worker2.PID)
		}
		if web2.Port != portA {
			t.Fatalf("expected status to keep reporting live port %d, got %d", portA, web2.Port)
		}
		if body := fetchHTTPBody(t, portA); body != "web-port-a" {
			t.Fatalf("expected old runtime to remain on original port, got %q", body)
		}
		if portListeningTest(portB) {
			t.Fatalf("expected new config port %d to remain unused after no-op up", portB)
		}

		h.writeUpConfig(base)
		h.runOK("up", "--file", h.configPath)
		web3 := h.findStatus("app/web")
		worker3 := h.findStatus("jobs/worker")
		h.assertStatusMatches(web3, "running", "healthy", nil)
		h.assertStatusMatches(worker3, "running", "healthy", nil)
		if web3.PID != web1.PID {
			t.Fatalf("expected web pid to stay stable after revert: before=%d after=%d", web1.PID, web3.PID)
		}
		if worker3.PID != worker1.PID {
			t.Fatalf("expected worker pid to stay stable after revert: before=%d after=%d", worker1.PID, worker3.PID)
		}
		if body := fetchHTTPBody(t, portA); body != "web-port-a" {
			t.Fatalf("expected original runtime to remain after revert, got %q", body)
		}
	})

	t.Run("adding_service_via_up_preserves_existing_runtime", func(t *testing.T) {
		h := newHarness(t)
		t.Cleanup(func() {
			_, _, _ = h.runDetailed("down", "--file", h.configPath)
		})

		start, portA, _ := reserveTCPPortRange(t, 20)
		base := upConfigSpec{
			PortRangeStart: start,
			PortRangeEnd:   start + 19,
			WebPort:        portA,
			WebMessage:     "web-base",
			WorkerMessage:  "worker-base",
		}
		withCron := base
		withCron.IncludeCron = true
		withCron.CronMessage = "cron-v1"
		withCronChanged := withCron
		withCronChanged.CronMessage = "cron-v2"

		h.writeUpConfig(base)
		h.runOK("up", "--file", h.configPath)
		web1 := h.findStatus("app/web")
		worker1 := h.findStatus("jobs/worker")
		h.assertStatusMatches(web1, "running", "healthy", nil)
		h.assertStatusMatches(worker1, "running", "healthy", nil)

		h.writeUpConfig(withCron)
		h.runOK("up", "--file", h.configPath)
		web2 := h.findStatus("app/web")
		worker2 := h.findStatus("jobs/worker")
		cron1 := h.findStatus("jobs/cron")
		h.assertStatusMatches(web2, "running", "healthy", nil)
		h.assertStatusMatches(worker2, "running", "healthy", nil)
		h.assertStatusMatches(cron1, "running", "healthy", nil)
		if web2.PID != web1.PID {
			t.Fatalf("expected web pid to remain stable after adding cron: before=%d after=%d", web1.PID, web2.PID)
		}
		if worker2.PID != worker1.PID {
			t.Fatalf("expected worker pid to remain stable after adding cron: before=%d after=%d", worker1.PID, worker2.PID)
		}
		if cron1.PID == 0 {
			t.Fatalf("expected cron pid to be recorded after add")
		}

		h.writeUpConfig(withCronChanged)
		h.runOK("up", "--file", h.configPath)
		web3 := h.findStatus("app/web")
		worker3 := h.findStatus("jobs/worker")
		cron2 := h.findStatus("jobs/cron")
		h.assertStatusMatches(web3, "running", "healthy", nil)
		h.assertStatusMatches(worker3, "running", "healthy", nil)
		h.assertStatusMatches(cron2, "running", "healthy", []string{"spec changed since last start"})
		if web3.PID != web1.PID {
			t.Fatalf("expected web pid to remain stable after cron-only mutation: before=%d after=%d", web1.PID, web3.PID)
		}
		if worker3.PID != worker1.PID {
			t.Fatalf("expected worker pid to remain stable after cron-only mutation: before=%d after=%d", worker1.PID, worker3.PID)
		}
		if cron2.PID != cron1.PID {
			t.Fatalf("expected cron pid to remain stable after no-op up: before=%d after=%d", cron1.PID, cron2.PID)
		}
	})
}

func (h *e2eHarness) writeUpConfig(spec upConfigSpec) {
	cronSection := ""
	if spec.IncludeCron {
		cronSection = fmt.Sprintf(`
[service."jobs/cron"]
cwd = %q
command = [%q, "sleep", "--duration", "60s", "--message", %q]
no_port = true
restart = "never"

[service."jobs/cron".health]
type = "process"
startup_timeout = "5s"
`, h.root, h.serviceBin, spec.CronMessage)
	}

	h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = %d, end = %d }
tmux_session = %q

[service."app/web"]
cwd = %q
command = [%q, "http", "--port", "${PORT}", "--message", %q]
port = %d
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200]
startup_timeout = "10s"

[service."jobs/worker"]
cwd = %q
command = [%q, "sleep", "--duration", "60s", "--message", %q]
no_port = true
restart = "never"

[service."jobs/worker".health]
type = "process"
startup_timeout = "5s"%s
`, spec.PortRangeStart, spec.PortRangeEnd, h.session, h.root, h.serviceBin, spec.WebMessage, spec.WebPort, h.root, h.serviceBin, spec.WorkerMessage, cronSection))
}

func (h *e2eHarness) assertStatusMatches(status statusView, wantStatus, wantHealth string, wantDrift []string) {
	h.t.Helper()

	if status.Status != wantStatus {
		h.t.Fatalf("expected %s status=%s, got %+v", status.Key, wantStatus, status)
	}
	if status.Health != wantHealth {
		h.t.Fatalf("expected %s health=%s, got %+v", status.Key, wantHealth, status)
	}
	if !slices.Equal(status.Drift, wantDrift) {
		h.t.Fatalf("expected %s drift=%v, got %+v", status.Key, wantDrift, status)
	}
}

func fetchHTTPBody(t *testing.T, port int) string {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("get web response: %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read web response: %v", err)
	}
	return string(body)
}
