package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/spf13/cobra"
)

func f64(v float64) *float64 { return &v }

func TestRubricFromAPI(t *testing.T) {
	low := gql.ModelComplexityLow
	empty := json.RawMessage(`{}`)
	values := json.RawMessage(`{"policy":"be kind"}`)
	d := gql.EnterpriseRubric{
		Id:   "eurb_1",
		Name: "quality",
		Metrics: []gql.EnterpriseRubricMetricsRubricMetric{
			&gql.EnterpriseRubricMetricsRubricJudgeMetric{
				JudgeEntryID: "erum_1",
				JudgeMetric:  gql.EnterpriseRubricMetricsRubricJudgeMetricJudgeMetric{Id: "eumt_1", Name: "Helpfulness"},
				TrialCount:   3,
				Complexity:   &low,
				InputValues:  &values,
				JudgeWeight:  f64(2),
			},
			&gql.EnterpriseRubricMetricsRubricJudgeMetric{
				JudgeEntryID: "erum_2",
				JudgeMetric:  gql.EnterpriseRubricMetricsRubricJudgeMetricJudgeMetric{Id: "eumt_2", Name: "Tone"},
				TrialCount:   1,
				InputValues:  &empty,
			},
			&gql.EnterpriseRubricMetricsRubricComputedMetric{
				ComputedEntryID: "ersm_1",
				ComputedMetric:  gql.EnterpriseRubricMetricsRubricComputedMetricComputedMetric{Key: "latency_ms", Name: "Latency"},
				ScoreCeiling:    f64(5000),
				ComputedWeight:  f64(1),
			},
		},
	}
	r := rubricFromAPI(d)
	if len(r.Metrics) != 3 {
		t.Fatalf("got %d metrics", len(r.Metrics))
	}
	j := r.Metrics[0]
	if j.JudgeMetricID != "eumt_1" || j.MetricName != "Helpfulness" || *j.TrialCount != 3 || j.Complexity != "LOW" || string(j.InputValues) != string(values) || *j.Weight != 2 || j.entryID != "erum_1" {
		t.Errorf("judge metric: %+v", j)
	}
	if r.Metrics[1].InputValues != nil {
		t.Errorf("empty input values should read as none, got %s", r.Metrics[1].InputValues)
	}
	c := r.Metrics[2]
	if c.ComputedMetricKey != "latency_ms" || c.TrialCount != nil || *c.ScoreCeiling != 5000 || c.entryID != "ersm_1" {
		t.Errorf("computed metric: %+v", c)
	}

	// The view must round-trip through -f: server-set fields are accepted.
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back Rubric
	if err := cmdutil.DecodeSpec("rubric", data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Metrics[0].JudgeMetricID != "eumt_1" || back.Metrics[2].ComputedMetricKey != "latency_ms" {
		t.Errorf("round trip lost metrics: %+v", back.Metrics)
	}
}

func TestCheckRubricMetric(t *testing.T) {
	tests := []struct {
		name    string
		in      RubricMetric
		want    RubricMetric
		wantErr string
	}{
		{name: "neither", in: RubricMetric{}, wantErr: "needs judge_metric_id or computed_metric_key"},
		{name: "both", in: RubricMetric{JudgeMetricID: "a", ComputedMetricKey: "b"}, wantErr: "give one"},
		{name: "judge with computed field", in: RubricMetric{JudgeMetricID: "a", ScoreFloor: f64(0)}, wantErr: "only for computed"},
		{name: "computed with judge field", in: RubricMetric{ComputedMetricKey: "k", Complexity: "low"}, wantErr: "only for judge"},
		{name: "complexity upper-cased", in: RubricMetric{JudgeMetricID: "a", Complexity: "high"}, want: RubricMetric{JudgeMetricID: "a", Complexity: "HIGH", InputValues: json.RawMessage{}}},
		{name: "bad complexity", in: RubricMetric{JudgeMetricID: "a", Complexity: "huge"}, wantErr: "complexity must be one of"},
		{name: "input values as a string", in: RubricMetric{JudgeMetricID: "a", InputValues: json.RawMessage(`"{\"x\":1}"`)}, want: RubricMetric{JudgeMetricID: "a", InputValues: json.RawMessage(`{"x":1}`)}},
		{name: "empty input values", in: RubricMetric{JudgeMetricID: "a", InputValues: json.RawMessage(`{}`)}, want: RubricMetric{JudgeMetricID: "a", InputValues: json.RawMessage{}}},
		{name: "input values not an object", in: RubricMetric{JudgeMetricID: "a", InputValues: json.RawMessage(`[1]`)}, wantErr: "must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checkRubricMetric(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got error %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Complexity != tt.want.Complexity || !bytes.Equal(got.InputValues, tt.want.InputValues) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRubricUpdate(t *testing.T) {
	held := []RubricMetric{
		{JudgeMetricID: "eumt_1", Weight: f64(1), entryID: "erum_1"},
		{JudgeMetricID: "eumt_2", entryID: "erum_2"},
		{ComputedMetricKey: "latency_ms", entryID: "ersm_1"},
	}
	trials := 3
	want := []RubricMetric{
		{JudgeMetricID: "eumt_1", Weight: f64(2), TrialCount: &trials},
		{ComputedMetricKey: "latency_ms", LowerIsBetter: cmdutil.Ptr(true)},
		{ComputedMetricKey: "cost_usd", Weight: f64(1)},
	}
	u := rubricUpdate("quality", "", held, want)
	if *u.Name != "quality" || u.Description != nil {
		t.Errorf("name %v description %v", u.Name, u.Description)
	}
	if !reflect.DeepEqual(u.DeletedMetricIDs, []string{"erum_2"}) {
		t.Errorf("deleted %v", u.DeletedMetricIDs)
	}
	if len(u.UpdatedMetrics) != 2 || u.UpdatedMetrics[0].ID != "erum_1" || *u.UpdatedMetrics[0].Weight != 2 || *u.UpdatedMetrics[0].TrialCount != 3 {
		t.Errorf("updated %+v", u.UpdatedMetrics)
	}
	if u.UpdatedMetrics[1].ID != "ersm_1" || !*u.UpdatedMetrics[1].LowerIsBetter || u.UpdatedMetrics[1].TrialCount != nil {
		t.Errorf("updated computed %+v", u.UpdatedMetrics[1])
	}
	if len(u.NewMetrics) != 1 || *u.NewMetrics[0].ComputedMetricKey != "cost_usd" || u.NewMetrics[0].JudgeMetricID != nil {
		t.Errorf("new %+v", u.NewMetrics)
	}

	// Kind-specific fields of the other kind must be left out of the JSON.
	data, _ := json.Marshal(newRubricMetric(RubricMetric{JudgeMetricID: "eumt_1"}))
	if string(data) != `{"judgeMetricID":"eumt_1"}` {
		t.Errorf("new judge metric JSON %s", data)
	}
}

func TestEditRubricMetrics(t *testing.T) {
	held := []RubricMetric{
		{JudgeMetricID: "eumt_1", MetricName: "Helpfulness", Weight: f64(1), entryID: "erum_1"},
		{ComputedMetricKey: "latency_ms", MetricName: "Latency", entryID: "ersm_1"},
	}
	got, err := editRubricMetrics(held, []RubricMetric{{JudgeMetricID: "eumt_1", Weight: f64(3)}, {JudgeMetricID: "eumt_9"}}, []string{"latency"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || *got[0].Weight != 3 || got[0].entryID != "erum_1" || got[1].JudgeMetricID != "eumt_9" {
		t.Errorf("got %+v", got)
	}
	if held[0].Weight == got[0].Weight {
		t.Error("held was changed")
	}
	if _, err := editRubricMetrics(held, nil, []string{"nope"}); err == nil {
		t.Error("removing an unknown metric should fail")
	}
}

func TestParseMetricFlags(t *testing.T) {
	got, err := parseMetricFlags([]string{"Helpfulness=2", "Tone"}, []string{"latency_ms=0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].JudgeMetricID != "Helpfulness" || *got[0].Weight != 2 || got[1].Weight != nil || got[2].ComputedMetricKey != "latency_ms" || *got[2].Weight != 0.5 {
		t.Errorf("got %+v", got)
	}
	if _, err := parseMetricFlags([]string{"x=heavy"}, nil); err == nil {
		t.Error("a bad weight should fail")
	}
}

func TestJudgeMetricInput(t *testing.T) {
	m := JudgeMetric{
		Name: "polite", Category: "quality", Scope: "hop", Complexity: "low", Prompt: "Was it polite?",
		Examples:    []ScoreExample{{Rating: 5, Scenario: "s", Judgment: "j"}},
		InputFields: []InputField{{Name: "tone", Label: "Tone", Description: "d", Type: "string", Required: true}},
	}
	in, err := judgeMetricInput(m)
	if err != nil {
		t.Fatal(err)
	}
	if in.Category != gql.MetricCategoryQuality || in.Scope != gql.JudgeScopeHop || in.Complexity != gql.ModelComplexityLow || in.Description != nil {
		t.Errorf("got %+v", in)
	}
	if len(in.Examples) != 1 || in.InputFields[0].Type != gql.JudgeInputFieldTypeString {
		t.Errorf("got %+v", in)
	}
	in, err = judgeMetricInput(JudgeMetric{Name: "x", Category: "QUALITY", Scope: "REQUEST", Complexity: "LOW", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if in.Examples == nil || in.InputFields == nil {
		t.Error("examples and input fields must be empty lists, not null, so an update clears them")
	}
	if _, err := judgeMetricInput(JudgeMetric{Name: "x", Category: "fun", Scope: "REQUEST", Complexity: "LOW", Prompt: "p"}); err == nil {
		t.Error("a bad category should fail")
	}
}

func TestReportTable(t *testing.T) {
	one := 1
	r := Report{
		Metrics: []ReportMetric{
			{RubricMetricID: "j", Name: "Helpfulness", Type: "JUDGED"},
			{RubricMetricID: "c", Name: "Latency", Type: "COMPUTED", Unit: "MILLISECONDS"},
		},
		Versions: []ReportVersion{{
			MeshVersionID: "cmsv_1", Version: &one, Label: "stable",
			Summary: ReportAggregate{TotalRequests: 10, SuccessfulRequests: 8, Coverage: f64(0.8), RubricScore: f64(0.75), Metrics: []ReportMetricValue{
				{RubricMetricID: "j", Score: f64(0.9)},
				{RubricMetricID: "c", Score: f64(0.6), Value: f64(1200)},
			}},
		}},
	}
	headers, rows := reportTable(r, false)
	wantHeaders := []string{"version", "requests", "scored", "coverage", "score", "Helpfulness", "Latency"}
	if !reflect.DeepEqual(headers, wantHeaders) {
		t.Errorf("headers %v", headers)
	}
	want := []string{"v1 stable", "10", "8", "80%", "0.75", "0.9", "0.6 (1200 milliseconds)"}
	if len(rows) != 1 || !reflect.DeepEqual(rows[0], want) {
		t.Errorf("rows %v", rows)
	}
}

func TestReportRange(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	start, end, err := reportRange("7d", "", "", now)
	if err != nil || !end.Equal(now) || !start.Equal(now.Add(-7*24*time.Hour)) {
		t.Errorf("7d: %v %v %v", start, end, err)
	}
	start, _, err = reportRange("24h", "2026-10-01T00:00:00Z", "", now)
	if err != nil || start.Day() != 1 {
		t.Errorf("from: %v %v", start, err)
	}
	if _, _, err := reportRange("24h", "2026-10-20T00:00:00Z", "", now); err == nil {
		t.Error("a start after the end should fail")
	}
	if _, _, err := reportRange("soon", "", "", now); err == nil {
		t.Error("a bad --since should fail")
	}
}

// Every command must refuse a server that is not enterprise before it sends
// anything.
func TestEnterpriseGuard(t *testing.T) {
	t.Setenv("PARTITE_CONFIG_DIR", t.TempDir())
	t.Setenv("PARTITE_NO_KEYRING", "1")
	t.Setenv("PARTITE_CONTEXT", "local")
	t.Setenv("PARTITE_URL", "http://127.0.0.1:1")
	for _, args := range [][]string{
		{"eval", "judge-metric", "list"},
		{"eval", "judge-metric", "create", "x", "--prompt", "p", "--category", "QUALITY"},
		{"eval", "computed-metric", "get", "latency_ms"},
		{"eval", "rubric", "create", "r", "--judge-metric", "x"},
		{"eval", "monitor", "list", "--mesh", "m"},
		{"eval", "monitor", "enable", "m/r"},
		{"eval", "evaluation", "list"},
		{"eval", "trace-run", "list"},
		{"eval", "report", "m", "r"},
	} {
		app := cmdutil.NewApp()
		var out bytes.Buffer
		app.Out, app.Err = &out, &out
		// Not root.New: root imports this package.
		cmd := &cobra.Command{Use: "partite", SilenceErrors: true, SilenceUsage: true,
			PersistentPreRunE: func(*cobra.Command, []string) error { return app.Init() }}
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.AddCommand(New(app))
		cmd.SetArgs(args)
		err := cmd.Execute()
		var e *cmdutil.Error
		if !errors.As(err, &e) || e.Code != cmdutil.ExitUsage || !strings.Contains(e.Msg, "enterprise") {
			t.Errorf("%v: got %v", args, err)
		}
	}
}
