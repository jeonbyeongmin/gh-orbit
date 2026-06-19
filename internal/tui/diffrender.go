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

// diffTheme bundles every color the diff renderer needs. syntaxStyle is the
// chroma style the code foreground comes from. base* tints a whole changed
// line; emph* (louder) marks the word-level span. *Marker is the leading +/-
// glyph. metaFg/hunkFg color the `diff --git`/`index`/`---`/`+++` headers and
// the `@@` hunk headers. emphFg overrides the syntax foreground on a word-level
// span so the changed text stays legible over the louder emph background even
// when its token color is dim (a gray comment is the case that breaks
// otherwise). Hex colors are truecolor — lipgloss degrades to the nearest 256
// color on lesser terminals; bare numbers are 256-color indices.
//
// key is the stable [diff] theme config value; name + dark drive the Settings
// picker row.
type diffTheme struct {
	key  string
	name string
	dark bool

	syntaxStyle string
	addBaseBg   string
	delBaseBg   string
	addEmphBg   string
	delEmphBg   string
	addMarker   string
	delMarker   string
	metaFg      string
	hunkFg      string
	emphFg      string
}

// diffThemes is the Settings picker order: the two dark themes, then the two
// light. Index 0 is the default for an unset / unknown [diff] theme — GitHub
// Dark, the familiar reviewer palette. (catppuccin-mocha, the original tuning,
// is one slot over.)
var diffThemes = []diffTheme{
	{
		key: "github-dark", name: "GitHub Dark", dark: true,
		syntaxStyle: "github-dark",
		addBaseBg:   "#12261e", delBaseBg: "#25171c",
		addEmphBg: "#1f6f33", delEmphBg: "#7c2b2e",
		addMarker: "#3fb950", delMarker: "#f85149",
		metaFg: "#6e7681", hunkFg: "#58a6ff", emphFg: "#f0f6fc",
	},
	{
		key: "catppuccin-mocha", name: "Catppuccin Mocha", dark: true,
		syntaxStyle: "catppuccin-mocha",
		addBaseBg:   "#243c1d", delBaseBg: "#4a1e1b",
		addEmphBg: "#3c6a32", delEmphBg: "#83372f",
		addMarker: "#a6e3a1", delMarker: "#f38ba8",
		metaFg: "240", hunkFg: "75", emphFg: "#f2f3f8",
	},
	{
		key: "catppuccin-latte", name: "Catppuccin Latte", dark: false,
		syntaxStyle: "catppuccin-latte",
		addBaseBg:   "#e3f0e1", delBaseBg: "#fbe4e6",
		addEmphBg: "#c5e6bf", delEmphBg: "#f4c4ca",
		addMarker: "#40a02b", delMarker: "#d20f39",
		metaFg: "#8c8fa1", hunkFg: "#1e66f5", emphFg: "#4c4f69",
	},
	{
		key: "github-light", name: "GitHub Light", dark: false,
		syntaxStyle: "github",
		addBaseBg:   "#e6ffec", delBaseBg: "#ffebe9",
		addEmphBg: "#abf2bc", delEmphBg: "#ffc1c0",
		addMarker: "#1a7f37", delMarker: "#cf222e",
		metaFg: "#6e7781", hunkFg: "#0550ae", emphFg: "#1f2328",
	},
}

// activeDiffTheme is the theme renderDiffContent paints with — a single
// app-wide setting (one diff theme at a time), set once in Model.New from prefs
// and re-pointed when the Settings picker cycles it. A package global to mirror
// the existing chrome globals (Version, the lipgloss style vars) and keep
// renderDiffContent's signature stable; it defaults to diffThemes[0] (GitHub
// Dark) so any render before New (tests) has a valid theme.
var activeDiffTheme = diffThemes[0]

// diffThemeIndex returns the diffThemes index for a config key, or 0 (the
// default dark theme) for "" / an unrecognized key.
func diffThemeIndex(key string) int {
	for i, t := range diffThemes {
		if t.key == key {
			return i
		}
	}
	return 0
}

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
	dlMeta    diffLineKind = iota // diff --git, index, ---/+++, \ No newline, Binary, blank
	dlHunk                        // @@ -a,b +c,d @@
	dlContext                     // leading space
	dlDel                         // leading -
	dlAdd                         // leading +
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

	th := activeDiffTheme
	r := newDiffRenderer(th)

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
			b.WriteString(styledSeg(ln, th.hunkFg, ""))
		case dlMeta:
			b.WriteString(styledSeg(ln, th.metaFg, ""))
		case dlContext:
			b.WriteString(r.contentLine(' ', codes[i], lex[i], "", "", "", nil, syntax, width))
		case dlAdd:
			b.WriteString(r.contentLine('+', codes[i], lex[i], th.addBaseBg, th.addEmphBg, th.addMarker, changed[i], syntax, width))
		case dlDel:
			b.WriteString(r.contentLine('-', codes[i], lex[i], th.delBaseBg, th.delEmphBg, th.delMarker, changed[i], syntax, width))
		}
	}
	return b.String()
}

type diffRenderer struct {
	theme    diffTheme
	style    *chroma.Style
	lexCache map[string]chroma.Lexer
}

func newDiffRenderer(th diffTheme) *diffRenderer {
	return &diffRenderer{
		theme:    th,
		style:    styles.Get(th.syntaxStyle), // falls back to a registered style if unknown
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
		// On a word-level changed span, force the theme's emph foreground so the
		// text stays legible over the emph background regardless of its token color.
		segFg := fg
		if emphBg != "" && bg == emphBg {
			segFg = r.theme.emphFg
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
