package main

import "testing"

func TestVersionStatus(t *testing.T) {
	cases := []struct {
		name            string
		current, latest string
		ok              bool
		want            string
	}{
		{"check unavailable prints nothing", "v1.0.0", "", false, ""},
		{"up to date", "v1.2.3", "v1.2.3", true, "your version v1.2.3 is the latest version"},
		{"update available", "v1.2.3", "v1.3.0", true, "the latest version is v1.3.0, yours is v1.2.3 — please update"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionStatus(c.current, c.latest, c.ok); got != c.want {
				t.Errorf("versionStatus(%q,%q,%v) = %q, want %q", c.current, c.latest, c.ok, got, c.want)
			}
		})
	}
}
