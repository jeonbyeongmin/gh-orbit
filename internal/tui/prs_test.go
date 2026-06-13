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
	// Order is gh's; the modal renders in this order. fail dominates pending.
	want := []prInfo{
		{Number: 1, HeadRef: "no-checks", Title: "First", Author: "alice", Checks: prChecksNone},
		{Number: 2, HeadRef: "all-green", Title: "Second", Author: "bob", Checks: prChecksPassing},
		{Number: 3, HeadRef: "one-red", Title: "Third", Author: "carol", Checks: prChecksFailing},
		{Number: 4, HeadRef: "still-running", Title: "Fourth", Author: "dave", Checks: prChecksPending},
	}
	if len(list) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(list), len(want), list)
	}
	for i, w := range want {
		if list[i] != w {
			t.Errorf("entry %d: got %+v, want %+v", i, list[i], w)
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
