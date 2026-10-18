package main

import "testing"

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.3.0", "v0.2.0", true},
		{"v0.3.0-beta.1", "v0.2.0", true},
		{"v0.3.0", "v0.3.0-beta.2", true},
		{"v0.3.0-beta.2", "v0.3.0", false},
		{"v0.3.0-beta.2", "v0.3.0-beta.1", true},
		{"v0.3.0-beta.10", "v0.3.0-beta.9", true},
		{"v0.3.0-rc.1", "v0.3.0-beta.5", true},
		{"v0.3.0-beta.1", "v0.3.0-beta.1", false},
		{"v0.2.0", "v0.2.0-2-gabc123-dirty", false},
		{"v0.3.0", "v0.2.0-2-gabc123-dirty", true},
		{"v0.3.0-beta.2", "v0.3.0-beta.1-4-gdeadbee", true},
		{"v0.3.0-beta.1", "v0.3.0-beta.1-4-gdeadbee-dirty", false},
		{"v0.1.0", "dev", true},
		{"dev", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
