package mihomolab

import (
	"os"
	"strings"
	"testing"
)

func TestRenderConfigIsLoopbackOnlyAndDisablesTUN(t *testing.T) {
	config := string(renderConfig(portSet{front: 31001, direct: 31002, proxy: 31003, sidecar: 31004}, "127.0.0.1:31005", "127.0.0.1:31006"))
	required := []string{
		"mixed-port: 31001",
		"bind-address: 127.0.0.1",
		"enable: false",
		"127.0.0.1:31006",
		"port: 31004",
		"port: 31002",
		"proxy: DIRECT",
		"port: 31003",
		"proxy: LAB-PROXY",
		"MATCH,SMARTROUTE-ADAPTER",
	}
	for _, value := range required {
		if !strings.Contains(config, value) {
			t.Fatalf("renderConfig() missing %q:\n%s", value, config)
		}
	}
	if strings.Contains(config, "0.0.0.0") || strings.Contains(config, "tun:\n  enable: true") {
		t.Fatalf("renderConfig() violates isolation:\n%s", config)
	}
}

func TestSyntheticDNSResponseMapsAOnly(t *testing.T) {
	query := []byte{
		0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0,
		4, 'e', 'c', 'h', 'o', 4, 't', 'e', 's', 't', 0,
		0, 1, 0, 1,
	}
	response, ok := syntheticDNSResponse(query)
	if !ok || len(response) < 4 || !strings.HasSuffix(string(response), string([]byte{127, 0, 0, 2})) {
		t.Fatalf("syntheticDNSResponse() ok=%v response=%v", ok, response)
	}
}

func TestIsolatedEnvironmentRemovesMihomoOverrides(t *testing.T) {
	t.Setenv("CLASH_HOME_DIR", "/should-not-survive")
	t.Setenv("MIHOMO_TEST_OVERRIDE", "unsafe")
	for _, item := range isolatedEnvironment() {
		if strings.HasPrefix(item, "CLASH_") || strings.HasPrefix(item, "MIHOMO_") {
			t.Fatalf("isolatedEnvironment() retained %q", item)
		}
	}
}

func TestValidateBinaryRequiresExplicitExecutable(t *testing.T) {
	if _, err := validateBinary(""); err == nil {
		t.Fatal("validateBinary(empty) error = nil")
	}
	path := t.TempDir() + "/not-executable"
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateBinary(path); err == nil {
		t.Fatal("validateBinary(non-executable) error = nil")
	}
}
