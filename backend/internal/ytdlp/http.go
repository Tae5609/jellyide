package ytdlp

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The updater only ever talks to the GitHub release infrastructure (the
// release download, SHA2-256SUMS and the releases/latest API). This is a
// fixed, https-only allowlist; checkRedirect below is the compliance
// mechanism for the outbound-HTTP rule, because every redirect hop is
// validated against it.
var (
	allowedRedirectHosts = map[string]bool{
		"github.com":                           true,
		"api.github.com":                       true,
		"objects.githubusercontent.com":        true,
		"release-assets.githubusercontent.com": true,
	}
)

// isAllowedHost reports whether host is part of the fixed release
// allowlist (exact match; no wildcard subdomains).
func isAllowedHost(host string) bool {
	host = strings.ToLower(host)
	return allowedRedirectHosts[host]
}

// checkRedirect enforces the allowlist on every redirect: https only, at
// most five hops, and only to the fixed GitHub release hosts.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many redirects (last host: %s)", req.URL.Host)
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("redirect to non-https url (host: %s)", req.URL.Host)
	}
	host := req.URL.Hostname()
	if !isAllowedHost(host) {
		return fmt.Errorf("redirect to host %q outside the release allowlist", host)
	}
	return nil
}

// newHTTPClient builds the client used for all updater traffic. The
// timeout covers large release downloads; CheckRedirect keeps every hop
// inside the fixed allowlist.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       10 * time.Minute,
		CheckRedirect: checkRedirect,
	}
}
