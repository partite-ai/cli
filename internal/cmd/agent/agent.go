// Package agent manages agents and their versions.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/clienttaskdef"
	"github.com/partite-ai/partite-cli/internal/cmd/skill"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Agent is the view printed by get and read by -f. Every change to an agent
// makes a new version; the version_* fields describe the latest one.
//
// In a spec, the id of an MCP connection, particle, skill, client task definition or
// memory interaction profile can be its name instead.
type Agent struct {
	ID                          string          `json:"id,omitempty"`
	Name                        string          `json:"name"`
	Description                 string          `json:"description,omitempty"`
	Identity                    string          `json:"identity,omitempty"`
	TaskInstructions            string          `json:"task_instructions"`
	ModelComplexity             string          `json:"model_complexity"`
	InputMessageType            json.RawMessage `json:"input_message_type,omitempty"`
	SendMessagesToUser          *bool           `json:"send_messages_to_user,omitempty"`
	SendThinkingToUser          *bool           `json:"send_thinking_to_user,omitempty"`
	McpConnections              []ToolSource    `json:"mcp_connections,omitempty"`
	Particles                   []ToolSource    `json:"particles,omitempty"`
	Skills                      []Ref           `json:"skills,omitempty"`
	ClientTasks                 []Ref           `json:"client_tasks,omitempty"`
	MemoryInteractionProfileIDs []string        `json:"memory_interaction_profile_ids,omitempty"`
	Tags                        []cmdutil.Tag   `json:"tags,omitempty"`
	VersionID                   string          `json:"version_id,omitempty"`
	VersionOrdinal              int             `json:"version_ordinal,omitempty"`
	VersionLabel                string          `json:"version_label,omitempty"`
	VersionCreatedAt            string          `json:"version_created_at,omitempty"`
	CreatedAt                   string          `json:"created_at,omitempty"`
	UpdatedAt                   string          `json:"updated_at,omitempty"`

	// names maps the ids of referenced resources to their names, for tables.
	names map[string]string
}

// ToolSource is an MCP connection or particle whose tools the agent can call.
// filter_type is NONE (all tools, the default), LIST (only tool_names) or
// PATTERN (tools whose name matches the tool_name_pattern regex).
type ToolSource struct {
	ID              string   `json:"id"`
	FilterType      string   `json:"filter_type,omitempty"`
	ToolNames       []string `json:"tool_names,omitempty"`
	ToolNamePattern string   `json:"tool_name_pattern,omitempty"`
}

// Ref refers to a skill or client task definition. In a spec it can be written as
// {id: ...} or as a plain string.
type Ref struct {
	ID string `json:"id"`
}

func (r *Ref) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &r.ID)
	}
	type plain Ref
	return json.Unmarshal(data, (*plain)(r))
}

var complexities = []string{string(gql.ModelComplexityLow), string(gql.ModelComplexityMedium), string(gql.ModelComplexityHigh)}

func fromAPI(d gql.AgentDetails) Agent {
	v := d.LatestVersion
	a := Agent{
		ID:                 d.Id,
		Name:               d.Name,
		Description:        cmdutil.Deref(d.Description),
		Identity:           cmdutil.Deref(v.Identity),
		TaskInstructions:   v.TaskInstructions,
		ModelComplexity:    string(v.ModelComplexity),
		InputMessageType:   v.InputMessageType,
		SendMessagesToUser: cmdutil.Ptr(v.SendMessagesToUser),
		SendThinkingToUser: cmdutil.Ptr(v.SendThinkingToUser),
		Tags:               cmdutil.TagsFromAPI(v.Tags),
		VersionID:          v.Id,
		VersionOrdinal:     v.Ordinal,
		VersionLabel:       cmdutil.Deref(v.Label),
		VersionCreatedAt:   v.CreatedAt,
		CreatedAt:          d.CreatedAt,
		UpdatedAt:          d.UpdatedAt,
		names:              map[string]string{},
	}
	for _, m := range v.McpConnections {
		a.McpConnections = append(a.McpConnections, ToolSource{ID: m.McpConnection.Id, FilterType: string(m.FilterType), ToolNames: m.ToolNames, ToolNamePattern: cmdutil.Deref(m.ToolNamePattern)})
		a.names[m.McpConnection.Id] = m.McpConnection.Name
	}
	for _, p := range v.Particles {
		a.Particles = append(a.Particles, ToolSource{ID: p.InstalledParticle.Id, FilterType: string(p.FilterType), ToolNames: p.ToolNames, ToolNamePattern: cmdutil.Deref(p.ToolNamePattern)})
	}
	for _, s := range v.Skills {
		a.Skills = append(a.Skills, Ref{ID: s.Skill.Id})
		a.names[s.Skill.Id] = s.Skill.Name
	}
	for _, t := range v.ClientTasks {
		a.ClientTasks = append(a.ClientTasks, Ref{ID: t.ClientTaskDefinition.Id})
		a.names[t.ClientTaskDefinition.Id] = t.ClientTaskDefinition.Name
	}
	for _, m := range v.MemoryInteractionProfiles {
		a.MemoryInteractionProfileIDs = append(a.MemoryInteractionProfileIDs, m.Id)
		a.names[m.Id] = m.Name
	}
	return a
}

// nameList prints references by name where the name is known.
func (a Agent) nameList(ids []string) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id
		if n := a.names[id]; n != "" {
			out[i] = n
		}
	}
	return strings.Join(out, ", ")
}

func toolSourceIDs(ts []ToolSource) []string {
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	return ids
}

func refIDs(rs []Ref) []string {
	ids := make([]string, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	return ids
}

var Resource = &cmdutil.Resource[Agent]{
	WebList:  "agents",
	WebPage:  "agents/%s",
	WebLink:  "agent",
	Kind:     "agent",
	Command:  "agent",
	IDPrefix: "cagt",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Agent, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Agent], error) {
			resp, err := gql.AgentList(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Agent]{}, err
			}
			conn := resp.Agents
			items := make([]Agent, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[Agent]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Agent, error) {
		c, err := app.GQL()
		if err != nil {
			return Agent{}, err
		}
		resp, err := gql.GetAgent(ctx, c, id)
		if err != nil {
			return Agent{}, err
		}
		if resp.Agent == nil {
			return Agent{}, cmdutil.NotFound("agent", id, "partite agent list")
		}
		return fromAPI(*resp.Agent), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteAgent(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(a Agent) string { return a.ID },
	Name:    func(a Agent) string { return a.Name },
	Columns: []string{"id", "name", "version", "complexity", "description", "updated"},
	Row: func(a Agent) []string {
		return []string{a.ID, a.Name, strconv.Itoa(a.VersionOrdinal), a.ModelComplexity, a.Description, cmdutil.FormatTime(a.UpdatedAt)}
	},
	Fields: func(a Agent) []output.Field {
		var inputType bytes.Buffer
		if json.Indent(&inputType, a.InputMessageType, "", "  ") != nil {
			inputType.Write(a.InputMessageType)
		}
		version := strconv.Itoa(a.VersionOrdinal)
		if a.VersionLabel != "" {
			version += " (" + a.VersionLabel + ")"
		}
		return []output.Field{
			{Label: "ID", Value: a.ID},
			{Label: "Name", Value: a.Name},
			{Label: "Description", Value: a.Description},
			{Label: "Version", Value: version},
			{Label: "Version id", Value: a.VersionID},
			{Label: "Identity", Value: a.Identity},
			{Label: "Model complexity", Value: a.ModelComplexity},
			{Label: "Input message type", Value: inputType.String()},
			{Label: "Send messages to user", Value: strconv.FormatBool(cmdutil.Deref(a.SendMessagesToUser))},
			{Label: "Send thinking to user", Value: strconv.FormatBool(cmdutil.Deref(a.SendThinkingToUser))},
			{Label: "MCP connections", Value: a.nameList(toolSourceIDs(a.McpConnections))},
			{Label: "Particles", Value: a.nameList(toolSourceIDs(a.Particles))},
			{Label: "Skills", Value: a.nameList(refIDs(a.Skills))},
			{Label: "Client tasks", Value: a.nameList(refIDs(a.ClientTasks))},
			{Label: "Memory profiles", Value: a.nameList(a.MemoryInteractionProfileIDs)},
			{Label: "Tags", Value: cmdutil.FormatTags(a.Tags)},
			{Label: "Task instructions", Value: a.TaskInstructions},
			{Label: "Created", Value: a.CreatedAt},
			{Label: "Updated", Value: a.UpdatedAt},
		}
	},
}

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent",
		Aliases: []string{"agents"},
		Short:   "Manage agents",
		Long: `An agent is a model with instructions and the tools it may use: MCP
connections, particles, skills and client task definitions. Agents run inside meshes.

Every change to an agent makes a new version. List them with
partite agent versions AGENT, and label one with partite agent label-version.

Model complexities: ` + strings.Join(complexities, ", "),
	}
	cmd.AddCommand(
		Resource.ListCmd(app),
		Resource.GetCmd(app),
		Resource.ViewCmd(app),
		newCreate(app),
		newUpdate(app),
		Resource.ApplyCmd(app, "  partite agent apply -f support-agent.yaml", func(ctx context.Context, spec Agent, id string) (Agent, error) {
			if id == "" {
				return create(ctx, app, spec)
			}
			return replace(ctx, app, id, spec)
		}),
		Resource.DeleteCmd(app),
		newVersions(app),
		newLabelVersion(app),
		newCopy(app),
	)
	return cmd
}

func resolveMcpConnection(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	return cmdutil.Resolve(ctx, ref, "cmco", "MCP connection", "mcp", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		resp, err := gql.AgentFindMcpConnections(ctx, c, name)
		if err != nil {
			return nil, err
		}
		var out []cmdutil.Named
		for _, e := range resp.McpConnections.Edges {
			out = append(out, cmdutil.Named{ID: e.Node.Id, Name: e.Node.Name})
		}
		return out, nil
	})
}

func resolveParticle(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	return cmdutil.Resolve(ctx, ref, "cipa", "particle", "particle", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		resp, err := gql.AgentFindParticles(ctx, c, name)
		if err != nil {
			return nil, err
		}
		var out []cmdutil.Named
		for _, e := range resp.InstalledParticles.Edges {
			out = append(out, cmdutil.Named{ID: e.Node.Id, Name: e.Node.Name})
		}
		return out, nil
	})
}

func resolveMemoryProfile(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	return cmdutil.Resolve(ctx, ref, "cmip", "memory interaction profile", "memory profile", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		resp, err := gql.AgentFindMemoryInteractionProfiles(ctx, c, name)
		if err != nil {
			return nil, err
		}
		var out []cmdutil.Named
		for _, e := range resp.MemoryInteractionProfiles.Edges {
			out = append(out, cmdutil.Named{ID: e.Node.Id, Name: e.Node.Name})
		}
		return out, nil
	})
}

func filterType(s string) (gql.ToolFilterType, error) {
	switch t := gql.ToolFilterType(strings.ToUpper(s)); t {
	case "":
		return gql.ToolFilterTypeNone, nil
	case gql.ToolFilterTypeNone, gql.ToolFilterTypeList, gql.ToolFilterTypePattern:
		return t, nil
	}
	return "", cmdutil.UsageError("filter_type must be NONE, LIST or PATTERN, got %q", s)
}

func modelComplexity(s string) (gql.ModelComplexity, error) {
	c := strings.ToUpper(s)
	for _, k := range complexities {
		if c == k {
			return gql.ModelComplexity(c), nil
		}
	}
	return "", cmdutil.UsageError("model complexity must be one of %s, got %q", strings.Join(complexities, ", "), s)
}

func mcpConnections(ctx context.Context, app *cmdutil.App, ts []ToolSource) ([]gql.NewMcpConnectionAgentAssociation, error) {
	out := make([]gql.NewMcpConnectionAgentAssociation, len(ts))
	for i, t := range ts {
		id, err := resolveMcpConnection(ctx, app, t.ID)
		if err != nil {
			return nil, err
		}
		ft, err := filterType(t.FilterType)
		if err != nil {
			return nil, err
		}
		out[i] = gql.NewMcpConnectionAgentAssociation{McpConnectionID: id, FilterType: ft, ToolNames: t.ToolNames, ToolNamePattern: cmdutil.OptString(t.ToolNamePattern)}
		if out[i].ToolNames == nil {
			out[i].ToolNames = []string{}
		}
	}
	return out, nil
}

func particles(ctx context.Context, app *cmdutil.App, ts []ToolSource) ([]gql.NewParticleAgentAssociation, error) {
	out := make([]gql.NewParticleAgentAssociation, len(ts))
	for i, t := range ts {
		id, err := resolveParticle(ctx, app, t.ID)
		if err != nil {
			return nil, err
		}
		ft, err := filterType(t.FilterType)
		if err != nil {
			return nil, err
		}
		out[i] = gql.NewParticleAgentAssociation{InstalledParticleID: id, FilterType: ft, ToolNames: t.ToolNames, ToolNamePattern: cmdutil.OptString(t.ToolNamePattern)}
		if out[i].ToolNames == nil {
			out[i].ToolNames = []string{}
		}
	}
	return out, nil
}

func skills(ctx context.Context, app *cmdutil.App, refs []Ref) ([]gql.NewSkillAgentAssociation, error) {
	out := make([]gql.NewSkillAgentAssociation, len(refs))
	for i, r := range refs {
		id, err := skill.Resource.Resolve(ctx, app, r.ID)
		if err != nil {
			return nil, err
		}
		out[i] = gql.NewSkillAgentAssociation{SkillID: id}
	}
	return out, nil
}

func clientTasks(ctx context.Context, app *cmdutil.App, refs []Ref) ([]gql.NewClientTaskAgentAssociation, error) {
	out := make([]gql.NewClientTaskAgentAssociation, len(refs))
	for i, r := range refs {
		id, err := clienttaskdef.Resource.Resolve(ctx, app, r.ID)
		if err != nil {
			return nil, err
		}
		out[i] = gql.NewClientTaskAgentAssociation{ClientTaskDefinitionID: id}
	}
	return out, nil
}

func memoryProfiles(ctx context.Context, app *cmdutil.App, refs []string) ([]gql.NewMemoryInteractionProfileAgentAssociation, error) {
	out := make([]gql.NewMemoryInteractionProfileAgentAssociation, len(refs))
	for i, r := range refs {
		id, err := resolveMemoryProfile(ctx, app, r)
		if err != nil {
			return nil, err
		}
		out[i] = gql.NewMemoryInteractionProfileAgentAssociation{MemoryInteractionProfileID: id}
	}
	return out, nil
}

// fullUpdate is every field of an agent in API form, with references resolved.
type fullUpdate struct {
	complexity  gql.ModelComplexity
	inputType   string
	mcp         []gql.NewMcpConnectionAgentAssociation
	particles   []gql.NewParticleAgentAssociation
	skills      []gql.NewSkillAgentAssociation
	clientTasks []gql.NewClientTaskAgentAssociation
	memory      []gql.NewMemoryInteractionProfileAgentAssociation
}

func resolveSpec(ctx context.Context, app *cmdutil.App, spec Agent) (fullUpdate, error) {
	var u fullUpdate
	var err error
	if spec.TaskInstructions == "" {
		return u, cmdutil.UsageError("task_instructions is required")
	}
	if spec.ModelComplexity == "" {
		return u, cmdutil.UsageError("model_complexity is required; use one of %s", strings.Join(complexities, ", "))
	}
	if u.complexity, err = modelComplexity(spec.ModelComplexity); err != nil {
		return u, err
	}
	if u.inputType, err = clienttaskdef.JSONText("input_message_type", spec.InputMessageType); err != nil {
		return u, err
	}
	if u.inputType == "" {
		return u, cmdutil.UsageError("input_message_type is required; it is a JSON schema of type object")
	}
	if u.mcp, err = mcpConnections(ctx, app, spec.McpConnections); err != nil {
		return u, err
	}
	if u.particles, err = particles(ctx, app, spec.Particles); err != nil {
		return u, err
	}
	if u.skills, err = skills(ctx, app, spec.Skills); err != nil {
		return u, err
	}
	if u.clientTasks, err = clientTasks(ctx, app, spec.ClientTasks); err != nil {
		return u, err
	}
	if u.memory, err = memoryProfiles(ctx, app, spec.MemoryInteractionProfileIDs); err != nil {
		return u, err
	}
	return u, nil
}

func create(ctx context.Context, app *cmdutil.App, spec Agent) (Agent, error) {
	u, err := resolveSpec(ctx, app, spec)
	if err != nil {
		return Agent{}, err
	}
	in := &gql.NewAgent{
		Name:                      spec.Name,
		Description:               gql.OptPtr(cmdutil.OptString(spec.Description)),
		Tags:                      cmdutil.TagsToAPI(spec.Tags),
		Identity:                  gql.OptPtr(cmdutil.OptString(spec.Identity)),
		TaskInstructions:          spec.TaskInstructions,
		ModelComplexity:           u.complexity,
		InputMessageType:          u.inputType,
		SendThinkingToUser:        gql.OptPtr(spec.SendThinkingToUser),
		SendMessagesToUser:        gql.OptPtr(spec.SendMessagesToUser),
		McpConnections:            u.mcp,
		Particles:                 u.particles,
		MemoryInteractionProfiles: u.memory,
		ClientTasks:               u.clientTasks,
		Skills:                    u.skills,
	}
	c, err := app.GQL()
	if err != nil {
		return Agent{}, err
	}
	resp, err := gql.CreateAgent(ctx, c, in)
	if err != nil {
		return Agent{}, err
	}
	return fromAPI(resp.CreateAgent.Agent), nil
}

// replace sets every field of the agent to the spec's. Lists left out of the
// spec become empty; send_*_to_user left out stay as they are.
func replace(ctx context.Context, app *cmdutil.App, id string, spec Agent) (Agent, error) {
	u, err := resolveSpec(ctx, app, spec)
	if err != nil {
		return Agent{}, err
	}
	in := &gql.UpdateAgent{
		Name:                      gql.Opt(spec.Name),
		Description:               gql.Opt(spec.Description),
		Tags:                      gql.Opt(cmdutil.TagsToAPI(spec.Tags)),
		Identity:                  gql.Opt(spec.Identity),
		TaskInstructions:          gql.Opt(spec.TaskInstructions),
		ModelComplexity:           gql.Opt(u.complexity),
		InputMessageType:          gql.Opt(u.inputType),
		SendThinkingToUser:        gql.OptPtr(spec.SendThinkingToUser),
		SendMessagesToUser:        gql.OptPtr(spec.SendMessagesToUser),
		McpConnections:            gql.Opt(u.mcp),
		Particles:                 gql.Opt(u.particles),
		MemoryInteractionProfiles: gql.Opt(u.memory),
		ClientTasks:               gql.Opt(u.clientTasks),
		Skills:                    gql.Opt(u.skills),
	}
	return update(ctx, app, id, in)
}

func update(ctx context.Context, app *cmdutil.App, id string, in *gql.UpdateAgent) (Agent, error) {
	c, err := app.GQL()
	if err != nil {
		return Agent{}, err
	}
	resp, err := gql.ModifyAgent(ctx, c, id, in)
	if err != nil {
		return Agent{}, err
	}
	return fromAPI(resp.UpdateAgent.Agent), nil
}

// agentFlags are the flags shared by create and update.
type agentFlags struct {
	description, identity, instructions, complexity, inputType string
	sendMessages, sendThinking                                 bool
	mcp, particles, skills, clientTasks, memory, tags          []string
}

func (f *agentFlags) register(cmd *cobra.Command, listVerb string) {
	fs := cmd.Flags()
	fs.StringVar(&f.description, "description", "", "description")
	fs.StringVar(&f.identity, "identity", "", "who the agent is, such as its role or persona")
	fs.StringVar(&f.instructions, "task-instructions", "", "instructions that guide the agent, or @file")
	fs.StringVar(&f.complexity, "model-complexity", "", "how capable a model the agent needs: "+strings.Join(complexities, ", "))
	fs.StringVar(&f.inputType, "input-message-type", "", "JSON schema (type object) of the messages the agent takes, or @file")
	fs.BoolVar(&f.sendMessages, "send-messages-to-user", false, "send the agent's messages to the user")
	fs.BoolVar(&f.sendThinking, "send-thinking-to-user", false, "send the agent's thinking to the user")
	fs.StringSliceVar(&f.mcp, "mcp-connection", nil, "MCP connection whose tools the agent can use, by name or id; "+listVerb)
	fs.StringSliceVar(&f.particles, "particle", nil, "installed particle whose tools the agent can use, by name or id; "+listVerb)
	fs.StringSliceVar(&f.skills, "skill", nil, "skill the agent can load, by name or id; "+listVerb)
	fs.StringSliceVar(&f.clientTasks, "client-task", nil, "client task definition the agent can send, by name or id; "+listVerb)
	fs.StringSliceVar(&f.memory, "memory-interaction-profile", nil, "memory interaction profile, by name or id; "+listVerb)
	fs.StringSliceVar(&f.tags, "tag", nil, "tag as name=value; "+listVerb)
	_ = cmd.RegisterFlagCompletionFunc("model-complexity", cobra.FixedCompletions(complexities, cobra.ShellCompDirectiveNoFileComp))
}

// nonEmpty drops empty values, so that --skill= gives an empty list.
func nonEmpty(ss []string) []string {
	out := []string{}
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func toolSources(refs []string) []ToolSource {
	out := make([]ToolSource, len(refs))
	for i, r := range refs {
		out[i] = ToolSource{ID: r}
	}
	return out
}

func refs(ss []string) []Ref {
	out := make([]Ref, len(ss))
	for i, s := range ss {
		out[i] = Ref{ID: s}
	}
	return out
}

// apply copies the flags that were passed onto spec.
func (f *agentFlags) apply(app *cmdutil.App, cmd *cobra.Command, spec *Agent) error {
	fs := cmd.Flags()
	if fs.Changed("description") {
		spec.Description = f.description
	}
	if fs.Changed("identity") {
		spec.Identity = f.identity
	}
	if fs.Changed("task-instructions") {
		text, err := app.ReadValue(f.instructions)
		if err != nil {
			return err
		}
		spec.TaskInstructions = text
	}
	if fs.Changed("model-complexity") {
		spec.ModelComplexity = f.complexity
	}
	if fs.Changed("input-message-type") {
		text, err := app.ReadValue(f.inputType)
		if err != nil {
			return err
		}
		if !json.Valid([]byte(text)) {
			return cmdutil.UsageError("--input-message-type is not valid JSON")
		}
		spec.InputMessageType = json.RawMessage(text)
	}
	if fs.Changed("send-messages-to-user") {
		spec.SendMessagesToUser = cmdutil.Ptr(f.sendMessages)
	}
	if fs.Changed("send-thinking-to-user") {
		spec.SendThinkingToUser = cmdutil.Ptr(f.sendThinking)
	}
	if fs.Changed("mcp-connection") {
		spec.McpConnections = toolSources(nonEmpty(f.mcp))
	}
	if fs.Changed("particle") {
		spec.Particles = toolSources(nonEmpty(f.particles))
	}
	if fs.Changed("skill") {
		spec.Skills = refs(nonEmpty(f.skills))
	}
	if fs.Changed("client-task") {
		spec.ClientTasks = refs(nonEmpty(f.clientTasks))
	}
	if fs.Changed("memory-interaction-profile") {
		spec.MemoryInteractionProfileIDs = nonEmpty(f.memory)
	}
	if fs.Changed("tag") {
		tags, err := cmdutil.ParseTags(nonEmpty(f.tags))
		if err != nil {
			return err
		}
		spec.Tags = tags
	}
	return nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var flags agentFlags
	var file string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an agent",
		Long: `Create an agent. --task-instructions, --model-complexity and
--input-message-type are required.
Simple settings are flags. Tool filters for MCP connections and particles need
a spec file: write one by hand, or start from partite agent get AGENT -o yaml.

Model complexities: ` + strings.Join(complexities, ", "),
		Example: `  partite agent create support --model-complexity MEDIUM \
    --task-instructions @support.md --input-message-type @input-schema.json \
    --mcp-connection zendesk --skill refunds
  partite agent create -f support-agent.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var spec Agent
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			if err := flags.apply(app, cmd, &spec); err != nil {
				return err
			}
			if spec.TaskInstructions == "" {
				return cmdutil.UsageError("--task-instructions is required")
			}
			if spec.ModelComplexity == "" {
				return cmdutil.UsageError("--model-complexity is required; use one of %s", strings.Join(complexities, ", "))
			}
			if len(spec.InputMessageType) == 0 {
				return cmdutil.UsageError("--input-message-type is required; it is a JSON schema of type object")
			}
			a, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created agent %s (%s).", a.Name, a.ID)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, a)
		},
	}
	flags.register(cmd, "repeat for more")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the agent from a YAML or JSON file; flags override it")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var flags agentFlags
	var name string
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change an agent, making a new version",
		Long: `Change an agent. Only the flags you pass are changed. Each list flag, such as
--skill, replaces the whole list; pass it empty (--skill=) to remove all.
Replacing --mcp-connection or --particle drops their tool filters; use
partite agent apply -f to keep filters.`,
		Example: `  partite agent update support --task-instructions @support.md
  partite agent update support --skill refunds --skill returns
  partite agent update support --client-task=`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := false
			cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) { changed = changed || f.Changed })
			if !changed {
				return cmdutil.UsageError("nothing to change; see --help for the flags")
			}
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			var spec Agent
			if err := flags.apply(app, cmd, &spec); err != nil {
				return err
			}
			fs := cmd.Flags()
			in := &gql.UpdateAgent{}
			if fs.Changed("name") {
				in.Name = gql.Opt(name)
			}
			if fs.Changed("description") {
				in.Description = gql.Opt(spec.Description)
			}
			if fs.Changed("identity") {
				in.Identity = gql.Opt(spec.Identity)
			}
			if fs.Changed("task-instructions") {
				in.TaskInstructions = gql.Opt(spec.TaskInstructions)
			}
			if fs.Changed("model-complexity") {
				c, err := modelComplexity(spec.ModelComplexity)
				if err != nil {
					return err
				}
				in.ModelComplexity = gql.Opt(c)
			}
			if fs.Changed("input-message-type") {
				in.InputMessageType = gql.Opt(string(spec.InputMessageType))
			}
			in.SendMessagesToUser = gql.OptPtr(spec.SendMessagesToUser)
			in.SendThinkingToUser = gql.OptPtr(spec.SendThinkingToUser)
			if fs.Changed("mcp-connection") {
				v, err := mcpConnections(ctx, app, spec.McpConnections)
				if err != nil {
					return err
				}
				in.McpConnections = gql.Opt(v)
			}
			if fs.Changed("particle") {
				v, err := particles(ctx, app, spec.Particles)
				if err != nil {
					return err
				}
				in.Particles = gql.Opt(v)
			}
			if fs.Changed("skill") {
				v, err := skills(ctx, app, spec.Skills)
				if err != nil {
					return err
				}
				in.Skills = gql.Opt(v)
			}
			if fs.Changed("client-task") {
				v, err := clientTasks(ctx, app, spec.ClientTasks)
				if err != nil {
					return err
				}
				in.ClientTasks = gql.Opt(v)
			}
			if fs.Changed("memory-interaction-profile") {
				v, err := memoryProfiles(ctx, app, spec.MemoryInteractionProfileIDs)
				if err != nil {
					return err
				}
				in.MemoryInteractionProfiles = gql.Opt(v)
			}
			if fs.Changed("tag") {
				in.Tags = gql.Opt(cmdutil.TagsToAPI(spec.Tags))
			}
			a, err := update(ctx, app, id, in)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated agent %s; it is now at version %d.", a.Name, a.VersionOrdinal)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, a)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	flags.register(cmd, "replaces the list; repeat for more")
	return cmd
}

// Version is one version of an agent.
type Version struct {
	ID              string `json:"id"`
	Ordinal         int    `json:"ordinal"`
	Label           string `json:"label,omitempty"`
	ModelComplexity string `json:"model_complexity"`
	CreatedAt       string `json:"created_at"`
}

func versionFromAPI(v gql.AgentVersionSummary) Version {
	return Version{ID: v.Id, Ordinal: v.Ordinal, Label: cmdutil.Deref(v.Label), ModelComplexity: string(v.ModelComplexity), CreatedAt: v.CreatedAt}
}

func listVersions(ctx context.Context, app *cmdutil.App, agentID string, limit int) ([]Version, error) {
	c, err := app.GQL()
	if err != nil {
		return nil, err
	}
	return cmdutil.ListAll(ctx, limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Version], error) {
		resp, err := gql.AgentListVersions(ctx, c, agentID, first, after)
		if err != nil {
			return cmdutil.Page[Version]{}, err
		}
		if resp.Agent == nil {
			return cmdutil.Page[Version]{}, cmdutil.NotFound("agent", agentID, "partite agent list")
		}
		conn := resp.Agent.Versions
		items := make([]Version, len(conn.Edges))
		for i, e := range conn.Edges {
			items[i] = versionFromAPI(e.Node)
		}
		return cmdutil.Page[Version]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
	})
}

func newVersions(app *cmdutil.App) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "versions <name-or-id>",
		Short:   "List an agent's versions, newest first",
		Example: "  partite agent versions support\n  partite agent versions support --limit 5 -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := Resource.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			versions, err := listVersions(cmd.Context(), app, id, limit)
			if err != nil {
				return err
			}
			rows := make([][]string, len(versions))
			for i, v := range versions {
				rows[i] = []string{v.ID, strconv.Itoa(v.Ordinal), v.Label, v.ModelComplexity, cmdutil.FormatTime(v.CreatedAt)}
			}
			if versions == nil {
				versions = []Version{}
			}
			return app.Printer().List(versions, []string{"id", "version", "label", "complexity", "created"}, rows)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "most versions to return (default: all)")
	return cmd
}

func newLabelVersion(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "label-version <agent> <version> <label>",
		Short: "Label a version of an agent",
		Long: `Give a version of an agent a label, such as a release name. The version is its
number, its id, or "latest".`,
		Example: "  partite agent label-version support latest v1.2\n  partite agent label-version support 3 stable",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			versionID := args[1]
			if !cmdutil.IsID(versionID, "cagv") {
				id, err := Resource.Resolve(ctx, app, args[0])
				if err != nil {
					return err
				}
				if versionID == "latest" {
					a, err := Resource.Get(ctx, app, id)
					if err != nil {
						return err
					}
					versionID = a.VersionID
				} else {
					n, err := strconv.Atoi(versionID)
					if err != nil {
						return cmdutil.UsageError("version must be a number, a version id or latest, got %q", args[1])
					}
					versions, err := listVersions(ctx, app, id, 0)
					if err != nil {
						return err
					}
					versionID = ""
					for _, v := range versions {
						if v.Ordinal == n {
							versionID = v.ID
						}
					}
					if versionID == "" {
						return &cmdutil.Error{Code: cmdutil.ExitNotFound, Msg: fmt.Sprintf("agent %s has no version %d", args[0], n), Hint: "List them with: partite agent versions " + args[0]}
					}
				}
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.AgentLabelVersion(ctx, c, versionID, args[2])
			if err != nil {
				return err
			}
			v := versionFromAPI(resp.LabelAgentVersion)
			app.Printer().Done("Labeled version %d of %s %q.", v.Ordinal, args[0], v.Label)
			if app.Format() == output.Table {
				return nil
			}
			return app.Printer().Data(v)
		},
	}
}

func newCopy(app *cmdutil.App) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "copy <name-or-id>",
		Short: "Copy an agent to a new agent",
		Long: `Copy an agent's latest version to a new agent. The server picks the new
agent's name unless you pass --name.`,
		Example: "  partite agent copy support --name support-test",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.AgentCopy(ctx, c, id)
			if err != nil {
				return err
			}
			a := fromAPI(resp.CopyAgent.Agent)
			if name != "" {
				renamed, err := update(ctx, app, a.ID, &gql.UpdateAgent{Name: gql.Opt(name)})
				if err != nil {
					return fmt.Errorf("copied %s to %s (%s), but could not rename it: %w", args[0], a.Name, a.ID, err)
				}
				a = renamed
			}
			app.Printer().Done("Copied agent %s to %s (%s).", args[0], a.Name, a.ID)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, a)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name for the copy")
	return cmd
}
