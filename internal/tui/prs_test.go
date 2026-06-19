package tui

import "testing"

func TestParsePRListEmpty(t *testing.T) {
	list, err := parsePRList([]byte(`[]`))
	if err != nil {
		t.Fatalf("parsePRList: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty list, got %v", list)
	}
}

func TestParsePRListRollup(t *testing.T) {
	data := []byte(`[
		{"number":1,"headRefName":"no-checks","title":"First","author":{"login":"alice"},"statusCheckRollup":[]},
		{"number":2,"headRefName":"all-green","title":"Second","author":{"login":"bob"},"statusCheckRollup":[
			{"status":"COMPLETED","conclusion":"SUCCESS"},
			{"state":"SUCCESS"},
			{"status":"COMPLETED","conclusion":"SKIPPED"}
		]},
		{"number":3,"headRefName":"one-red","title":"Third","author":{"login":"carol"},"statusCheckRollup":[
			{"status":"COMPLETED","conclusion":"SUCCESS"},
			{"status":"COMPLETED","conclusion":"FAILURE"},
			{"status":"IN_PROGRESS","conclusion":""}
		]},
		{"number":4,"headRefName":"still-running","title":"Fourth","author":{"login":"dave"},"statusCheckRollup":[
			{"status":"COMPLETED","conclusion":"SUCCESS"},
			{"status":"QUEUED","conclusion":""},
			{"state":"PENDING"}
		]}
	]`)
	list, err := parsePRList(data)
	if err != nil {
		t.Fatalf("parsePRList: %v", err)
	}
	// Order is gh's; the page renders in this order. fail dominates pending.
	type meta struct {
		number int
		head   string
		title  string
		author string
		checks prCheckState
	}
	want := []meta{
		{1, "no-checks", "First", "alice", prChecksNone},
		{2, "all-green", "Second", "bob", prChecksPassing},
		{3, "one-red", "Third", "carol", prChecksFailing},
		{4, "still-running", "Fourth", "dave", prChecksPending},
	}
	if len(list) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(list), len(want), list)
	}
	for i, w := range want {
		got := list[i]
		if got.Number != w.number || got.HeadRef != w.head || got.Title != w.title ||
			got.Author != w.author || got.Checks != w.checks {
			t.Errorf("entry %d: got %+v, want %+v", i, got, w)
		}
	}
}

// parsePRList preserves the per-check name, verdict, and URL (collapsed away
// before) and orders the rows failures-first so the checks modal lands a
// reviewer on the broken check. detailsUrl (CheckRun) and targetUrl
// (StatusContext) both feed prCheck.URL.
func TestParsePRListCheckRows(t *testing.T) {
	data := []byte(`[
		{"number":7,"headRefName":"feat","title":"T","author":{"login":"erin"},"statusCheckRollup":[
			{"status":"COMPLETED","conclusion":"SUCCESS","name":"unit","detailsUrl":"https://ci/unit"},
			{"context":"lint","state":"FAILURE","targetUrl":"https://ci/lint"},
			{"status":"IN_PROGRESS","conclusion":"","name":"build","detailsUrl":"https://ci/build"}
		]}
	]`)
	list, err := parsePRList(data)
	if err != nil {
		t.Fatalf("parsePRList: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d entries, want 1", len(list))
	}
	want := []prCheck{
		{Name: "lint", State: prChecksFailing, URL: "https://ci/lint"},
		{Name: "build", State: prChecksPending, URL: "https://ci/build"},
		{Name: "unit", State: prChecksPassing, URL: "https://ci/unit"},
	}
	got := list[0].CheckRows
	if len(got) != len(want) {
		t.Fatalf("CheckRows: got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("CheckRows[%d]: got %+v, want %+v", i, got[i], w)
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
