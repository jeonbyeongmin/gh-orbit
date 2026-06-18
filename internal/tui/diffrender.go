// In-process unified-diff styling. gh-orbit used to let `git -c color.ui=always`
// paint the patch and just piped that ANSI into the viewport. To layer syntax
// highlighting and word-level (intra-line) emphasis on top, the cockpit now
// fetches the diff *uncolored* and renders it here: chroma colors each code
// line by language, a subtle red/green background marks removed/added lines,
// and the changed spans of a paired -/+ line get a brighter background so the
// reviewer's eye lands on what actually changed.
//
// Every input line maps to exactly one output line, so the caller's hunk- and
// file-boundary line indices (parseHunkStarts / parseFileBoundaries) stay valid
// against the rendered text. The result is width-independent — the viewport
// truncates long lines itself (MaxWidth), so the background sits behind the
// text only and never needs padding.
//
// Styling is threaded per segment (one lipgloss style carrying both the chroma
// foreground and the diff background), never by wrapping an already-styled
// string — a `.Render` over text that already carries ANSI breaks on the inner
// reset (see lcSelectedStyle's note).
package tui

import (
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// diffSyntaxStyleName is the chroma style the diff foreground colors come from.
// A dark, muted palette to sit under gh-orbit's chrome — change this one line
// to retune syntax colors.
const diffSyntaxStyleName = "catppuccin-mocha"

// Diff background palette, tuned for a dark terminal. base* tints the whole
// changed line; emph* (louder) marks the word-level span. Hex (truecolor) —
// lipgloss degrades to the nearest 256 color on lesser terminals.
const (
	diffAddBaseBg = "#243c1d" // added-line bar
	diffDelBaseBg = "#4a1e1b" // removed-line bar
	diffAddEmphBg = "#3c6a32" // changed-word backgrounds: louder than the bar
	diffDelEmphBg = "#83372f"
	diffAddMarker = "#a6e3a1" // the leading +/- glyph
	diffDelMarker = "#f38ba8"
	diffMetaFg    = "240" // diff --git / index / ---/+++ headers
	diffHunkFg    = "75"  // @@ … @@ hunk headers
	// diffEmphFg overrides the syntax foreground on word-level changed spans, so
	// the changed text stays legible on the louder emph background even when its
	// token color is dim (a gray comment is the case that breaks otherwise).
	diffEmphFg = "#f2f3f8"
)

// Render guards: above these the cost of per-line tokenisation + LCS isn't
// worth it, so the line (or whole patch) falls back to plain +/- backgrounds
// with no syntax/word-level work.
const (
	maxDiffRenderLines = 4000
	maxDiffLineRunes   = 2000
	diffTabWidth       = 4
)

type diffLineKind int

const (
	dlMeta diffLineKind = iota // diff --git, index, ---/+++, \ No newline, Binary, blank
	dlHunk                     // @@ -a,b +c,d @@
	dlContext                  // leading space
	dlDel                      // leading -
	dlAdd                      // leading +
)

// renderDiffContent styles a plain (uncolored) unified diff for the viewport.
// width is the viewport content width: removed/added lines pad their background
// out to it so each reads as a continuous full-width bar (width ≤ 0 skips the
// padding — background sits behind the text only). Empty in → empty out.
// Cached per load + width; the caller re-applies only the selected-hunk accent
// on refresh, and re-renders when the width changes.
func renderDiffContent(patch string, width int) string {
	if patch == "" {
		return ""
	}
	lines := strings.Split(patch, "\n")
	syntax := len(lines) <= maxDiffRenderLines

	r := newDiffRenderer()

	kinds := make([]diffLineKind, len(lines))
	codes := make([]string, len(lines)) // tab-expanded code (content lines only)
	lex := make([]chroma.Lexer, len(lines))

	var cur chroma.Lexer
	for i, ln := range lines {
		k := classifyDiffLine(ln)
		kinds[i] = k
		switch k {
		case dlMeta:
			if path, ok := parseFileDiffHeader(ln); ok {
				cur = r.lexerFor(path)
			}
		case dlContext, dlAdd, dlDel:
			codes[i] = expandTabs(ln[1:], diffTabWidth)
			lex[i] = cur
		}
	}

	changed := wordLevelRanges(kinds, codes, syntax)

	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch kinds[i] {
		case dlHunk:
			b.WriteString(styledSeg(ln, diffHunkFg, ""))
		case dlMeta:
			b.WriteString(styledSeg(ln, diffMetaFg, ""))
		case dlContext:
			b.WriteString(r.contentLine(' ', codes[i], lex[i], "", "", "", nil, syntax, width))
		case dlAdd:
			b.WriteString(r.contentLine('+', codes[i], lex[i], diffAddBaseBg, diffAddEmphBg, diffAddMarker, changed[i], syntax, width))
		case dlDel:
			b.WriteString(r.contentLine('-', codes[i], lex[i], diffDelBaseBg, diffDelEmphBg, diffDelMarker, changed[i], syntax, width))
		}
	}
	return b.String()
}

type diffRenderer struct {
	style    *chroma.Style
	lexCache map[string]chroma.Lexer
}

func newDiffRenderer() *diffRenderer {
	return &diffRenderer{
		style:    styles.Get(diffSyntaxStyleName), // falls back to a registered style if unknown
		lexCache: map[string]chroma.Lexer{},
	}
}

// lexerFor resolves (and caches) a coalesced lexer for a destination path,
// falling back to the plain-text lexer for unknown extensions.
func (r *diffRenderer) lexerFor(path string) chroma.Lexer {
	if l, ok := r.lexCache[path]; ok {
		return l
	}
	l := lexers.Match(path)
	if l == nil {
		l = lexers.Fallback
	}
	l = chroma.Coalesce(l)
	r.lexCache[path] = l
	return l
}

// fgFor returns the chroma foreground hex for a token type, or "" when the
// style leaves it at the default.
func (r *diffRenderer) fgFor(tt chroma.TokenType) string {
	if e := r.style.Get(tt); e.Colour.IsSet() {
		return e.Colour.String()
	}
	return ""
}

// contentLine renders one context/add/del line: a marker cell, then the code
// split into segments that each carry a chroma foreground and the line's
// background (the louder emph background over word-level changed runes). When
// syntax is off (huge diff / over-long line) the code renders as one plain
// segment with just the backgrounds.
func (r *diffRenderer) contentLine(marker rune, code string, lexer chroma.Lexer, baseBg, emphBg, markerFg string, changed [][2]int, syntax bool, width int) string {
	var b strings.Builder
	b.WriteString(styledSeg(string(marker), markerFg, baseBg))

	runes := []rune(code)
	if !syntax || lexer == nil || len(runes) > maxDiffLineRunes {
		r.writeSpans(&b, runes, 0, "", baseBg, emphBg, changed)
	} else {
		pos := 0
		for _, t := range tokenize(lexer, code) {
			tr := []rune(t.Value)
			r.writeSpans(&b, tr, pos, r.fgFor(t.Type), baseBg, emphBg, changed)
			pos += len(tr)
		}
	}

	// Pad the background out to the viewport width so a changed line reads as a
	// continuous bar. Context lines (no background) are left unpadded.
	if baseBg != "" && width > 0 {
		if pad := width - 1 - runewidth.StringWidth(code); pad > 0 {
			b.WriteString(styledSeg(strings.Repeat(" ", pad), "", baseBg))
		}
	}
	return b.String()
}

// writeSpans emits runes[0:] (whose first rune sits at absolute offset base in
// the line) as the fewest segments possible, breaking only where the
// background flips between baseBg and emphBg per the changed rune ranges.
func (r *diffRenderer) writeSpans(b *strings.Builder, runes []rune, base int, fg, baseBg, emphBg string, changed [][2]int) {
	k := 0
	for k < len(runes) {
		bg := bgAt(base+k, changed, baseBg, emphBg)
		j := k + 1
		for j < len(runes) && bgAt(base+j, changed, baseBg, emphBg) == bg {
			j++
		}
		// On a word-level changed span, force a bright foreground so the text
		// stays legible over the emph background regardless of its token color.
		segFg := fg
		if emphBg != "" && bg == emphBg {
			segFg = diffEmphFg
		}
		b.WriteString(styledSeg(string(runes[k:j]), segFg, bg))
		k = j
	}
}

// bgAt is the background for the rune at offset i: the louder emph background
// inside a word-level changed range, the line's base background otherwise.
func bgAt(i int, changed [][2]int, baseBg, emphBg string) string {
	for _, rg := range changed {
		if i >= rg[0] && i < rg[1] {
			return emphBg
		}
	}
	return baseBg
}

// styledSeg renders text under an optional foreground/background. Empty color
// strings leave that attribute unset, so context lines (no background) and
// default-colored tokens stay plain.
func styledSeg(text, fg, bg string) string {
	st := lipgloss.NewStyle()
	if fg != "" {
		st = st.Foreground(lipgloss.Color(fg))
	}
	if bg != "" {
		st = st.Background(lipgloss.Color(bg))
	}
	return st.Render(text)
}

// tokenize runs the lexer over one line, returning its tokens. On a lexer error
// the whole line comes back as a single default-colored token so rendering
// still succeeds.
func tokenize(lexer chroma.Lexer, code string) []chroma.Token {
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return []chroma.Token{{Type: chroma.Text, Value: code}}
	}
	return it.Tokens()
}

// wordLevelRanges finds, for each line index, the changed rune ranges to
// emphasise. It pairs each maximal run of removed lines with the immediately
// following run of added lines, line-by-line, and word-diffs each pair. Lines
// with no pairing (or too-dissimilar pairs) get no emphasis (nil). Returns nil
// for the whole patch when syntax work is disabled.
func wordLevelRanges(kinds []diffLineKind, codes []string, syntax bool) [][][2]int {
	out := make([][][2]int, len(kinds))
	if !syntax {
		return out
	}
	i := 0
	for i < len(kinds) {
		if kinds[i] != dlDel {
			i++
			continue
		}
		dStart := i
		for i < len(kinds) && kinds[i] == dlDel {
			i++
		}
		dEnd := i
		if i >= len(kinds) || kinds[i] != dlAdd {
			continue
		}
		aStart := i
		for i < len(kinds) && kinds[i] == dlAdd {
			i++
		}
		aEnd := i
		n := min(dEnd-dStart, aEnd-aStart)
		for j := 0; j < n; j++ {
			delIdx, addIdx := dStart+j, aStart+j
			dr, ar := wordLevelChanged([]rune(codes[delIdx]), []rune(codes[addIdx]))
			out[delIdx] = dr
			out[addIdx] = ar
		}
	}
	return out
}

// wordMatchFloor is the minimum share of non-whitespace runes a paired -/+ line
// must hold in common before per-word emphasis is drawn. Below it the lines are
// more rewritten than edited — most words would light up, which reads as noise
// — so the full-width base bar carries the "this line changed" signal alone and
// emphasis is reserved for small, targeted edits (a renamed identifier, a
// changed number). Whitespace is excluded so shared indentation doesn't inflate
// the measure.
const wordMatchFloor = 0.40

// wordLevelChanged word-diffs two code lines and returns the changed rune
// ranges in each. A token is a maximal run of word runes or a single other
// rune; the LCS of the token streams is the unchanged part. Too-dissimilar
// pairs (see wordMatchFloor) return nil,nil.
func wordLevelChanged(a, b []rune) (aRanges, bRanges [][2]int) {
	at, bt := wordTokens(a), wordTokens(b)
	n, m := len(at), len(bt)
	if n == 0 || m == 0 {
		return nil, nil
	}
	// LCS length table (suffix DP).
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if at[i].text == bt[j].text {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	aMatch, bMatch := make([]bool, n), make([]bool, m)
	matchedNonWS := 0
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case at[i].text == bt[j].text:
			aMatch[i], bMatch[j] = true, true
			if !isBlankTok(at[i]) {
				matchedNonWS += at[i].n
			}
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	denom := max(nonWSRunes(at), nonWSRunes(bt))
	if denom == 0 || float64(matchedNonWS)/float64(denom) < wordMatchFloor {
		return nil, nil
	}
	return unmatchedRanges(at, aMatch), unmatchedRanges(bt, bMatch)
}

func isBlankTok(t wtok) bool { return strings.TrimSpace(t.text) == "" }

// nonWSRunes counts the runes in non-whitespace tokens.
func nonWSRunes(toks []wtok) int {
	total := 0
	for _, t := range toks {
		if !isBlankTok(t) {
			total += t.n
		}
	}
	return total
}

type wtok struct {
	text  string
	start int // rune offset of the token in its line
	n     int // rune count
}

// wordTokens splits a line into word tokens (maximal [\p{L}\p{N}_] runs) and
// single-rune punctuation/space tokens, tracking each token's rune offset.
func wordTokens(runes []rune) []wtok {
	var out []wtok
	i := 0
	for i < len(runes) {
		if isWordRune(runes[i]) {
			j := i
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
			out = append(out, wtok{text: string(runes[i:j]), start: i, n: j - i})
			i = j
		} else {
			out = append(out, wtok{text: string(runes[i]), start: i, n: 1})
			i++
		}
	}
	return out
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// unmatchedRanges merges runs of consecutive unmatched tokens into rune ranges
// [start, end). Tokens partition the line contiguously, so adjacent unmatched
// tokens collapse into one span.
func unmatchedRanges(toks []wtok, match []bool) [][2]int {
	var out [][2]int
	k := 0
	for k < len(toks) {
		if match[k] {
			k++
			continue
		}
		start := toks[k].start
		end := toks[k].start + toks[k].n
		k++
		for k < len(toks) && !match[k] {
			end = toks[k].start + toks[k].n
			k++
		}
		out = append(out, [2]int{start, end})
	}
	return out
}

// expandTabs replaces tabs with spaces to the next diffTabWidth stop so width
// math and word-level offsets stay rune-counted (a literal tab inside a
// background-styled segment renders unpredictably). Cheap no-op when tab-free.
func expandTabs(s string, tw int) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			pad := tw - col%tw
			for n := 0; n < pad; n++ {
				b.WriteByte(' ')
			}
			col += pad
			continue
		}
		b.WriteRune(r)
		col += runewidth.RuneWidth(r)
	}
	return b.String()
}

// classifyDiffLine buckets one unified-diff line. ---/+++ file headers are
// caught before the +/- content check; everything that isn't a hunk header,
// content, or context line (diff --git, index, \ No newline, Binary, the blank
// trailing split element) is meta.
func classifyDiffLine(line string) diffLineKind {
	if line == "" {
		return dlMeta
	}
	if strings.HasPrefix(line, "@@") {
		return dlHunk
	}
	if strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") {
		return dlMeta
	}
	switch line[0] {
	case '+':
		return dlAdd
	case '-':
		return dlDel
	case ' ':
		return dlContext
	default:
		return dlMeta
	}
}
