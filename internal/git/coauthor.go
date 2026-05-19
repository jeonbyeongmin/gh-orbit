// Co-author trailer parsing for AI vendor identification. Inputs are
// commit message bodies (`%B` from `git show`); outputs are the AI vendor
// labels the TUI renders as cyan chips next to the commit subject.
//
// Only four vendors are surfaced — anthropic.com, openai.com, cursor.sh,
// google.com — via exact `@<domain>` suffix matching. Lookalike domains
// (anthropic.co, openai-api.com, etc.) do not match, by design: a chip
// claiming an AI vendor is a trust signal, and ambiguous matches would
// dilute it.
package git

import (
	"strings"
)

// AIVendor is the human-readable label rendered inside the chip. Short,
// lowercase, ASCII — no emoji per Q24 D15 so the cockpit reads the same
// across every terminal and font.
type AIVendor string

const (
	VendorAnthropic AIVendor = "anthropic"
	VendorOpenAI    AIVendor = "openai"
	VendorCursor    AIVendor = "cursor"
	VendorGoogle    AIVendor = "google"
)

// vendorWhitelist pairs the exact `@<domain>` suffix with its vendor
// label. strings.HasSuffix matching with the `@` prefix on the suffix
// rules out lookalikes like `anthropic.co.uk` or `not-openai.com` —
// only addresses literally ending in `@anthropic.com` etc. qualify.
var vendorWhitelist = []struct {
	suffix string
	vendor AIVendor
}{
	{"@anthropic.com", VendorAnthropic},
	{"@openai.com", VendorOpenAI},
	{"@cursor.sh", VendorCursor},
	{"@google.com", VendorGoogle},
}

// VendorFromEmail returns the matching AIVendor for one email address.
// Match is case-insensitive (email domains are RFC-flat) and requires the
// full `@<domain>` suffix. Empty/missing matches return ok=false so
// callers can use the zero AIVendor without nil checks.
func VendorFromEmail(email string) (AIVendor, bool) {
	lower := strings.ToLower(strings.TrimSpace(email))
	if lower == "" {
		return "", false
	}
	for _, w := range vendorWhitelist {
		if strings.HasSuffix(lower, w.suffix) {
			return w.vendor, true
		}
	}
	return "", false
}

// coAuthorPrefix is git's conventional trailer key. Case-insensitive
// comparison is the practical default — agents and humans vary on
// capitalization ("Co-Authored-By", "Co-authored-by", etc.).
const coAuthorPrefix = "co-authored-by:"

// ParseCoAuthors walks a commit message body line-by-line and returns
// every `Co-Authored-By:` email it finds, in order. Lines without an
// `<email>` block are skipped (malformed trailer); duplicates are kept
// so the caller can count occurrences if needed.
//
// The parser intentionally does NOT require trailers to be in the final
// "trailer block" of the body — some agents (and many humans) emit
// `Co-Authored-By:` mid-body. RFC strictness here loses real signal.
func ParseCoAuthors(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < len(coAuthorPrefix) {
			continue
		}
		if !strings.EqualFold(trimmed[:len(coAuthorPrefix)], coAuthorPrefix) {
			continue
		}
		rest := strings.TrimSpace(trimmed[len(coAuthorPrefix):])
		// Format: "Name <email>". Pull the address between angle brackets.
		start := strings.IndexByte(rest, '<')
		end := strings.LastIndexByte(rest, '>')
		if start < 0 || end <= start+1 {
			continue
		}
		email := strings.TrimSpace(rest[start+1 : end])
		if email != "" {
			out = append(out, email)
		}
	}
	return out
}

// CommitAIVendors composes ParseCoAuthors + VendorFromEmail: returns the
// distinct AI vendor labels detected in a commit body, preserving
// first-seen order. Empty slice when no AI vendor appears. Callers use
// it to decide whether to render the cockpit's cyan chip (and which
// label).
func CommitAIVendors(body string) []AIVendor {
	emails := ParseCoAuthors(body)
	if len(emails) == 0 {
		return nil
	}
	var out []AIVendor
	seen := make(map[AIVendor]struct{}, 4)
	for _, e := range emails {
		v, ok := VendorFromEmail(e)
		if !ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
