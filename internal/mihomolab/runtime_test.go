package mihomolab

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/model"
)

func TestRuntimeBaseConfigIsLoopbackOnlyAndTransformCompatible(t *testing.T) {
	value := runtimeBaseConfig(32001, "127.0.0.1:32002", "127.0.0.1:32003", "127.0.0.1:32004")
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, required := range []string{`"mixed-port":32001`, `"enable":false`, `"MATCH,ROOT"`, `"PROXY-BRANCH"`, `"DIRECT-BRANCH"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("runtime base config missing %q: %s", required, text)
		}
	}
	if strings.Contains(text, "0.0.0.0") {
		t.Fatalf("runtime base config escaped loopback: %s", text)
	}
}

func TestValidateRuntimeCandidateRequiresExactTransformObjects(t *testing.T) {
	ports := portSet{guard: 32001, direct: 32002, proxy: 32003, original: 32004}
	candidate := map[string]any{
		"mode": "rule", "tun": map[string]any{"enable": false},
		"rules":   []string{"MATCH,SMARTROUTE-GUARD-ADAPTER"},
		"proxies": []map[string]any{{"name": "SMARTROUTE-GUARD-ADAPTER", "type": "socks5", "server": "127.0.0.1", "port": ports.guard, "udp": false}},
		"listeners": []map[string]any{
			{"name": "smartroute-direct", "type": "mixed", "listen": "127.0.0.1", "port": ports.direct, "proxy": "DIRECT", "udp": false},
			{"name": "smartroute-proxy", "type": "mixed", "listen": "127.0.0.1", "port": ports.proxy, "proxy": "PROXY-BRANCH", "udp": false},
			{"name": "smartroute-original", "type": "mixed", "listen": "127.0.0.1", "port": ports.original, "proxy": "ROOT", "udp": false},
		},
	}
	encoded, _ := json.Marshal(candidate)
	if err := validateRuntimeCandidate(encoded, ports); err != nil {
		t.Fatal(err)
	}
	candidate["rules"] = []string{"MATCH,ROOT"}
	encoded, _ = json.Marshal(candidate)
	if err := validateRuntimeCandidate(encoded, ports); err == nil {
		t.Fatal("candidate without transformed MATCH was accepted")
	}
}

func TestParseRuntimeEventsIgnoresOtherLines(t *testing.T) {
	value := strings.Join([]string{
		`{"event_type":"supervisor","state":"started"}`,
		`not-json`,
		`{"event_type":"decision","selected_path":"proxy","reason_code":"durable_policy_selected","observation":{"path":"proxy","success":true,"stage_reached":"tls","latency_ms":1},"committed":true}`,
	}, "\n")
	events := parseRuntimeEvents(value, "decision")
	if len(events) != 1 || events[0].SelectedPath != model.PathProxy || !events[0].Committed || events[0].Observation.StageReached != model.StageTLS {
		t.Fatalf("events=%+v", events)
	}
}

func TestRuntimeSmartRouteConfigUsesAutoAndPrivateWorkspace(t *testing.T) {
	workspace := t.TempDir()
	ports := portSet{front: 32100, direct: 32101, proxy: 32102, sidecar: 32103, guard: 32104, original: 32105}
	path, databasePath, err := writeRuntimeSmartRouteConfig(workspace, ports)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Learning.Mode != "auto" || cfg.Learning.Persistence.DatabasePath != databasePath || cfg.Observation.Enabled || cfg.ListenAddress != "127.0.0.1:32103" {
		t.Fatalf("runtime config=%+v", cfg)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime config mode=%v", info.Mode())
	}
}
