package git

import "strings"

// commonRemotePrefixes avoids a second `git for-each-ref` for remote names.
// graph-color-config will make this configurable; until then, non-default
// remotes won't pair-merge with their local counterpart.
var commonRemotePrefixes = []string{"origin/", "upstream/", "fork/"}

// DecoratedRef is one classified token from a Commit.RefNames slice.
// ShortName has decoration prefixes ("tag: ", "HEAD -> ") stripped — chip
// renderers can use it directly.
type DecoratedRef struct {
	Kind      RefKind
	ShortName string
	IsHead    bool
}

// ChipRef is what the renderer ultimately draws: at most one entry per visual
// chip slot. Local + remote pairs that point at the same commit collapse into
// a single ChipRef with PairedRemote=true; the renderer decides how to mark
// the paired state.
type ChipRef struct {
	Kind         RefKind
	DisplayName  string
	IsHead       bool
	PairedRemote bool
}

// ParseDecoration turns the raw "%D" tokens of a single commit into typed
// refs. headDetached is true when a bare "HEAD" token appears (i.e. detached
// HEAD); in that case no DecoratedRef carries IsHead — chip rendering treats
// detached HEAD as its own standalone chip.
//
// "origin/HEAD" symbolic refs are dropped: they always point where another
// ref already does, so a chip would be redundant.
func ParseDecoration(tokens []string) (refs []DecoratedRef, headDetached bool) {
	if len(tokens) == 0 {
		return nil, false
	}
	refs = make([]DecoratedRef, 0, len(tokens))
	for _, raw := range tokens {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		switch {
		case t == "HEAD":
			headDetached = true
		case strings.HasPrefix(t, "HEAD -> "):
			name := strings.TrimPrefix(t, "HEAD -> ")
			refs = append(refs, DecoratedRef{
				Kind:      classifyRefName(name),
				ShortName: name,
				IsHead:    true,
			})
		case strings.HasPrefix(t, "tag: "):
			refs = append(refs, DecoratedRef{
				Kind:      RefKindTag,
				ShortName: strings.TrimPrefix(t, "tag: "),
			})
		default:
			if isSymbolicRemoteHead(t) {
				continue
			}
			refs = append(refs, DecoratedRef{
				Kind:      classifyRefName(t),
				ShortName: t,
			})
		}
	}
	return refs, headDetached
}

// MergeLocalRemotePairs collapses (local "X" + remote "<prefix>/X") into a
// single local ChipRef with PairedRemote=true. The pair must point at the
// same commit, but %D output already restricts tokens to one commit, so
// name matching alone is sufficient.
func MergeLocalRemotePairs(refs []DecoratedRef) []ChipRef {
	if len(refs) == 0 {
		return nil
	}
	// Pairing only matters when both kinds coexist — most rows are 1 ref tip,
	// so skip the map allocations on the common path.
	hasLocal, hasRemote := false, false
	for _, r := range refs {
		switch r.Kind {
		case RefKindLocal:
			hasLocal = true
		case RefKindRemote:
			hasRemote = true
		}
	}
	if !hasLocal || !hasRemote {
		out := make([]ChipRef, len(refs))
		for i, r := range refs {
			out[i] = ChipRef{Kind: r.Kind, DisplayName: r.ShortName, IsHead: r.IsHead}
		}
		return out
	}

	localIdx := make(map[string]int, len(refs))
	for i, r := range refs {
		if r.Kind == RefKindLocal {
			localIdx[r.ShortName] = i
		}
	}
	pairedLocals := make(map[int]bool, len(refs))
	pairedRemotes := make(map[int]bool, len(refs))
	for i, r := range refs {
		if r.Kind != RefKindRemote {
			continue
		}
		stripped, ok := stripRemotePrefix(r.ShortName)
		if !ok {
			continue
		}
		if li, found := localIdx[stripped]; found {
			pairedLocals[li] = true
			pairedRemotes[i] = true
		}
	}
	out := make([]ChipRef, 0, len(refs))
	for i, r := range refs {
		if pairedRemotes[i] {
			continue
		}
		out = append(out, ChipRef{
			Kind:         r.Kind,
			DisplayName:  r.ShortName,
			IsHead:       r.IsHead,
			PairedRemote: pairedLocals[i],
		})
	}
	return out
}

func classifyRefName(name string) RefKind {
	if strings.HasPrefix(name, "stash@{") {
		return RefKindStash
	}
	if _, ok := stripRemotePrefix(name); ok {
		return RefKindRemote
	}
	return RefKindLocal
}

func stripRemotePrefix(name string) (string, bool) {
	for _, p := range commonRemotePrefixes {
		if strings.HasPrefix(name, p) {
			return strings.TrimPrefix(name, p), true
		}
	}
	return "", false
}

func isSymbolicRemoteHead(token string) bool {
	for _, p := range commonRemotePrefixes {
		if token == p+"HEAD" {
			return true
		}
	}
	return false
}
