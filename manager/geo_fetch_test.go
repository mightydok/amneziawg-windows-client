/* SPDX-License-Identifier: MIT */

package manager

import (
	"strings"
	"testing"

	"github.com/mightydok/awg-geolist"
)

// TestGeoFetchDefaultSource downloads the default IPv4 list through WinHTTP and runs
// it through the same validation as a real refresh. It needs network access and is
// skipped in short mode.
func TestGeoFetchDefaultSource(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	settings := geolist.DefaultSettings()
	raw, err := geoFetch(settings.Source(geolist.IPv4, geolist.DefaultCountry))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	list, err := geolist.Validate(raw, geolist.IPv4, 0)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	t.Logf("fetched %d bytes, %d IPv4 prefixes", len(raw), len(list))
}

func TestGeoFetchReportsHTTPStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	_, err := geoFetch("https://raw.githubusercontent.com/ipverse/country-ip-blocks/master/country/zz/does-not-exist.txt")
	if err == nil {
		t.Fatal("expected an error for a missing resource")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected HTTP 404 in error, got: %v", err)
	}
}

func TestGeoFetchLocalFileAndLimits(t *testing.T) {
	dir := t.TempDir()
	path := dir + `\list.txt`
	if err := writeTestFile(path, "10.0.0.0/8\n# comment\n"); err != nil {
		t.Fatal(err)
	}
	raw, err := geoFetch(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "10.0.0.0/8\n# comment\n" {
		t.Fatalf("unexpected content %q", raw)
	}
	if _, err := geoFetch(dir + `\missing.txt`); err == nil {
		t.Fatal("expected error for a missing file")
	}
}
