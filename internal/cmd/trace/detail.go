package trace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convui"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
)

// Trace is a trace as get prints it.
type Trace struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	CreatedAt      string    `json:"created_at"`
	Mesh           Mesh      `json:"mesh"`
	Usage          Usage     `json:"usage"`
	Requests       []Request `json:"requests"`
}

// Usage adds up model calls.
type Usage struct {
	ModelCalls       int     `json:"model_calls"`
	TokensIn         int     `json:"tokens_in"`
	TokensOut        int     `json:"tokens_out"`
	TokensCacheRead  int     `json:"tokens_cache_read"`
	TokensCacheWrite int     `json:"tokens_cache_write"`
	TokensReasoning  int     `json:"tokens_reasoning"`
	Cost             float64 `json:"cost"`
}

type Request struct {
	ID         string          `json:"id"`
	RequestID  string          `json:"request_id"`
	CreatedAt  string          `json:"created_at"`
	Status     string          `json:"status,omitempty"`
	StatusData json.RawMessage `json:"status_data,omitempty"`
	Usage      Usage           `json:"usage"`
	Hops       []Hop           `json:"hops"`
}

// Hop is one agent's turn in a request. Via says how it was reached: by an
// input of the mesh, or by a link from another agent.
type Hop struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id,omitempty"`
	Agent     string    `json:"agent"`
	Via       string    `json:"via"`
	LinkID    string    `json:"link_id"`
	ActionID  string    `json:"action_id"`
	CreatedAt string    `json:"created_at"`
	Messages  []Message `json:"messages"`
}

type Message struct {
	ID          string          `json:"id"`
	Role        string          `json:"role"`
	IsError     bool            `json:"is_error"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   string          `json:"created_at"`
	Request     json.RawMessage `json:"request,omitempty"`
	Response    json.RawMessage `json:"response,omitempty"`
	Prompt      json.RawMessage `json:"prompt,omitempty"`
	Text        []string        `json:"text,omitempty"`
	Thinking    []string        `json:"thinking,omitempty"`
	ToolUses    []ToolUse       `json:"tool_uses,omitempty"`
	ToolOutputs []ToolOutput    `json:"tool_outputs,omitempty"`
	Model       *ModelCall      `json:"model,omitempty"`
}

type ToolUse struct {
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input"`
}

type ToolOutput struct {
	CallID  string            `json:"call_id"`
	IsError bool              `json:"is_error"`
	Output  []json.RawMessage `json:"output"`
}

type ModelCall struct {
	ID               string   `json:"id"`
	Provider         string   `json:"provider"`
	Model            string   `json:"model,omitempty"`
	Start            string   `json:"start"`
	End              string   `json:"end"`
	TokensIn         *int     `json:"tokens_in,omitempty"`
	TokensOut        *int     `json:"tokens_out,omitempty"`
	TokensTotal      *int     `json:"tokens_total,omitempty"`
	TokensCacheRead  *int     `json:"tokens_cache_read,omitempty"`
	TokensCacheWrite *int     `json:"tokens_cache_write,omitempty"`
	TokensReasoning  *int     `json:"tokens_reasoning,omitempty"`
	Cost             *float64 `json:"cost,omitempty"`
}

func (u *Usage) add(m *ModelCall) {
	u.ModelCalls++
	u.TokensIn += cmdutil.Deref(m.TokensIn)
	u.TokensOut += cmdutil.Deref(m.TokensOut)
	u.TokensCacheRead += cmdutil.Deref(m.TokensCacheRead)
	u.TokensCacheWrite += cmdutil.Deref(m.TokensCacheWrite)
	u.TokensReasoning += cmdutil.Deref(m.TokensReasoning)
	u.Cost += cmdutil.Deref(m.Cost)
}

func fromAPI(t gql.RuntimeTraceDetail) Trace {
	snap := t.MeshSnapshot
	agents := map[string]string{}
	for _, a := range snap.Agents {
		agents[a.Id] = a.Name
	}
	type route struct{ agent, via string }
	routes := map[string]route{}
	for _, in := range snap.Inputs {
		routes[in.Id] = route{agents[in.AgentID], "input " + in.Name}
	}
	for _, l := range snap.Links {
		routes[l.Id] = route{agents[l.ToAgentID], fmt.Sprintf("link %s from %s", l.Name, agents[l.FromAgentID])}
	}

	out := Trace{
		ID:             t.Id,
		ConversationID: t.ConversationID,
		CreatedAt:      t.CreatedAt,
		Mesh:           Mesh{ID: snap.Id, Name: snap.Name, VersionID: snap.VersionID, VersionOrdinal: snap.VersionOrdinal, ModelSet: cmdutil.Deref(snap.ModelSetName)},
		Requests:       make([]Request, len(t.Requests)),
	}
	for i, r := range t.Requests {
		req := Request{ID: r.Id, RequestID: r.ConversationRequestID, CreatedAt: r.CreatedAt, Status: status(r.Status), StatusData: jsonOrNil(r.StatusData), Hops: make([]Hop, len(r.Hops))}
		for j, h := range r.Hops {
			// Hops record links as input:<id> or link:<id>, and the hop that
			// starts a request as start.
			_, ref, found := strings.Cut(h.LinkID, ":")
			if !found {
				ref = h.LinkID
			}
			rt, ok := routes[ref]
			switch {
			case h.LinkID == "start":
				rt = route{"request", ""}
			case !ok:
				rt = route{h.LinkID, ""}
			}
			hop := Hop{ID: h.Id, ParentID: cmdutil.Deref(h.ParentID), Agent: rt.agent, Via: rt.via, LinkID: h.LinkID, ActionID: h.ActionID, CreatedAt: h.CreatedAt, Messages: make([]Message, len(h.Messages))}
			for k, m := range h.Messages {
				msg := messageFromAPI(m)
				if msg.Model != nil {
					req.Usage.add(msg.Model)
					out.Usage.add(msg.Model)
				}
				hop.Messages[k] = msg
			}
			req.Hops[j] = hop
		}
		out.Requests[i] = req
	}
	return out
}

func messageFromAPI(m gql.RuntimeTraceMessage) Message {
	msg := Message{ID: m.Id, Role: strings.ToLower(string(m.Role)), IsError: m.IsError, Error: cmdutil.Deref(m.Error), CreatedAt: m.CreatedAt}
	if p := m.MessageParts; p != nil {
		msg.Request = jsonOrNil(p.ConversationRequest)
		msg.Response = jsonOrNil(p.ConversationResponse)
		msg.Prompt = jsonOrNil(p.UserPrompt)
		msg.Text = p.AgentText
		msg.Thinking = p.AgentThinking
		for _, u := range p.AgentToolUses {
			msg.ToolUses = append(msg.ToolUses, ToolUse{CallID: u.CallID, Name: u.Name, Input: u.Input})
		}
		for _, o := range p.UserToolOutputs {
			msg.ToolOutputs = append(msg.ToolOutputs, ToolOutput{CallID: o.CallID, IsError: o.IsError, Output: o.Output})
		}
	}
	if mt := m.ModelTrace; mt != nil {
		msg.Model = &ModelCall{
			ID: mt.Id, Provider: string(mt.ProviderType), Model: cmdutil.Deref(mt.ModelName), Start: mt.TimeStart, End: mt.TimeEnd,
			TokensIn: mt.TokensIn, TokensOut: mt.TokensOut, TokensTotal: mt.TokensTotal,
			TokensCacheRead: mt.TokensCacheRead, TokensCacheWrite: mt.TokensCacheWrite, TokensReasoning: mt.TokensReasoning,
			Cost: mt.CostTotal,
		}
	}
	return msg
}

// thousands writes n with commas: 12,345.
func thousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (u Usage) String() string {
	if u.ModelCalls == 0 {
		return "no model calls"
	}
	parts := []string{
		cmdutil.Plural(u.ModelCalls, "model call"),
		thousands(u.TokensIn) + " tokens in",
		thousands(u.TokensOut) + " out",
	}
	if u.TokensCacheRead > 0 {
		parts = append(parts, thousands(u.TokensCacheRead)+" cache read")
	}
	if u.TokensReasoning > 0 {
		parts = append(parts, thousands(u.TokensReasoning)+" reasoning")
	}
	if u.Cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", u.Cost))
	}
	return strings.Join(parts, " · ")
}

func (m *ModelCall) String() string {
	parts := []string{"model " + m.Model}
	if m.Model == "" {
		parts[0] = "model call (" + m.Provider + ")"
	}
	start, err1 := time.Parse(time.RFC3339Nano, m.Start)
	end, err2 := time.Parse(time.RFC3339Nano, m.End)
	if err1 == nil && err2 == nil {
		d := end.Sub(start)
		if d < time.Second {
			d = d.Round(time.Millisecond)
		} else {
			d = d.Round(100 * time.Millisecond)
		}
		parts = append(parts, d.String())
	}
	if m.TokensIn != nil {
		parts = append(parts, thousands(*m.TokensIn)+" in")
	}
	if m.TokensOut != nil {
		parts = append(parts, thousands(*m.TokensOut)+" out")
	}
	if m.TokensCacheRead != nil && *m.TokensCacheRead > 0 {
		parts = append(parts, thousands(*m.TokensCacheRead)+" cache read")
	}
	if m.Cost != nil {
		parts = append(parts, fmt.Sprintf("$%.4f", *m.Cost))
	}
	return strings.Join(parts, " · ")
}

// treeWriter writes lines under a prefix that shows how deep they are.
type treeWriter struct {
	w     io.Writer
	style convui.Style
	full  bool
}

// line writes label, which may have colors, and text, which is cut to one
// line unless full is set.
func (t treeWriter) line(depth int, label, text string) {
	prefix := t.style.Dim(strings.Repeat("│ ", depth))
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if !t.full {
		lines = []string{output.Truncate(strings.Join(lines, " ⏎ "), 160)}
	}
	fmt.Fprintln(t.w, prefix+label+lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintln(t.w, prefix+"  "+l)
	}
}

func compact(raw json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

func indented(raw json.RawMessage) string {
	var buf bytes.Buffer
	if json.Indent(&buf, raw, "", "  ") != nil {
		return string(raw)
	}
	return buf.String()
}

func writeTree(w io.Writer, style convui.Style, t Trace, full bool) {
	tw := treeWriter{w: w, style: style, full: full}
	fmt.Fprintf(w, "%s %s\n", style.Bold("Trace"), t.ID)
	fields := []output.Field{
		{Label: "Conversation", Value: t.ConversationID},
		{Label: "Mesh", Value: fmt.Sprintf("%s, version %d (%s)", t.Mesh.Name, t.Mesh.VersionOrdinal, t.Mesh.ID)},
		{Label: "Model set", Value: t.Mesh.ModelSet},
		{Label: "Created", Value: cmdutil.FormatTime(t.CreatedAt)},
		{Label: "Usage", Value: t.Usage.String()},
	}
	for _, f := range fields {
		if f.Value != "" {
			fmt.Fprintf(w, "  %-13s %s\n", f.Label+":", f.Value)
		}
	}
	for i, r := range t.Requests {
		fmt.Fprintln(w)
		head := fmt.Sprintf("Request %d  %s  %s  %s", i+1, r.RequestID, r.Status, cmdutil.FormatTime(r.CreatedAt))
		fmt.Fprintln(w, style.Bold(head))
		if r.Usage.ModelCalls > 0 {
			fmt.Fprintln(w, style.Dim("  "+r.Usage.String()))
		}
		if len(r.StatusData) > 0 {
			tw.line(1, "status: ", convui.ResponseText(r.StatusData))
		}
		children := map[string][]Hop{}
		var roots []Hop
		ids := map[string]bool{}
		for _, h := range r.Hops {
			ids[h.ID] = true
		}
		for _, h := range r.Hops {
			if h.ParentID == "" || !ids[h.ParentID] {
				roots = append(roots, h)
			} else {
				children[h.ParentID] = append(children[h.ParentID], h)
			}
		}
		var walk func(h Hop, depth int)
		walk = func(h Hop, depth int) {
			head := style.Bold("▸ " + h.Agent)
			if h.Via != "" {
				head += style.Dim("  (" + h.Via + ")")
			}
			tw.line(depth, head, "")
			writeMessages(tw, depth+1, h.Messages)
			for _, c := range children[h.ID] {
				walk(c, depth+1)
			}
		}
		for _, h := range roots {
			walk(h, 1)
		}
	}
}

func writeMessages(tw treeWriter, depth int, msgs []Message) {
	style := tw.style
	tools := map[string]string{}
	for _, m := range msgs {
		if m.Model != nil {
			tw.line(depth, style.Dim(m.Model.String()), "")
		}
		if len(m.Request) > 0 {
			tw.line(depth, style.Cyan("request  "), requestText(m.Request, tw.full))
		}
		if tw.full && len(m.Prompt) > 0 {
			tw.line(depth, style.Dim("prompt   "), indented(m.Prompt))
		}
		for _, o := range m.ToolOutputs {
			parts := make([]string, len(o.Output))
			for i, p := range o.Output {
				parts[i] = compact(p)
			}
			label := "← " + tools[o.CallID]
			if tools[o.CallID] == "" {
				label = "← " + o.CallID
			}
			text := strings.Join(parts, " ")
			if o.IsError {
				tw.line(depth, style.Red(label+" failed: "), text)
			} else {
				tw.line(depth, style.Dim(label+": "), text)
			}
		}
		for _, th := range m.Thinking {
			tw.line(depth, style.Dim("thinking: "), th)
		}
		for _, text := range m.Text {
			tw.line(depth, "", text)
		}
		for _, u := range m.ToolUses {
			tools[u.CallID] = u.Name
			tw.line(depth, style.Green("→ "+u.Name), "("+compact(u.Input)+")")
		}
		if len(m.Response) > 0 {
			tw.line(depth, style.Cyan("response "), requestText(m.Response, tw.full))
		}
		if m.IsError || m.Error != "" {
			tw.line(depth, style.Red("error: "), m.Error)
		}
	}
}

func requestText(raw json.RawMessage, full bool) string {
	if full {
		return indented(raw)
	}
	return compact(raw)
}
