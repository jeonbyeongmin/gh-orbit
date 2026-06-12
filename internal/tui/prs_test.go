package tui

import "testing"

func TestParsePRListEmpty(t *testing.T) {
	prs, err := parsePRList([]byte(`[]`))
	if err != nil {
		t.Fatalf("parsePRList: %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("want empty map, got %v", prs)
	}
}

func TestParsePRListRollup(t *testing.T) {
	data := []byte(`[
		{"number":1,"headRefName":"no-checks","statusCheckRollup":[]},
		{"number":2,"headRefName":"all-green","statusCheckRollup":[
			{"status":"COMPLETED","conclusion":"SUCCESS"},
			{"state":"SUCCESS"},
			{"status":"COMPLETED","conclusion":"SKIPPED"}
		]},
		{"number":3,"headRefName":"one-red","statusCheckRollup":[
			{"status":"COMPLETED","conclusion":"SUCCESS"},
			{"status":"COMPLETED","conclusion":"FAILURE"},
			{"status":"IN_PROGRESS","conclusion":""}
		]},
		{"number":4,"headRefName":"still-running","statusCheckRollup":[
			{"status":"COMPLETED","conclusion":"SUCCESS"},
			{"status":"QUEUED","conclusion":""},
			{"state":"PENDING"}
		]}
	]`)
	prs, err := parsePRList(data)
	if err != nil {
		t.Fatalf("parsePRList: %v", err)
	}
	want := map[string]prInfo{
		"no-checks":     {Number: 1, Checks: prChecksNone},
		"all-green":     {Number: 2, Checks: prChecksPassing},
		"one-red":       {Number: 3, Checks: prChecksFailing}, // fail dominates pending
		"still-running": {Number: 4, Checks: prChecksPending},
	}
	if len(prs) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(prs), len(want), prs)
	}
	for branch, w := range want {
		got, ok := prs[branch]
		if !ok {
			t.Errorf("missing branch %q", branch)
			continue
		}
		if got != w {
			t.Errorf("%s: got %+v, want %+v", branch, got, w)
		}
	}
}

func TestParsePRListBadJSON(t *testing.T) {
	if _, err := parsePRList([]byte(`{not json`)); err == nil {
		t.Fatal("want parse error, got nil")
	}
}

func TestPRBadge(t *testing.T) {
	cases := []struct {
		pr   prInfo
		want string
	}{
		{prInfo{Number: 7, Checks: prChecksNone}, "#7"},
		{prInfo{Number: 7, Checks: prChecksPassing}, "#7✓"},
		{prInfo{Number: 7, Checks: prChecksFailing}, "#7✗"},
		{prInfo{Number: 7, Checks: prChecksPending}, "#7○"},
	}
	for _, c := range cases {
		if got := prBadge(c.pr); got != c.want {
			t.Errorf("prBadge(%+v) = %q, want %q", c.pr, got, c.want)
		}
	}
}
