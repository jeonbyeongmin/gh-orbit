package git

import "testing"

func TestParseDecorationEmpty(t *testing.T) {
	refs, det := ParseDecoration(nil)
	if refs != nil || det {
		t.Errorf("nil tokens: refs=%v det=%v, want (nil,false)", refs, det)
	}
	refs, det = ParseDecoration([]string{})
	if refs != nil || det {
		t.Errorf("empty tokens: refs=%v det=%v, want (nil,false)", refs, det)
	}
}

func TestParseDecorationHeadArrowAndPair(t *testing.T) {
	refs, det := ParseDecoration([]string{"HEAD -> main", "origin/main"})
	if det {
		t.Errorf("headDetached=true; HEAD -> main shouldn't mark detached")
	}
	if len(refs) != 2 {
		t.Fatalf("len(refs)=%d, want 2; refs=%+v", len(refs), refs)
	}
	if refs[0].Kind != RefKindLocal || refs[0].ShortName != "main" || !refs[0].IsHead {
		t.Errorf("refs[0] = %+v, want local main head", refs[0])
	}
	if refs[1].Kind != RefKindRemote || refs[1].ShortName != "origin/main" || refs[1].IsHead {
		t.Errorf("refs[1] = %+v, want remote origin/main", refs[1])
	}
}

func TestParseDecorationDetachedHead(t *testing.T) {
	refs, det := ParseDecoration([]string{"HEAD"})
	if !det {
		t.Errorf("headDetached=false, want true")
	}
	if len(refs) != 0 {
		t.Errorf("refs=%v, want empty", refs)
	}
}

func TestParseDecorationTagStripsPrefix(t *testing.T) {
	refs, _ := ParseDecoration([]string{"tag: v0.0.1"})
	if len(refs) != 1 {
		t.Fatalf("len=%d, want 1", len(refs))
	}
	if refs[0].Kind != RefKindTag || refs[0].ShortName != "v0.0.1" {
		t.Errorf("refs[0] = %+v, want tag v0.0.1", refs[0])
	}
}

func TestParseDecorationSlashLocalNotMisclassified(t *testing.T) {
	// "feat/foo" 는 commonRemotePrefixes 에 없는 prefix 이므로 local.
	refs, _ := ParseDecoration([]string{"feat/foo"})
	if len(refs) != 1 || refs[0].Kind != RefKindLocal || refs[0].ShortName != "feat/foo" {
		t.Errorf("refs = %+v, want local feat/foo", refs)
	}
}

func TestParseDecorationDropsSymbolicRemoteHead(t *testing.T) {
	refs, _ := ParseDecoration([]string{"HEAD -> develop", "origin/develop", "origin/HEAD"})
	for _, r := range refs {
		if r.ShortName == "origin/HEAD" {
			t.Fatalf("origin/HEAD should be dropped, got %+v", refs)
		}
	}
	if len(refs) != 2 {
		t.Errorf("len=%d, want 2 (HEAD->develop + origin/develop); got %+v", len(refs), refs)
	}
}

func TestMergeLocalRemotePairsCollapses(t *testing.T) {
	in := []DecoratedRef{
		{Kind: RefKindLocal, ShortName: "main", IsHead: true},
		{Kind: RefKindRemote, ShortName: "origin/main"},
	}
	chips := MergeLocalRemotePairs(in)
	if len(chips) != 1 {
		t.Fatalf("len=%d, want 1; chips=%+v", len(chips), chips)
	}
	c := chips[0]
	if c.Kind != RefKindLocal || c.DisplayName != "main" || !c.IsHead || !c.PairedRemote {
		t.Errorf("chip = %+v, want local main head paired", c)
	}
}

func TestMergeLocalRemotePairsKeepsUnpaired(t *testing.T) {
	in := []DecoratedRef{
		{Kind: RefKindLocal, ShortName: "main"},
		{Kind: RefKindRemote, ShortName: "origin/feature"},
		{Kind: RefKindTag, ShortName: "v0.0.1"},
	}
	chips := MergeLocalRemotePairs(in)
	if len(chips) != 3 {
		t.Fatalf("len=%d, want 3; chips=%+v", len(chips), chips)
	}
	for _, c := range chips {
		if c.PairedRemote {
			t.Errorf("unexpected pairing on %+v", c)
		}
	}
}

func TestMergeLocalRemotePairsPreservesOrder(t *testing.T) {
	in := []DecoratedRef{
		{Kind: RefKindLocal, ShortName: "main", IsHead: true},
		{Kind: RefKindTag, ShortName: "v0.0.1"},
		{Kind: RefKindRemote, ShortName: "origin/main"},
	}
	chips := MergeLocalRemotePairs(in)
	if len(chips) != 2 {
		t.Fatalf("len=%d, want 2; chips=%+v", len(chips), chips)
	}
	if chips[0].DisplayName != "main" || !chips[0].PairedRemote {
		t.Errorf("chips[0] = %+v, want main paired", chips[0])
	}
	if chips[1].DisplayName != "v0.0.1" || chips[1].Kind != RefKindTag {
		t.Errorf("chips[1] = %+v, want tag v0.0.1", chips[1])
	}
}

func TestMergeLocalRemotePairsUnknownRemoteStaysSeparate(t *testing.T) {
	// myremote/ 는 commonRemotePrefixes 에 없으므로 ParseDecoration 단계에서 local 로
	// 분류된다. 즉 여기 도달하는 입력 자체가 RefKindRemote 가 아니라는 점이 invariant —
	// 그래도 직접 RefKindRemote 로 만들어 넣었을 때 합쳐지지 않는지 확인.
	in := []DecoratedRef{
		{Kind: RefKindLocal, ShortName: "main"},
		{Kind: RefKindRemote, ShortName: "myremote/main"},
	}
	chips := MergeLocalRemotePairs(in)
	if len(chips) != 2 {
		t.Errorf("unknown remote should stay separate; got %+v", chips)
	}
}
