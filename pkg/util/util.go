package util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"github.com/miekg/dns"
)

// NormalizeSuffixes lower-cases each suffix, strips any trailing dot, and
// prepends a leading dot so suffix "cluster.local" matches
// "foo.cluster.local" but not "notcluster.local". Empty/duplicate entries
// are dropped.
func NormalizeSuffixes(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(strings.ToLower(s))
		s = strings.TrimSuffix(s, ".")
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, ".") {
			s = "." + s
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func GetResolvConfDNSServers(resolvconf string) ([]string, error) {
	resolvConf, err := dns.ClientConfigFromFile(resolvconf)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	var servers []string
	for _, s := range resolvConf.Servers {
		servers = append(servers, net.JoinHostPort(stripZone(s), resolvConf.Port))
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("no DNS servers found in %s", resolvconf)
	}

	return servers, nil
}

// Strips the zone, but preserves any port that comes after the zone
func stripZone(host string) string {
	if strings.Contains(host, "%") {
		lastPercent := strings.LastIndex(host, "%")
		newHost := host[:lastPercent]
		return newHost
	}
	return host
}

// SafeConcatName is copied from github.com/loft-sh/vcluster/pkg/util/translate.SafeConcatName
func SafeConcatName(name ...string) string {
	fullPath := strings.Join(name, "-")
	if len(fullPath) > 63 {
		digest := sha256.Sum256([]byte(fullPath))
		return strings.ReplaceAll(fullPath[0:52]+"-"+hex.EncodeToString(digest[0:])[0:10], ".-", "-")
	}
	return fullPath
}
