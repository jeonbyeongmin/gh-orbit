package git

import (
	"reflect"
	"testing"
)

func TestVendorFromEmailMatchesWhitelist(t *testing.T) {
	tests := []struct {
		email      string
		wantVendor AIVendor
		wantOK     bool
	}{
		{"noreply@anthropic.com", VendorAnthropic, true},
		{"agent@openai.com", VendorOpenAI, true},
		{"bot@cursor.sh", VendorCursor, true},
		{"gemini@google.com", VendorGoogle, true},
		{"  agent@OPENAI.com  ", VendorOpenAI, true}, // case + whitespace tolerance
		{"qa@example.com", "", false},
		{"", "", false},
		// Lookalikes must not match.
		{"agent@anthropic.co", "", false},
		{"agent@not-openai.com", "", false},
		{"agent@cursor.sh.attacker.com", "", false},
	}
	for _, tt := range tests {
		gotV, gotOK := VendorFromEmail(tt.email)
		if gotV != tt.wantVendor || gotOK != tt.wantOK {
			t.Errorf("VendorFromEmail(%q) = (%q, %v), want (%q, %v)", tt.email, gotV, gotOK, tt.wantVendor, tt.wantOK)
		}
	}
}

func TestParseCoAuthorsExtractsEmails(t *testing.T) {
	body := `fix: foo

Some prose body.

Co-Authored-By: Claude <noreply@anthropic.com>
Co-authored-by: Bob Bot <agent@openai.com>
not-a-coauthor: ignored
Co-Authored-By: malformed (no angle brackets)
`
	got := ParseCoAuthors(body)
	want := []string{"noreply@anthropic.com", "agent@openai.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseCoAuthors = %+v, want %+v", got, want)
	}
}

func TestParseCoAuthorsEmpty(t *testing.T) {
	if got := ParseCoAuthors(""); len(got) != 0 {
		t.Errorf("empty body should yield no co-authors, got %+v", got)
	}
	if got := ParseCoAuthors("just a subject line\n"); len(got) != 0 {
		t.Errorf("body without trailers should yield no co-authors, got %+v", got)
	}
}

func TestCommitAIVendorsDedupesByVendor(t *testing.T) {
	body := `feat: x

Co-Authored-By: A <a@anthropic.com>
Co-Authored-By: B <b@anthropic.com>
Co-Authored-By: C <c@openai.com>
Co-Authored-By: D <d@example.com>
`
	got := CommitAIVendors(body)
	want := []AIVendor{VendorAnthropic, VendorOpenAI}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CommitAIVendors = %+v, want %+v", got, want)
	}
}

func TestCommitAIVendorsEmptyOnNoAI(t *testing.T) {
	body := "fix: bug\n\nCo-Authored-By: Human <human@example.com>\n"
	if got := CommitAIVendors(body); len(got) != 0 {
		t.Errorf("non-AI co-authors should yield empty vendors, got %+v", got)
	}
}
