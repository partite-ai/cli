package trace

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convui"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
)

func loadTrace(t *testing.T) Trace {
	t.Helper()
	data, err := os.ReadFile("testdata/trace.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw gql.RuntimeTraceDetail
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return fromAPI(raw)
}

func TestFromAPI(t *testing.T) {
	tr := loadTrace(t)
	if tr.Usage.ModelCalls != 3 || tr.Usage.TokensIn != 30 || tr.Usage.TokensOut != 15 {
		t.Errorf("usage = %+v", tr.Usage)
	}
	if len(tr.Requests) != 1 || tr.Requests[0].Status != "complete" || tr.Requests[0].Usage.ModelCalls != 3 {
		t.Fatalf("requests = %+v", tr.Requests)
	}
	hops := tr.Requests[0].Hops
	if len(hops) != 2 || hops[0].Agent != "request" || hops[1].Agent != "helper" || hops[1].Via != "input chat" || hops[1].ParentID != hops[0].ID {
		t.Errorf("hops = %+v", hops)
	}
}

func TestWriteTree(t *testing.T) {
	var buf bytes.Buffer
	writeTree(&buf, convui.Style{}, loadTrace(t), false)
	got := buf.String()
	for _, want := range []string{
		"Trace ttrc_11M9eWLV8nJiQ7h3cS73rhZu\n",
		"  Usage:        3 model calls · 30 tokens in · 15 out\n",
		"│ ▸ request\n",
		`│ │ request  {"text":"colors fake-script:5ca1c5d447155619"}` + "\n",
		"│ │ ▸ helper  (input chat)\n",
		"│ │ │ model fake · 1ms · 10 in · 5 out\n",
		"│ │ │ thinking: Ask with pick_one.\n",
		`│ │ │ → pick_one({"options":["red","green","blue"],"question":"Which color do you like?"})` + "\n",
		`│ │ │ ← pick_one: {"selected":"green"}` + "\n",
		`│ │ │ → answer({"answer":"Done."})` + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tree has no %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "(prompt)") {
		t.Error("the prompt shows without --full")
	}
	buf.Reset()
	writeTree(&buf, convui.Style{}, loadTrace(t), true)
	if !strings.Contains(buf.String(), "(prompt)") {
		t.Error("the prompt is missing with --full")
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestParseTime(t *testing.T) {
	for _, s := range []string{"6h", "90m", "2d", "2026-10-01", "2026-10-01T10:00", "2026-10-01T10:00:00Z"} {
		if got, err := parseTime("--since", s); err != nil || got == "" {
			t.Errorf("parseTime(%q) = %q, %v", s, got, err)
		}
	}
	if _, err := parseTime("--since", "yesterday"); err == nil {
		t.Error("parseTime(yesterday) did not fail")
	}
}
