/* SPDX-License-Identifier: MIT
 *
 * Geo-split routing: list refresh, settings and status served by the manager.
 */

package manager

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amnezia-vpn/amneziawg-windows-client/services"
	"github.com/amnezia-vpn/amneziawg-windows-client/updater/winhttp"
	"github.com/amnezia-vpn/amneziawg-windows-client/version"
	"github.com/amnezia-vpn/amneziawg-windows/v3/conf"
	"github.com/mightydok/awg-geolist"
)

// GeoStatus is what the UI shows about geo-split routing.
type GeoStatus struct {
	// Enabled is true when at least one stored tunnel has GeoSplit set.
	Enabled    bool
	Settings   geolist.Settings
	Meta       geolist.Meta
	Country    string
	Countries  []string
	SourceKind string
	Stats      geolist.Stats
	Refreshing bool
	Error      string
}

// GeoPreview is the effect of candidate settings on the current list.
type GeoPreview struct {
	Stats geolist.Stats
}

// geoRefreshTimeoutOnStart bounds how long a tunnel start waits for a list refresh.
const geoRefreshTimeoutOnStart = 15 * time.Second

var (
	geoRefreshLock sync.Mutex
	geoRefreshing  uint32
)

// geoCountries returns the distinct country codes enabled in stored tunnels, or the
// default country (with enabled=false) when none is enabled.
func geoCountries() (countries []string, enabled bool) {
	set := make(map[string]bool)
	if names, err := conf.ListConfigNames(); err == nil {
		for _, name := range names {
			c, err := conf.LoadFromName(name)
			if err != nil || c.Interface.GeoSplit == "" {
				continue
			}
			set[c.Interface.GeoSplit] = true
		}
	}
	if len(set) == 0 {
		return []string{geolist.DefaultCountry}, false
	}
	for c := range set {
		countries = append(countries, c)
	}
	sort.Strings(countries)
	return countries, true
}

func geoComputeStats(country string, settings geolist.Settings) (geolist.Stats, geolist.SourceKind, error) {
	policy, err := settings.Policy()
	if err != nil {
		return geolist.Stats{}, "", err
	}
	list4, list6, src, err := geolist.Load(country)
	if err != nil {
		return geolist.Stats{}, "", err
	}
	return geolist.Apply(list4, list6, policy).Stats, src.Kind, nil
}

func (s *ManagerService) GeoStatus() (GeoStatus, error) {
	settings, err := geolist.LoadSettings()
	if err != nil {
		log.Printf("Geo-split: %v", err)
	}
	meta, err := geolist.LoadMeta()
	if err != nil {
		log.Printf("Geo-split: %v", err)
	}
	countries, enabled := geoCountries()
	status := GeoStatus{
		Enabled:    enabled,
		Settings:   settings,
		Meta:       meta,
		Country:    countries[0],
		Countries:  countries,
		Refreshing: atomic.LoadUint32(&geoRefreshing) == 1,
	}
	stats, kind, err := geoComputeStats(status.Country, settings)
	if err != nil {
		status.Error = err.Error()
	} else {
		status.Stats = stats
		status.SourceKind = string(kind)
	}
	return status, nil
}

func (s *ManagerService) GeoSetSettings(settings geolist.Settings) error {
	if s.elevatedToken == 0 {
		return errors.New("Administrator rights are required to change geo-split settings")
	}
	if err := settings.Save(); err != nil {
		return err
	}
	log.Printf("Geo-split: settings saved (min prefix /%d, IPv6 %s, update on start %v, stale after %dh)", settings.MinPrefixV4, settings.IPv6Mode, settings.UpdateOnStart, settings.StaleHours)
	IPCServerNotifyGeoChange()
	return nil
}

func (s *ManagerService) GeoPreview(settings geolist.Settings) (GeoPreview, error) {
	countries, _ := geoCountries()
	stats, _, err := geoComputeStats(countries[0], settings)
	return GeoPreview{Stats: stats}, err
}

func (s *ManagerService) GeoRefresh() error {
	if s.elevatedToken == 0 {
		return errors.New("Administrator rights are required to refresh the geo-split list")
	}
	settings, err := geolist.LoadSettings()
	if err != nil {
		log.Printf("Geo-split: %v", err)
	}
	countries, _ := geoCountries()
	var firstErr error
	for _, country := range countries {
		if err := geoRefresh(country, settings); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// geoRefreshIfStale refreshes the list for country if it is older than the configured
// threshold, waiting at most timeout. On any failure the existing list stays in use.
func geoRefreshIfStale(country string, timeout time.Duration) {
	settings, err := geolist.LoadSettings()
	if err != nil {
		log.Printf("Geo-split: %v", err)
	}
	if !settings.UpdateOnStart {
		return
	}
	meta, _ := geolist.LoadMeta()
	if meta.Country == country && !meta.IsStale(settings.StaleHours, time.Now()) {
		return
	}
	log.Printf("Geo-split: %s list is missing or older than %dh, refreshing before start (waiting at most %v)", strings.ToUpper(country), settings.StaleHours, timeout)
	done := make(chan error, 1)
	go func() { done <- geoRefresh(country, settings) }()
	select {
	case err := <-done:
		if err != nil {
			log.Printf("Geo-split: refresh failed, starting with the existing list: %v", err)
		}
	case <-time.After(timeout):
		log.Printf("Geo-split: refresh still running after %v, starting with the existing list", timeout)
	}
}

// geoBackgroundRefresh checks list staleness shortly after the manager starts and
// then periodically, so that tunnels started at boot pick up a fresh list next time.
func geoBackgroundRefresh() {
	if services.StartedAtBoot() {
		jitterSleep(time.Minute*1, time.Minute*3)
	} else {
		jitterSleep(time.Second*20, time.Second*40)
	}
	for {
		settings, err := geolist.LoadSettings()
		if countries, enabled := geoCountries(); err == nil && settings.UpdateOnStart && enabled {
			meta, _ := geolist.LoadMeta()
			for _, country := range countries {
				if meta.Country != country || meta.IsStale(settings.StaleHours, time.Now()) {
					if err := geoRefresh(country, settings); err != nil {
						log.Printf("Geo-split: background refresh of %s failed: %v", strings.ToUpper(country), err)
					}
				}
			}
		}
		jitterSleep(time.Hour*6-time.Minute*5, time.Hour*6+time.Minute*5)
	}
}

// geoRefresh downloads both lists for country, validates them and replaces the cache.
// Nothing is written unless both lists pass validation.
func geoRefresh(country string, settings geolist.Settings) (err error) {
	geoRefreshLock.Lock()
	defer geoRefreshLock.Unlock()
	atomic.StoreUint32(&geoRefreshing, 1)
	defer atomic.StoreUint32(&geoRefreshing, 0)
	IPCServerNotifyGeoChange()
	defer IPCServerNotifyGeoChange()

	meta, _ := geolist.LoadMeta()
	prev4, prev6 := 0, 0
	if meta.Country == country {
		prev4, prev6 = meta.CountV4, meta.CountV6
	}
	now := time.Now()
	defer func() {
		if err != nil {
			meta.LastAttempt = now
			meta.LastError = err.Error()
			if saveErr := meta.Save(); saveErr != nil {
				log.Printf("Geo-split: unable to record refresh error: %v", saveErr)
			}
		}
	}()

	source4 := settings.Source(geolist.IPv4, country)
	source6 := settings.Source(geolist.IPv6, country)
	raw4, err := geoFetch(source4)
	if err != nil {
		return fmt.Errorf("IPv4 list: %w", err)
	}
	raw6, err := geoFetch(source6)
	if err != nil {
		return fmt.Errorf("IPv6 list: %w", err)
	}
	if _, err = geolist.Validate(raw4, geolist.IPv4, prev4); err != nil {
		return err
	}
	if _, err = geolist.Validate(raw6, geolist.IPv6, prev6); err != nil {
		return err
	}
	count4, sha4, err := geolist.Store(country, geolist.IPv4, raw4, prev4)
	if err != nil {
		return err
	}
	count6, sha6, err := geolist.Store(country, geolist.IPv6, raw6, prev6)
	if err != nil {
		return err
	}
	meta = geolist.Meta{
		Country:     country,
		SourceV4:    source4,
		SourceV6:    source6,
		FetchedAt:   now,
		SHA256V4:    sha4,
		SHA256V6:    sha6,
		CountV4:     count4,
		CountV6:     count6,
		LastAttempt: now,
	}
	if err = meta.Save(); err != nil {
		return err
	}
	log.Printf("Geo-split: refreshed %s lists: %d IPv4 and %d IPv6 prefixes", strings.ToUpper(country), count4, count6)
	return nil
}

// geoFetch reads a list from an http(s) URL through WinHTTP, or from a local path.
func geoFetch(source string) ([]byte, error) {
	source = strings.TrimSpace(source)
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		f, err := os.Open(source)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return readLimited(f)
	}
	https := u.Scheme == "https"
	port := uint16(80)
	if https {
		port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid port in %s", source)
		}
		port = uint16(n)
	}
	session, err := winhttp.NewSession(version.UserAgent())
	if err != nil {
		return nil, err
	}
	defer session.Close()
	connection, err := session.Connect(u.Hostname(), port, https)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	response, err := connection.Get(path, true)
	if err != nil {
		return nil, err
	}
	defer response.Close()
	if code, err := response.StatusCode(); err == nil && code != 200 {
		return nil, fmt.Errorf("HTTP %d from %s", code, u.Host)
	}
	return readLimited(response)
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, geolist.MaxListBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > geolist.MaxListBytes {
		return nil, fmt.Errorf("list exceeds %d bytes", geolist.MaxListBytes)
	}
	return data, nil
}
