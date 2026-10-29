package memory

import (
	"context"
	"strconv"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Profile is the view of a memory interaction profile printed by get and read
// by -f. Field names follow partiteai_memory_interaction_profile.
type Profile struct {
	ID                  string               `json:"id,omitempty"`
	Name                string               `json:"name"`
	Description         string               `json:"description,omitempty"`
	AllowExpansion      bool                 `json:"allow_expansion"`
	ConversationHistory *ConversationHistory `json:"conversation_history,omitempty"`
	ContextInjections   []ContextInjection   `json:"context_injections,omitempty"`
	QueryTools          []QueryTool          `json:"query_tools,omitempty"`
	WriteTools          []WriteTool          `json:"write_tools,omitempty"`
	CreatedAt           string               `json:"created_at,omitempty"`
	UpdatedAt           string               `json:"updated_at,omitempty"`
}

type ConversationHistory struct {
	TokenBudget int          `json:"token_budget"`
	Selection   string       `json:"selection,omitempty"`
	Query       *QuerySource `json:"query,omitempty"`
}

type QuerySource struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type Selection struct {
	Kind string         `json:"kind"`
	Tags []SelectionTag `json:"tags,omitempty"`
}

type SelectionTag struct {
	Source        string `json:"source"`
	TagKey        string `json:"tag_key"`
	TagValue      string `json:"tag_value,omitempty"`
	RequestTagKey string `json:"request_tag_key,omitempty"`
}

type ContextInjection struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	TokenBudget int         `json:"token_budget"`
	RecordTypes []string    `json:"record_types"`
	Selection   Selection   `json:"selection"`
	Query       QuerySource `json:"query"`
}

type QueryTool struct {
	ToolName     string        `json:"tool_name"`
	Description  string        `json:"description"`
	RecordTypes  []string      `json:"record_types"`
	Selection    Selection     `json:"selection"`
	TokenBudget  int           `json:"token_budget,omitempty"`
	EffortLevels []EffortLevel `json:"effort_levels,omitempty"`
}

type EffortLevel struct {
	Level       string `json:"level"`
	TokenBudget int    `json:"token_budget"`
}

type WriteTool struct {
	ToolName    string       `json:"tool_name"`
	Description string       `json:"description"`
	TagMappings []TagMapping `json:"tag_mappings,omitempty"`
}

type TagMapping struct {
	Kind     string   `json:"kind"`
	Keys     []string `json:"keys,omitempty"`
	TagKey   string   `json:"tag_key,omitempty"`
	TagValue string   `json:"tag_value,omitempty"`
}

func recordTypesFromAPI(types []gql.MemoryRecordType) []string {
	out := make([]string, len(types))
	for i, t := range types {
		out[i] = string(t)
	}
	return out
}

func selectionFromAPI(d gql.MemorySelectionDetails) Selection {
	s := Selection{Kind: string(d.Kind)}
	for _, t := range d.Tags {
		s.Tags = append(s.Tags, SelectionTag{
			Source:        string(t.Source),
			TagKey:        t.TagKey,
			TagValue:      cmdutil.Deref(t.TagValue),
			RequestTagKey: cmdutil.Deref(t.RequestTagKey),
		})
	}
	return s
}

func querySourceFromAPI(d gql.MemoryQuerySourceDetails) QuerySource {
	return QuerySource{Kind: string(d.Kind), Text: cmdutil.Deref(d.Text)}
}

func profileFromAPI(d gql.MemoryInteractionProfileDetails) Profile {
	p := Profile{
		ID:             d.Id,
		Name:           d.Name,
		Description:    cmdutil.Deref(d.Description),
		AllowExpansion: d.AllowExpansion,
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
	}
	if h := d.ConversationHistory; h != nil {
		p.ConversationHistory = &ConversationHistory{TokenBudget: h.TokenBudget, Selection: string(h.Selection)}
		if h.Query != nil {
			q := querySourceFromAPI(h.Query.MemoryQuerySourceDetails)
			p.ConversationHistory.Query = &q
		}
	}
	for _, in := range d.ContextInjections {
		p.ContextInjections = append(p.ContextInjections, ContextInjection{
			Name:        in.Name,
			Description: cmdutil.Deref(in.Description),
			TokenBudget: in.TokenBudget,
			RecordTypes: recordTypesFromAPI(in.RecordTypes),
			Selection:   selectionFromAPI(in.Selection.MemorySelectionDetails),
			Query:       querySourceFromAPI(in.Query.MemoryQuerySourceDetails),
		})
	}
	for _, t := range d.QueryTools {
		qt := QueryTool{
			ToolName:    t.ToolName,
			Description: t.Description,
			RecordTypes: recordTypesFromAPI(t.RecordTypes),
			Selection:   selectionFromAPI(t.Selection.MemorySelectionDetails),
			TokenBudget: cmdutil.Deref(t.TokenBudget),
		}
		for _, l := range t.EffortLevels {
			qt.EffortLevels = append(qt.EffortLevels, EffortLevel{Level: l.Level, TokenBudget: l.TokenBudget})
		}
		p.QueryTools = append(p.QueryTools, qt)
	}
	for _, t := range d.WriteTools {
		wt := WriteTool{ToolName: t.ToolName, Description: t.Description}
		for _, m := range t.TagMappings {
			wt.TagMappings = append(wt.TagMappings, TagMapping{
				Kind:     string(m.Kind),
				Keys:     m.Keys,
				TagKey:   cmdutil.Deref(m.TagKey),
				TagValue: cmdutil.Deref(m.TagValue),
			})
		}
		p.WriteTools = append(p.WriteTools, wt)
	}
	return p
}

func recordTypesToAPI(where string, types []string) ([]string, error) {
	out := make([]string, len(types))
	for i, t := range types {
		v, err := parseEnum(where+" record_types", t, gql.AllMemoryRecordType)
		if err != nil {
			return nil, err
		}
		out[i] = string(v)
	}
	return out, nil
}

func querySourceToAPI(where string, q QuerySource) (*gql.MemoryQuerySourceInput, error) {
	kind, err := parseEnum(where+" query kind", q.Kind, gql.AllMemoryQuerySourceKind)
	if err != nil {
		return nil, err
	}
	return &gql.MemoryQuerySourceInput{Kind: string(kind), Text: gql.OptPtr(cmdutil.OptString(q.Text))}, nil
}

func selectionToAPI(where string, s Selection) (*gql.MemorySelectionInput, error) {
	kind, err := parseEnum(where+" selection kind", s.Kind, gql.AllMemorySelectionKind)
	if err != nil {
		return nil, err
	}
	out := &gql.MemorySelectionInput{Kind: string(kind)}
	for _, t := range s.Tags {
		src, err := parseEnum(where+" selection tag source", t.Source, gql.AllMemoryTagSource)
		if err != nil {
			return nil, err
		}
		out.Tags = append(out.Tags, &gql.MemorySelectionTagInput{
			Source:        string(src),
			TagKey:        t.TagKey,
			TagValue:      gql.OptPtr(cmdutil.OptString(t.TagValue)),
			RequestTagKey: gql.OptPtr(cmdutil.OptString(t.RequestTagKey)),
		})
	}
	return out, nil
}

// profileInputs turns the nested parts of p into the server's input types.
// Every list is non-nil, so that an update empties a list the spec left out.
func profileInputs(p Profile) (*gql.MemoryConversationHistoryInput, []*gql.MemoryContextInjectionInput, []*gql.MemoryQueryToolInput, []*gql.MemoryWriteToolInput, error) {
	var history *gql.MemoryConversationHistoryInput
	if h := p.ConversationHistory; h != nil {
		sel := gql.MemoryHistorySelectionKindRecent
		if h.Selection != "" {
			var err error
			if sel, err = parseEnum("conversation_history selection", h.Selection, gql.AllMemoryHistorySelectionKind); err != nil {
				return nil, nil, nil, nil, err
			}
		}
		history = &gql.MemoryConversationHistoryInput{TokenBudget: h.TokenBudget, Selection: string(sel)}
		if h.Query != nil {
			q, err := querySourceToAPI("conversation_history", *h.Query)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			history.Query = q
		}
	}
	injections := []*gql.MemoryContextInjectionInput{}
	for _, in := range p.ContextInjections {
		where := "context injection " + strconv.Quote(in.Name)
		types, err := recordTypesToAPI(where, in.RecordTypes)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		sel, err := selectionToAPI(where, in.Selection)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		q, err := querySourceToAPI(where, in.Query)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		injections = append(injections, &gql.MemoryContextInjectionInput{
			Name:        in.Name,
			Description: gql.OptPtr(cmdutil.OptString(in.Description)),
			TokenBudget: in.TokenBudget,
			RecordTypes: types,
			Selection:   sel,
			Query:       q,
		})
	}
	queryTools := []*gql.MemoryQueryToolInput{}
	for _, t := range p.QueryTools {
		where := "query tool " + strconv.Quote(t.ToolName)
		types, err := recordTypesToAPI(where, t.RecordTypes)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		sel, err := selectionToAPI(where, t.Selection)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		qt := &gql.MemoryQueryToolInput{
			ToolName:    t.ToolName,
			Description: t.Description,
			RecordTypes: types,
			Selection:   sel,
		}
		if t.TokenBudget != 0 {
			qt.TokenBudget = gql.Opt(t.TokenBudget)
		}
		for _, l := range t.EffortLevels {
			qt.EffortLevels = append(qt.EffortLevels, &gql.MemoryEffortLevelInput{Level: l.Level, TokenBudget: l.TokenBudget})
		}
		queryTools = append(queryTools, qt)
	}
	writeTools := []*gql.MemoryWriteToolInput{}
	for _, t := range p.WriteTools {
		wt := &gql.MemoryWriteToolInput{ToolName: t.ToolName, Description: t.Description}
		for _, m := range t.TagMappings {
			kind, err := parseEnum("write tool "+strconv.Quote(t.ToolName)+" tag mapping kind", m.Kind, gql.AllMemoryTagMappingKind)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			wt.TagMappings = append(wt.TagMappings, &gql.MemoryTagMappingInput{
				Kind:     string(kind),
				Keys:     m.Keys,
				TagKey:   gql.OptPtr(cmdutil.OptString(m.TagKey)),
				TagValue: gql.OptPtr(cmdutil.OptString(m.TagValue)),
			})
		}
		writeTools = append(writeTools, wt)
	}
	return history, injections, queryTools, writeTools, nil
}

func toolNames[T any](items []T, name func(T) string) string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = name(it)
	}
	return strings.Join(names, ", ")
}

var ProfileResource = &cmdutil.Resource[Profile]{
	WebList:  "memory/interaction-profiles",
	WebPage:  "memory/interaction-profiles/%s",
	WebLink:  "memory-profile",
	Kind:     "memory profile",
	Command:  "memory profile",
	IDPrefix: "cmip",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Profile, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Profile], error) {
			resp, err := gql.MemoryListInteractionProfiles(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Profile]{}, err
			}
			conn := resp.MemoryInteractionProfiles
			items := make([]Profile, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = profileFromAPI(e.Node.MemoryInteractionProfileDetails)
			}
			return cmdutil.Page[Profile]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Profile, error) {
		c, err := app.GQL()
		if err != nil {
			return Profile{}, err
		}
		resp, err := gql.GetMemoryInteractionProfile(ctx, c, id)
		if err != nil {
			return Profile{}, err
		}
		if resp.MemoryInteractionProfile == nil {
			return Profile{}, cmdutil.NotFound("memory profile", id, "partite memory profile list")
		}
		return profileFromAPI(*resp.MemoryInteractionProfile), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteMemoryInteractionProfile(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(p Profile) string { return p.ID },
	Name:    func(p Profile) string { return p.Name },
	Columns: []string{"id", "name", "history", "injections", "query tools", "write tools", "created"},
	Row: func(p Profile) []string {
		history := "no"
		if p.ConversationHistory != nil {
			history = strings.ToLower(p.ConversationHistory.Selection)
		}
		return []string{p.ID, p.Name, history, strconv.Itoa(len(p.ContextInjections)),
			toolNames(p.QueryTools, func(t QueryTool) string { return t.ToolName }),
			toolNames(p.WriteTools, func(t WriteTool) string { return t.ToolName }),
			cmdutil.FormatTime(p.CreatedAt)}
	},
	Fields: func(p Profile) []output.Field {
		fields := []output.Field{
			{Label: "ID", Value: p.ID},
			{Label: "Name", Value: p.Name},
			{Label: "Description", Value: p.Description},
			{Label: "Allow expansion", Value: strconv.FormatBool(p.AllowExpansion)},
		}
		if h := p.ConversationHistory; h != nil {
			fields = append(fields, output.Field{Label: "History", Value: h.Selection + ", " + strconv.Itoa(h.TokenBudget) + " tokens"})
		}
		for _, in := range p.ContextInjections {
			fields = append(fields, output.Field{Label: "Injection", Value: in.Name + ": " + strings.Join(in.RecordTypes, ",") + ", " + strconv.Itoa(in.TokenBudget) + " tokens"})
		}
		for _, t := range p.QueryTools {
			fields = append(fields, output.Field{Label: "Query tool", Value: t.ToolName + ": " + strings.Join(t.RecordTypes, ",")})
		}
		for _, t := range p.WriteTools {
			fields = append(fields, output.Field{Label: "Write tool", Value: t.ToolName})
		}
		fields = append(fields, output.Field{Label: "Created", Value: p.CreatedAt}, output.Field{Label: "Updated", Value: p.UpdatedAt})
		return fields
	},
}

const profileSpecExample = `name: support-memory
description: What the support agent remembers about each user
allow_expansion: true          # tools to read around a record it was given

# The conversation so far, in the prompt.
conversation_history:
  token_budget: 4000
  selection: RECENT            # RECENT: newest turns. RELEVANT: the turns that match query.
  # query: {kind: DERIVED}     # RELEVANT only

# Memory put in the prompt without the agent asking.
context_injections:
  - name: about-this-user
    description: What you know about the person you are talking to
    token_budget: 1500
    record_types: [OBSERVATION, FACT]   # CHUNK, FACT, OBSERVATION
    selection:
      kind: TAGS               # ALL reads everything; TAGS reads records with these tags
      tags:
        - source: REQUEST_TAG  # the value comes from the request's tags
          tag_key: user        # the key records are tagged with
          request_tag_key: user
    query:
      kind: DERIVED            # DERIVED: from the request. FIXED: the text below.

# Tools the agent can call to search memory.
query_tools:
  - tool_name: search_product_notes
    description: Search what we know about the product. Call it before answering product questions.
    record_types: [OBSERVATION]
    selection:
      kind: TAGS
      tags:
        - source: FIXED
          tag_key: audience
          tag_value: everyone
    effort_levels:             # or token_budget: 2000 for a single amount
      - level: quick
        token_budget: 500
      - level: deep
        token_budget: 4000

# Tools the agent can call to write a memory.
write_tools:
  - tool_name: remember
    description: Save something the user asked you to remember.
    tag_mappings:              # decide which scopes process what it writes
      - kind: KEYS_FROM_REQUEST  # also ALL_FROM_REQUEST, or FIXED with tag_key and tag_value
        keys: [user]`

func newProfileCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Aliases: []string{"profiles", "interaction-profile"},
		Short:   "Manage memory interaction profiles",
		Long: `A memory interaction profile states how an agent uses memory: what is put in
its prompt (conversation_history and context_injections), and which tools it
can call to search (query_tools) or write (write_tools) memory. Give a profile
to an agent in the agent's configuration. A profile does nothing until the
agent runs in a mesh that has a memory store.

Profiles are mostly nested settings, so write them as a spec file and use
create -f or apply -f. Example:

` + profileSpecExample,
	}
	get := ProfileResource.GetCmd(app)
	get.Long = strings.ReplaceAll(get.Long, "memory profile.yaml", "profile.yaml")
	view := ProfileResource.ViewCmd(app)
	view.Example = "  partite memory profile view\n  partite memory profile view support-memory"
	del := ProfileResource.DeleteCmd(app)
	del.Example = "  partite memory profile delete support-memory\n  partite memory profile delete support-memory --yes"
	cmd.AddCommand(
		ProfileResource.ListCmd(app),
		get,
		view,
		newProfileCreate(app),
		newProfileUpdate(app),
		ProfileResource.ApplyCmd(app, `  partite memory profile apply -f support-memory.yaml
  partite memory profile get support-memory -o yaml > p.yaml && partite memory profile apply -f p.yaml

The file states the whole profile: a list it leaves out is emptied.
See partite memory profile --help for an example file.`, func(ctx context.Context, spec Profile, id string) (Profile, error) {
			if id == "" {
				return createProfile(ctx, app, spec)
			}
			return applyProfile(ctx, app, id, spec)
		}),
		del,
	)
	return cmd
}

func createProfile(ctx context.Context, app *cmdutil.App, spec Profile) (Profile, error) {
	history, injections, queryTools, writeTools, err := profileInputs(spec)
	if err != nil {
		return Profile{}, err
	}
	c, err := app.GQL()
	if err != nil {
		return Profile{}, err
	}
	resp, err := gql.CreateMemoryInteractionProfile(ctx, c, &gql.NewMemoryInteractionProfile{
		Name:                spec.Name,
		Description:         gql.OptPtr(cmdutil.OptString(spec.Description)),
		ConversationHistory: history,
		ContextInjections:   injections,
		QueryTools:          queryTools,
		WriteTools:          writeTools,
		AllowExpansion:      spec.AllowExpansion,
	})
	if err != nil {
		return Profile{}, err
	}
	return profileFromAPI(resp.CreateMemoryInteractionProfile), nil
}

func applyProfile(ctx context.Context, app *cmdutil.App, id string, spec Profile) (Profile, error) {
	history, injections, queryTools, writeTools, err := profileInputs(spec)
	if err != nil {
		return Profile{}, err
	}
	return updateProfile(ctx, app, id, &gql.UpdateMemoryInteractionProfile{
		Name:                gql.Opt(spec.Name),
		Description:         clearable(spec.Description),
		ConversationHistory: gql.Opt(history),
		ContextInjections:   gql.Opt(injections),
		QueryTools:          gql.Opt(queryTools),
		WriteTools:          gql.Opt(writeTools),
		AllowExpansion:      gql.Opt(spec.AllowExpansion),
	})
}

func updateProfile(ctx context.Context, app *cmdutil.App, id string, input *gql.UpdateMemoryInteractionProfile) (Profile, error) {
	c, err := app.GQL()
	if err != nil {
		return Profile{}, err
	}
	resp, err := gql.ModifyMemoryInteractionProfile(ctx, c, id, input)
	if err != nil {
		return Profile{}, err
	}
	return profileFromAPI(resp.UpdateMemoryInteractionProfile), nil
}

func newProfileCreate(app *cmdutil.App) *cobra.Command {
	var spec Profile
	var file string
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a memory interaction profile",
		Long: `Create a memory interaction profile, usually from a spec file. The name
argument and the flags override the file. See partite memory profile --help
for an example file.`,
		Example: `  partite memory profile create -f support-memory.yaml
  partite memory profile create support-copy -f support-memory.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			want := spec
			if file != "" {
				if err := app.ReadSpec(file, &want); err != nil {
					return err
				}
				f := cmd.Flags()
				if f.Changed("description") {
					want.Description = spec.Description
				}
				if f.Changed("allow-expansion") {
					want.AllowExpansion = spec.AllowExpansion
				}
			}
			if len(args) == 1 {
				want.Name = args[0]
			}
			if want.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			p, err := createProfile(cmd.Context(), app, want)
			if err != nil {
				return err
			}
			app.Printer().Done("Created memory profile %s (%s).", p.Name, p.ID)
			if app.Format() == output.Table {
				return nil
			}
			return ProfileResource.Show(app, p)
		},
	}
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().BoolVar(&spec.AllowExpansion, "allow-expansion", false, "give the agent tools to read around the records it was given")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the profile from a YAML or JSON file")
	return cmd
}

func newProfileUpdate(app *cmdutil.App) *cobra.Command {
	var name, description string
	var allowExpansion, noHistory bool
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a memory profile's name, description or expansion",
		Long: `Change a memory interaction profile. Only the flags you pass change.
To change injections or tools, edit the spec and apply it:

  partite memory profile get NAME -o yaml > p.yaml
  partite memory profile apply -f p.yaml`,
		Example: `  partite memory profile update support-memory --allow-expansion=false
  partite memory profile update support-memory --no-conversation-history`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := ProfileResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			input := &gql.UpdateMemoryInteractionProfile{}
			changed := false
			if f.Changed("name") {
				input.Name, changed = gql.Opt(name), true
			}
			if f.Changed("description") {
				input.Description, changed = clearable(description), true
			}
			if f.Changed("allow-expansion") {
				input.AllowExpansion, changed = gql.Opt(allowExpansion), true
			}
			if noHistory {
				input.ConversationHistory, changed = gql.Opt[*gql.MemoryConversationHistoryInput](nil), true
			}
			if !changed {
				return cmdutil.UsageError("nothing to change; pass --name, --description, --allow-expansion or --no-conversation-history")
			}
			p, err := updateProfile(ctx, app, id, input)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated memory profile %s.", p.Name)
			if app.Format() == output.Table {
				return nil
			}
			return ProfileResource.Show(app, p)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description; empty clears it")
	cmd.Flags().BoolVar(&allowExpansion, "allow-expansion", false, "give the agent tools to read around the records it was given")
	cmd.Flags().BoolVar(&noHistory, "no-conversation-history", false, "stop putting the conversation history in the prompt")
	return cmd
}
