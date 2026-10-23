package sched

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Plain-language errors for the Updates feed. Go network errors read like
// `Get "https://nyaa.si/?...": dial tcp 1.2.3.4:443: connectex: A connection
// attempt failed because ...`; the feed shows what happened, what to check and
// when the next try is, and keeps the raw text in data.detail.

// loopLabels name each background loop the way the UI talks about it.
var loopLabels = map[string]string{
	"conveyor": "Release search",
	"airing":   "Airing update (AniList)",
	"qbit":     "qBittorrent sync",
	"scan":     "Library scan",
	"player":   "Player watch",
}

func loopLabel(name string) string {
	if l, ok := loopLabels[name]; ok {
		return l
	}
	return name
}

var (
	reURL    = regexp.MustCompile(`https?://([^/\s"]+)`)
	reHTTP   = regexp.MustCompile(`(?:returned|HTTP) (\d{3})`)
	reRetHst = regexp.MustCompile(`([A-Za-z0-9.-]+(?::\d+)?) returned \d{3}`)
	reStep   = regexp.MustCompile(`^nyaa "([^"]+)": `)
	reMore   = regexp.MustCompile(` \(and (\d+) more\)$`)
	reWinErr = regexp.MustCompile(`: (connectex|wsarecv|wsasend): `)
)

// plainError translates err into a sentence a person can act on. ok is false
// when the error is not a network or HTTP failure it knows; the caller then
// shows the raw text.
func plainError(err error) (msg string, ok bool) {
	if err == nil {
		return "", false
	}
	raw := err.Error()
	host := ""
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			host = u.Host
		}
	}
	if host == "" {
		if m := reURL.FindStringSubmatch(raw); m != nil {
			host = m[1]
		} else if m := reRetHst.FindStringSubmatch(raw); m != nil {
			host = m[1]
		}
	}
	who := host
	if who == "" {
		who = "The server"
	}
	low := strings.ToLower(raw)
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns) || strings.Contains(low, "no such host"):
		return who + " could not be looked up (DNS failed). Check your internet connection or VPN", true
	case strings.Contains(low, "connection refused") || strings.Contains(low, "actively refused"):
		return who + " refused the connection. The service is down or the address in Settings is wrong", true
	case strings.Contains(low, "certificate") || strings.Contains(low, "x509"):
		return who + " has a certificate problem (HTTPS check failed). A VPN or proxy may be in the way", true
	case isTimeout(err) || strings.Contains(low, "timeout") || strings.Contains(low, "timed out") ||
		strings.Contains(low, "deadline exceeded") || strings.Contains(low, "did not properly respond") ||
		strings.Contains(low, "failed to respond"):
		return who + " did not answer in time. Check your internet connection or VPN; the site may also be down", true
	case strings.Contains(low, "connection reset") || strings.Contains(low, "forcibly closed") ||
		strings.Contains(low, "unexpected eof") || strings.HasSuffix(low, ": eof") ||
		strings.Contains(low, "network is unreachable") || reWinErr.MatchString(raw):
		return who + " dropped the connection. Check your internet connection or VPN", true
	}
	if m := reHTTP.FindStringSubmatch(raw); m != nil {
		code := m[1]
		switch {
		case code == "401" || code == "403":
			return who + " refused the request (HTTP " + code + "). It may be blocking automated requests for a while", true
		case code == "404":
			return who + " says the address does not exist (HTTP 404). Check the address in Settings", true
		case code == "429":
			return who + " is rate-limiting (HTTP 429): too many requests. Nothing to do, it clears by itself", true
		case code[0] == '5':
			return who + " is having trouble on its side (HTTP " + code + ")", true
		}
	}
	return "", false
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// loopErrorMessage is the feed line for a failed loop run: the loop's label,
// the step that failed, the plain explanation and when the next try is.
// Example: Release search failed while searching "black clover": nyaa.si did
// not answer in time. Check your internet connection or VPN; the site may
// also be down. Next try in 30 min (and 2 more shows).
// Without a translation it falls back to the label plus the raw error.
func loopErrorMessage(name string, err error, retry time.Duration) string {
	raw := err.Error()
	more := ""
	if m := reMore.FindStringSubmatch(raw); m != nil {
		raw = strings.TrimSuffix(raw, m[0])
		more = fmt.Sprintf(" (and %s more)", m[1])
	}
	plain, ok := plainError(err)
	if !ok {
		return loopLabel(name) + ": " + raw + more
	}
	step := ""
	if m := reStep.FindStringSubmatch(raw); m != nil {
		step = fmt.Sprintf(" while searching %q", m[1])
	}
	msg := loopLabel(name) + " failed" + step + ": " + plain + "."
	tail := ""
	if retry > 0 {
		tail = "Next try in " + humanDur(retry)
	}
	if tail += more; tail != "" {
		msg += " " + strings.TrimSpace(tail) + "."
	}
	return msg
}

func humanDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d%time.Hour == 0:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
}
