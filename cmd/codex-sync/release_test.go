package main

import (
	"strings"
	"testing"
)

func TestReleaseURLsPointAtThisRepository(t *testing.T) {
	for _, u := range []string{latestReleaseURL(), releasesURL(5)} {
		if !strings.Contains(u, "https://api.github.com/repos/d-jiao/codex-sync/releases") {
			t.Errorf("unexpected release URL %q", u)
		}
		if strings.Contains(u, "tawanorg") {
			t.Errorf("release URL still points at upstream: %q", u)
		}
	}
	if !strings.HasSuffix(releasesURL(5), "per_page=5") {
		t.Errorf("releasesURL(5) = %q", releasesURL(5))
	}
}
