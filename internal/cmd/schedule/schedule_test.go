package schedule

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/spf13/cobra"
)

func TestFromAPIRoundTrip(t *testing.T) {
	label := "stable"
	d := gql.EnterpriseSchedule{
		Id: "esch_1", Name: "daily",
		Mesh:             gql.EnterpriseScheduleMesh{Id: "cmsh_1", Name: "reports"},
		MeshVersionLabel: &label,
		InputName:        "prompt",
		Input:            json.RawMessage(`{"message":"go"}`),
		ConversationTags: []*gql.Tag{{Name: "team", Value: "ops"}},
		CronExpression:   "0 9 * * 1-5",
		Enabled:          true,
		CreatedAt:        "2026-10-01T00:00:00Z",
	}
	s := fromAPI(d)
	if s.MeshID != "cmsh_1" || s.MeshName != "reports" || s.MeshVersionLabel != "stable" || !*s.Enabled || s.ConversationTags[0].Value != "ops" {
		t.Errorf("got %+v", s)
	}
	if target(s) != "reports@stable" {
		t.Errorf("target %q", target(s))
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Schedule
	if err := cmdutil.DecodeSpec("schedule", data, &back); err != nil {
		t.Fatal(err)
	}
	if string(back.Input) != `{"message":"go"}` || back.CronExpression != s.CronExpression {
		t.Errorf("round trip: %+v", back)
	}
}

// replaceAll must send null for the optional fields a spec leaves empty, so
// apply clears them, and leave enabled out when the spec does not set it.
func TestReplaceAll(t *testing.T) {
	in := replaceAll(Schedule{Name: "daily", MeshID: "cmsh_1", InputName: "prompt", Input: json.RawMessage(`{}`), CronExpression: "* * * * *"})
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"description", "meshVersionLabel", "routeID", "notificationChannelID"} {
		if v, ok := got[k]; !ok || v != nil {
			t.Errorf("%s: want null, got %v (present %v)", k, v, ok)
		}
	}
	if _, ok := got["enabled"]; ok {
		t.Error("enabled should be left out")
	}
	if got["name"] != "daily" || got["cronExpression"] != "* * * * *" {
		t.Errorf("got %s", data)
	}
	if tags, ok := got["conversationTags"].([]any); !ok || len(tags) != 0 {
		t.Errorf("conversationTags should be an empty list, got %v", got["conversationTags"])
	}
}

func TestCheckInput(t *testing.T) {
	if got, err := checkInput(nil); err != nil || string(got) != "{}" {
		t.Errorf("empty: %s %v", got, err)
	}
	if _, err := checkInput(json.RawMessage(`"hello"`)); err == nil {
		t.Error("a string should fail")
	}
	if _, err := checkInput(json.RawMessage(`{"a":1}`)); err != nil {
		t.Error(err)
	}
}

func TestEnterpriseGuard(t *testing.T) {
	t.Setenv("PARTITE_CONFIG_DIR", t.TempDir())
	t.Setenv("PARTITE_NO_KEYRING", "1")
	t.Setenv("PARTITE_CONTEXT", "local")
	t.Setenv("PARTITE_URL", "http://127.0.0.1:1")
	for _, args := range [][]string{
		{"schedule", "list"},
		{"schedule", "next", "* * * * *"},
		{"schedule", "create", "x", "--mesh", "m", "--input-name", "p", "--cron", "* * * * *"},
		{"schedule", "disable", "x"},
	} {
		app := cmdutil.NewApp()
		var out bytes.Buffer
		app.Out, app.Err = &out, &out
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
