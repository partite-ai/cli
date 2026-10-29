package modelset

import (
	"testing"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

func TestProfileRefTakesStringOrObject(t *testing.T) {
	var s Set
	spec := `
name: s
default_model_profile: my-anthropic/sonnet
rules:
  - complexity: high
    model_profile:
      name: opus
      provider_id: my-anthropic
`
	if err := cmdutil.DecodeSpec("spec", []byte(spec), &s); err != nil {
		t.Fatal(err)
	}
	if s.DefaultModelProfile != (ProfileRef{Name: "my-anthropic/sonnet"}) {
		t.Errorf("default = %+v", s.DefaultModelProfile)
	}
	if got := s.Rules[0].ModelProfile; got != (ProfileRef{Name: "opus", ProviderID: "my-anthropic"}) {
		t.Errorf("rule profile = %+v", got)
	}
	bad := "name: s\ndefault_model_profile:\n  nam: x\n"
	if err := cmdutil.DecodeSpec("spec", []byte(bad), &s); err == nil {
		t.Error("unknown field in a profile: want an error")
	}
}
