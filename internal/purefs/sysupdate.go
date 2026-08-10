package purefs

import (
	"bufio"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// DdiRelease is one publishable version resolved from a systemd-sysupdate
// v1 repository listing: the split artifacts a live ISO needs. The layout
// is what mkosi's SplitArtifacts publishes and sysupdate's *.transfer
// definitions consume (observed contract: frostyard/snosi
// shared/native-ab/channels/*/tree/usr/lib/sysupdate.d/):
//
//	<base>_<version>.efi                          UKI
//	<base>_<version>_<partuuid>.root.raw[.xz]     root partition (EROFS)
//	<base>_<version>_<partuuid>.root-verity.raw[.xz]
//
// where <base> is e.g. "snow-ab". The verity artifact is not fetched for
// live media — the live overlay makes the root writable anyway, and the
// tbox live path mounts the EROFS by file, not by GPT partition. That is
// a real integrity downgrade vs the installed system, stated here on
// purpose.
type DdiRelease struct {
	Version string
	UKI     string // artifact filename
	Root    string // artifact filename (possibly .xz)
	UKISHA  string // sha256 from the manifest, "" if listing had none
	RootSHA string
}

var (
	ukiPat  = regexp.MustCompile(`^([A-Za-z0-9.-]+)_([A-Za-z0-9.+-]+)\.efi$`)
	rootPat = regexp.MustCompile(`^([A-Za-z0-9.-]+)_([A-Za-z0-9.+-]+)_[0-9a-fA-F-]{36}\.root\.raw(\.xz)?$`)
)

// ResolveDdiRelease parses a SHA256SUMS manifest (sysupdate's version
// source: "<sha256>  <filename>" lines; bare filename lines are accepted
// too, for indexes without checksums) and returns the newest version
// that publishes BOTH a UKI and a root artifact. base filters to one
// artifact stem ("snow-ab"); empty matches any single stem present —
// ambiguity between stems is an error rather than a guess.
func ResolveDdiRelease(manifest, base string) (*DdiRelease, error) {
	type half struct{ uki, root, ukiSHA, rootSHA string }
	byVersion := map[string]*half{}
	stems := map[string]bool{}

	sc := bufio.NewScanner(strings.NewReader(manifest))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sha, name := "", line
		if fields := strings.Fields(line); len(fields) == 2 && len(fields[0]) == 64 {
			sha, name = fields[0], strings.TrimPrefix(fields[1], "*")
		}
		if m := ukiPat.FindStringSubmatch(name); m != nil {
			if base != "" && m[1] != base {
				continue
			}
			stems[m[1]] = true
			h := byVersion[m[2]]
			if h == nil {
				h = &half{}
				byVersion[m[2]] = h
			}
			h.uki, h.ukiSHA = name, sha
		} else if m := rootPat.FindStringSubmatch(name); m != nil {
			if base != "" && m[1] != base {
				continue
			}
			stems[m[1]] = true
			h := byVersion[m[2]]
			if h == nil {
				h = &half{}
				byVersion[m[2]] = h
			}
			h.root, h.rootSHA = name, sha
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if base == "" && len(stems) > 1 {
		names := make([]string, 0, len(stems))
		for s := range stems {
			names = append(names, s)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("manifest lists multiple artifact stems %v — pass one explicitly", names)
	}

	var versions []string
	for v, h := range byVersion {
		if h.uki != "" && h.root != "" {
			versions = append(versions, v)
		}
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no version in the manifest publishes both a UKI and a root artifact")
	}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[i], versions[j]) })
	v := versions[len(versions)-1]
	h := byVersion[v]
	return &DdiRelease{Version: v, UKI: h.uki, Root: h.root, UKISHA: h.ukiSHA, RootSHA: h.rootSHA}, nil
}

// versionLess is a strverscmp-style comparison: digit runs compare
// numerically, everything else byte-wise — the same ordering sysupdate
// applies to @v captures.
func versionLess(a, b string) bool {
	for a != "" && b != "" {
		ad, an := splitLead(a)
		bd, bn := splitLead(b)
		if isNum(ad) && isNum(bd) {
			switch compareNum(ad, bd) {
			case -1:
				return true
			case 1:
				return false
			}
		} else if ad != bd {
			return ad < bd
		}
		a, b = an, bn
	}
	return a == "" && b != ""
}

func splitLead(s string) (lead, rest string) {
	num := s[0] >= '0' && s[0] <= '9'
	for i := 0; i < len(s); i++ {
		if (s[i] >= '0' && s[i] <= '9') != num {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

func isNum(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// compareNum compares decimal runs without converting them to a machine
// integer. Release versions are normally timestamps, but keeping this
// arbitrary-width avoids silently ordering very large future versions as 0
// when strconv.ParseUint overflows.
func compareNum(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
