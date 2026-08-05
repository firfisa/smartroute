package connectionid

import "testing"

func TestNewIsDistinctAndValid(t *testing.T) {
	first, err := New()
	if err != nil {
		t.Fatal(err)
	}
	second, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || Validate(first) != nil || Validate(second) != nil {
		t.Fatalf("first=%q second=%q", first, second)
	}
}

func TestValidateRejectsSemanticOrMalformedValues(t *testing.T) {
	for _, value := range []string{
		"home-network", "conn-0123", "conn-ABCDEF0123456789abcdef0123456789",
		"trial-0123456789abcdef0123456789abcdef", "conn-0123456789abcdef0123456789abcdeg",
	} {
		if err := Validate(value); err == nil {
			t.Fatalf("accepted invalid value %q", value)
		}
	}
}
