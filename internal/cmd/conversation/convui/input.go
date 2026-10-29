package convui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

// PickIntent returns the intent with this name. With no name it returns
// the mesh's only intent, or, when text is true because the input is plain
// text, the only intent that takes text.
func PickIntent(intents []convapi.Intent, name string, text bool, intentsCmd string) (convapi.Intent, error) {
	names := make([]string, len(intents))
	var textIntents []convapi.Intent
	for i, in := range intents {
		if in.Name == name {
			return in, nil
		}
		names[i] = in.Name
		if _, ok := TextProperty(in.Schema); ok {
			textIntents = append(textIntents, in)
		}
	}
	hint := "See each intent and its input with: " + intentsCmd
	switch {
	case len(intents) == 0:
		return convapi.Intent{}, &cmdutil.Error{Code: cmdutil.ExitUsage, Msg: "this mesh has no intents, so it cannot be run", Hint: "Add an input to the mesh first."}
	case name != "":
		return convapi.Intent{}, &cmdutil.Error{Code: cmdutil.ExitNotFound, Msg: fmt.Sprintf("the mesh has no intent %q; it has %s", name, strings.Join(names, ", ")), Hint: hint}
	case len(intents) == 1:
		return intents[0], nil
	case text && len(textIntents) == 1:
		return textIntents[0], nil
	}
	return convapi.Intent{}, &cmdutil.Error{Code: cmdutil.ExitUsage, Msg: "the mesh has several intents, so pass --intent with one of: " + strings.Join(names, ", "), Hint: hint}
}

// BuildInput makes the input message for an intent. inputFlag is JSON or
// YAML for the whole message; text fills the intent's one text property.
// With neither, the input is empty, which suits intents that need nothing.
func BuildInput(intent convapi.Intent, text, inputFlag, intentsCmd string) (json.RawMessage, error) {
	if text != "" && inputFlag != "" {
		return nil, cmdutil.UsageError("pass the input as text or with --input, not both")
	}
	var msg map[string]any
	switch {
	case inputFlag != "":
		if err := json.Unmarshal([]byte(inputFlag), &msg); err != nil {
			if err := cmdutil.DecodeSpec("--input", []byte(inputFlag), &msg); err != nil {
				return nil, cmdutil.UsageError("--input must be a JSON or YAML object: %s", err)
			}
		}
		if msg == nil {
			return nil, cmdutil.UsageError("--input must be a JSON or YAML object")
		}
	case text != "":
		prop, ok := TextProperty(intent.Schema)
		if !ok {
			return nil, &cmdutil.Error{
				Code: cmdutil.ExitUsage,
				Msg:  fmt.Sprintf("intent %s takes structured input, so pass it with --input as JSON; it has these fields:\n%s", intent.Name, fieldList(intent.Schema)),
				Hint: "See the full schema with: " + intentsCmd + " -o json",
			}
		}
		msg = map[string]any{prop: text}
	default:
		msg = map[string]any{}
	}
	var missing []string
	for _, name := range RequiredNames(intent.Schema) {
		if _, ok := msg[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		how := "pass them with --input as JSON"
		if _, ok := TextProperty(intent.Schema); ok && inputFlag == "" {
			how = "pass the text as an argument"
		}
		return nil, &cmdutil.Error{
			Code: cmdutil.ExitUsage,
			Msg:  fmt.Sprintf("intent %s needs %s; %s. Its fields:\n%s", intent.Name, strings.Join(missing, ", "), how, fieldList(intent.Schema)),
			Hint: "See the full schema with: " + intentsCmd + " -o json",
		}
	}
	return json.Marshal(msg)
}

// fieldList lists a schema's properties, one per line.
func fieldList(raw json.RawMessage) string {
	s, err := ParseSchema(raw)
	if err != nil {
		return "  (unreadable schema)"
	}
	props := s.OrderedProperties(raw)
	if len(props) == 0 {
		return "  (none)"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, p := range props {
		req := ""
		if p.Required {
			req = "required"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", p.Name, p.Schema.Describe(), req, p.Schema.Description)
	}
	_ = tw.Flush()
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

// WriteIntents describes intents for people: each one's input fields and
// responses.
func WriteIntents(w io.Writer, style Style, intents []convapi.Intent) {
	if len(intents) == 0 {
		fmt.Fprintln(w, "No intents.")
		return
	}
	for i, in := range intents {
		if i > 0 {
			fmt.Fprintln(w)
		}
		head := style.Bold(in.Name)
		if in.Description != "" {
			head += "  " + in.Description
		}
		fmt.Fprintln(w, head)
		fmt.Fprintln(w, "  input:")
		for _, line := range strings.Split(fieldList(in.Schema), "\n") {
			fmt.Fprintln(w, "  "+line)
		}
		if prop, ok := TextProperty(in.Schema); ok {
			fmt.Fprintf(w, "  %s\n", style.Dim(fmt.Sprintf("partite run's text argument fills %s", prop)))
		}
		if len(in.Responses) > 0 {
			names := make([]string, len(in.Responses))
			for j, r := range in.Responses {
				names[j] = r.Name
			}
			fmt.Fprintf(w, "  responses: %s\n", strings.Join(names, ", "))
		}
	}
}
