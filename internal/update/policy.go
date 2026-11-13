package update

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/WhitenWhiten/topophany/internal/management"
)

type Policy struct {
	Schema        int    `json:"schema"`
	Minimum       string `json:"minimum_version"`
	MaxMajor      int64  `json:"max_major"`
	AnyTime       bool   `json:"any_time"`
	Timezone      string `json:"timezone,omitempty"`
	Start         string `json:"window_start,omitempty"`
	End           string `json:"window_end,omitempty"`
	Socket        string `json:"helper_socket"`
	HealthSeconds int    `json:"health_seconds"`
}

func LoadPolicy(path string, system bool) (Policy, error) {
	b, e := management.ReadLocal(path, 64<<10, system, false)
	if e != nil {
		return Policy{}, e
	}
	var p Policy
	if e = management.Decode(b, &p, 64<<10); e != nil {
		return p, e
	}
	return p, p.Validate()
}
func (p Policy) Validate() error {
	if p.Schema != 1 || p.MaxMajor < 0 || p.HealthSeconds < 30 || p.HealthSeconds > 600 || !filepath.IsAbs(p.Socket) {
		return errors.New("invalid_update_policy")
	}
	if _, e := Version(p.Minimum); e != nil {
		return e
	}
	if !p.AnyTime {
		if p.Timezone == "" {
			return errors.New("explicit_update_timezone_required")
		}
		if _, e := time.LoadLocation(p.Timezone); e != nil {
			return errors.New("invalid_update_timezone")
		}
		if _, e := clockMinute(p.Start); e != nil {
			return e
		}
		if _, e := clockMinute(p.End); e != nil {
			return e
		}
		if p.Start == p.End {
			return errors.New("empty_update_window")
		}
	}
	return nil
}
func clockMinute(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, errors.New("invalid_update_window")
	}
	h, e := strconv.Atoi(s[:2])
	if e != nil || h < 0 || h > 23 {
		return 0, errors.New("invalid_update_window")
	}
	m, e := strconv.Atoi(s[3:])
	if e != nil || m < 0 || m > 59 {
		return 0, errors.New("invalid_update_window")
	}
	return h*60 + m, nil
}
func (p Policy) Window(now time.Time) bool {
	if p.Validate() != nil {
		return false
	}
	if p.AnyTime {
		return true
	}
	loc, _ := time.LoadLocation(p.Timezone)
	t := now.In(loc)
	// Neither occurrence of a repeated local minute starts an update. Use the
	// actual zone transition, including half-hour/two-hour DST changes.
	startBound, endBound := t.ZoneBounds()
	for _, boundary := range []time.Time{startBound, endBound} {
		if boundary.IsZero() {
			continue
		}
		_, before := boundary.Add(-time.Second).In(loc).Zone()
		_, after := boundary.In(loc).Zone()
		if before <= after {
			continue
		}
		shift := time.Duration(before-after) * time.Second
		for _, delta := range []time.Duration{-shift, shift} {
			if now.Add(delta).In(loc).Format("2006-01-02 15:04") == t.Format("2006-01-02 15:04") {
				return false
			}
		}
	}
	start, _ := clockMinute(p.Start)
	end, _ := clockMinute(p.End)
	minute := t.Hour()*60 + t.Minute()
	if end > start {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}
func (p Policy) Allows(target, highest string) error {
	v, e := Version(target)
	if e != nil {
		return e
	}
	if v[0] > p.MaxMajor {
		return errors.New("major_upgrade_not_authorized")
	}
	for _, floor := range []string{p.Minimum, highest} {
		if floor == "" {
			continue
		}
		n, e := Compare(target, floor)
		if e != nil || n < 0 {
			return errors.New("downgrade_denied")
		}
	}
	return nil
}
func LoadRoot(path string, system bool) (Root, error) {
	b, e := management.ReadLocal(path, 64<<10, system, false)
	if e != nil {
		return Root{}, e
	}
	var r Root
	if e = management.Decode(b, &r, 64<<10); e != nil {
		return r, e
	}
	return r, r.Validate(time.Now())
}

// Target is the exact deployment payload. It contains no URL or executable path.
type Target struct {
	Version        string `json:"version"`
	ReleaseID      int64  `json:"release_id"`
	AssetID        int64  `json:"asset_id"`
	ArchiveSHA256  string `json:"archive_sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

func (t Target) Matches(r Resolution) bool {
	return t.Version == r.Manifest.Version && t.ReleaseID == r.Release.ID && t.AssetID == r.Artifact.ID && t.ArchiveSHA256 == r.Artifact.SHA256 && t.ManifestSHA256 == management.Digest(management.Marshal(r.Envelope))
}
func TargetFor(r Resolution) Target {
	return Target{r.Manifest.Version, r.Release.ID, r.Artifact.ID, r.Artifact.SHA256, management.Digest(management.Marshal(r.Envelope))}
}
func (t Target) Validate() error {
	if _, e := Version(t.Version); e != nil {
		return e
	}
	if t.ReleaseID <= 0 || t.AssetID <= 0 || !management.HashOK(t.ArchiveSHA256) || !management.HashOK(t.ManifestSHA256) {
		return errors.New("invalid_update_target")
	}
	return nil
}
func NormalizeInstalledVersion(s string) string { return "v" + strings.TrimPrefix(s, "v") }
