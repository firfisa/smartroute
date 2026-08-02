package privacy

import (
	"strings"
	"testing"

	"github.com/firfisa/smartroute/internal/model"
)

func TestExplicitOptInExactAndSuffixRules(t *testing.T) {
	policy, err := New(ModeExplicitOptIn, []string{"Login.Example.", ".private.example", "*.sensitive.example", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		host   string
		allow  bool
		reason string
	}{
		{"login.example", false, ReasonNeverDirectExact},
		{"LOGIN.EXAMPLE.", false, ReasonNeverDirectExact},
		{"other.login.example", true, ReasonDirectAllowed},
		{"private.example", false, ReasonNeverDirectSuffix},
		{"a.private.example", false, ReasonNeverDirectSuffix},
		{"notprivate.example", true, ReasonDirectAllowed},
		{"sensitive.example", false, ReasonNeverDirectSuffix},
		{"deep.sensitive.example", false, ReasonNeverDirectSuffix},
		{"127.0.0.1", false, ReasonNeverDirectExact},
		{"127.0.0.2", true, ReasonDirectAllowed},
	}
	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			decision := policy.Evaluate(target(test.host))
			if decision.AllowDirect != test.allow || decision.ReasonCode != test.reason {
				t.Fatalf("Evaluate(%q) = %+v", test.host, decision)
			}
		})
	}
}

func TestPrivacyFirstAndMissingPolicyFailClosed(t *testing.T) {
	policy, err := New(ModePrivacyFirst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision := policy.Evaluate(target("public.example")); decision.AllowDirect || decision.ReasonCode != ReasonPrivacyFirst {
		t.Fatalf("privacy-first decision = %+v", decision)
	}
	if decision := (Policy{}).Evaluate(target("public.example")); decision.AllowDirect || decision.ReasonCode != ReasonMissingRuntimePolicy {
		t.Fatalf("missing-policy decision = %+v", decision)
	}
}

func TestInvalidTargetFailsClosed(t *testing.T) {
	policy, err := New(ModeExplicitOptIn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision := policy.Evaluate(target("bad_name.example")); decision.AllowDirect || decision.ReasonCode != ReasonInvalidTarget {
		t.Fatalf("invalid-target decision = %+v", decision)
	}
}

func TestNewRejectsAmbiguousOrInvalidPatterns(t *testing.T) {
	for _, patterns := range [][]string{{""}, {" example.com"}, {"https://example.com"}, {".127.0.0.1"}, {"bad_name.example"}} {
		_, err := New(ModeExplicitOptIn, patterns)
		if err == nil || !strings.Contains(err.Error(), "never_direct_probe") {
			t.Fatalf("New(%q) error = %v", patterns, err)
		}
	}
}

func target(host string) model.Target {
	return model.Target{Hostname: host, Port: 443, Transport: model.TransportTCP}
}
