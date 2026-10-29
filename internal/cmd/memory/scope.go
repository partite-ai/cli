package memory

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Scope is one scope of a memory store, as in the terraform provider's scopes
// list.
type Scope struct {
	ID                          string `json:"id,omitempty"`
	Key                         string `json:"key"`
	Kind                        string `json:"kind"`
	Value                       string `json:"value,omitempty"`
	Match                       string `json:"match,omitempty"`
	Description                 string `json:"description,omitempty"`
	ExtractionEnabled           bool   `json:"extraction_enabled"`
	ExtractionModelProfileID    string `json:"extraction_model_profile_id,omitempty"`
	ExtractionPrompt            string `json:"extraction_prompt,omitempty"`
	ConsolidationEnabled        bool   `json:"consolidation_enabled"`
	ConsolidationModelProfileID string `json:"consolidation_model_profile_id,omitempty"`
	ConsolidationPrompt         string `json:"consolidation_prompt,omitempty"`
}

func scopeFromAPI(d gql.MemoryStoreScopeDetails) Scope {
	s := Scope{
		ID:                   d.Id,
		Key:                  d.Key,
		Kind:                 string(d.Kind),
		Value:                d.Value,
		Match:                d.Match,
		Description:          cmdutil.Deref(d.Description),
		ExtractionEnabled:    d.ExtractionEnabled,
		ExtractionPrompt:     cmdutil.Deref(d.ExtractionPrompt),
		ConsolidationEnabled: d.ConsolidationEnabled,
		ConsolidationPrompt:  cmdutil.Deref(d.ConsolidationPrompt),
	}
	if d.ExtractionModelProfile != nil {
		s.ExtractionModelProfileID = d.ExtractionModelProfile.Id
	}
	if d.ConsolidationModelProfile != nil {
		s.ConsolidationModelProfileID = d.ConsolidationModelProfile.Id
	}
	return s
}

// identity is what names a scope within its store. None of it can change.
func (s Scope) identity() string {
	return strings.ToUpper(s.Kind) + "\x00" + s.Key + "\x00" + s.Value
}

func scopeSummary(s Scope) string {
	out := strings.ToUpper(s.Kind) + " " + s.Key
	if s.Value != "" {
		out += "=" + s.Value
	}
	if s.Match != "" {
		out += " matching " + s.Match
	}
	var does []string
	if s.ExtractionEnabled {
		does = append(does, "extracts")
	}
	if s.ConsolidationEnabled {
		does = append(does, "consolidates")
	}
	if len(does) > 0 {
		out += " (" + strings.Join(does, ", ") + ")"
	}
	return out
}

func resolveScopeRefs(ctx context.Context, app *cmdutil.App, s *Scope) error {
	var err error
	if s.ExtractionModelProfileID, err = optRef(ctx, app, s.ExtractionModelProfileID, resolveModelProfile); err != nil {
		return err
	}
	s.ConsolidationModelProfileID, err = optRef(ctx, app, s.ConsolidationModelProfileID, resolveModelProfile)
	return err
}

func newScopeInput(s Scope) (*gql.NewMemoryStoreScope, error) {
	if s.Key == "" {
		return nil, cmdutil.UsageError("every scope needs a key")
	}
	kind, err := parseEnum("scope kind", s.Kind, gql.AllMemoryScopeKind)
	if err != nil {
		return nil, err
	}
	return &gql.NewMemoryStoreScope{
		Description:                 gql.OptPtr(cmdutil.OptString(s.Description)),
		Kind:                        string(kind),
		Key:                         s.Key,
		Value:                       gql.OptPtr(cmdutil.OptString(s.Value)),
		Match:                       gql.OptPtr(cmdutil.OptString(s.Match)),
		ExtractionEnabled:           s.ExtractionEnabled,
		ExtractionPrompt:            gql.OptPtr(cmdutil.OptString(s.ExtractionPrompt)),
		ExtractionModelProfileID:    gql.OptPtr(cmdutil.OptString(s.ExtractionModelProfileID)),
		ConsolidationEnabled:        s.ConsolidationEnabled,
		ConsolidationPrompt:         gql.OptPtr(cmdutil.OptString(s.ConsolidationPrompt)),
		ConsolidationModelProfileID: gql.OptPtr(cmdutil.OptString(s.ConsolidationModelProfileID)),
	}, nil
}

// scopeChanges returns what to send to make cur into want, or nil when they
// already agree.
func scopeChanges(cur, want Scope) *gql.UpdateMemoryStoreScope {
	var u gql.UpdateMemoryStoreScope
	changed := false
	setString := func(dst *gql.Optional[string], have, w string) {
		if have != w {
			*dst, changed = clearable(w), true
		}
	}
	setString(&u.Description, cur.Description, want.Description)
	setString(&u.Match, cur.Match, want.Match)
	setString(&u.ExtractionPrompt, cur.ExtractionPrompt, want.ExtractionPrompt)
	setString(&u.ExtractionModelProfileID, cur.ExtractionModelProfileID, want.ExtractionModelProfileID)
	setString(&u.ConsolidationPrompt, cur.ConsolidationPrompt, want.ConsolidationPrompt)
	setString(&u.ConsolidationModelProfileID, cur.ConsolidationModelProfileID, want.ConsolidationModelProfileID)
	if cur.ExtractionEnabled != want.ExtractionEnabled {
		u.ExtractionEnabled, changed = gql.Opt(want.ExtractionEnabled), true
	}
	if cur.ConsolidationEnabled != want.ConsolidationEnabled {
		u.ConsolidationEnabled, changed = gql.Opt(want.ConsolidationEnabled), true
	}
	if !changed {
		return nil
	}
	return &u
}

// findScope finds a scope of store by id, by key, or by key=value where two
// scopes share a key.
func findScope(store Store, ref string) (Scope, error) {
	var matches []Scope
	for _, sc := range store.Scopes {
		switch {
		case sc.ID == ref, sc.Key == ref:
			matches = append(matches, sc)
		case sc.Value != "" && sc.Key+"="+sc.Value == ref:
			matches = append(matches, sc)
		}
	}
	switch len(matches) {
	case 0:
		return Scope{}, &cmdutil.Error{
			Code: cmdutil.ExitNotFound,
			Msg:  fmt.Sprintf("memory store %s has no scope %q", store.Name, ref),
			Hint: "List its scopes with: partite memory scope list " + store.Name,
		}
	case 1:
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = m.ID
	}
	return Scope{}, &cmdutil.Error{
		Code: cmdutil.ExitUsage,
		Msg:  fmt.Sprintf("%d scopes of memory store %s have the key %q", len(matches), store.Name, ref),
		Hint: "Use key=value or the id instead: " + strings.Join(ids, ", "),
	}
}

// getStore resolves a store name or id and reads the store.
func getStore(ctx context.Context, app *cmdutil.App, ref string) (Store, error) {
	id, err := StoreResource.Resolve(ctx, app, ref)
	if err != nil {
		return Store{}, err
	}
	return StoreResource.Get(ctx, app, id)
}

func showScope(app *cmdutil.App, s Scope) error {
	return app.Printer().Object(s, s.ID, []output.Field{
		{Label: "ID", Value: s.ID},
		{Label: "Kind", Value: s.Kind},
		{Label: "Key", Value: s.Key},
		{Label: "Value", Value: s.Value},
		{Label: "Match", Value: s.Match},
		{Label: "Description", Value: s.Description},
		{Label: "Extraction", Value: strconv.FormatBool(s.ExtractionEnabled)},
		{Label: "Extraction model", Value: s.ExtractionModelProfileID},
		{Label: "Extraction prompt", Value: s.ExtractionPrompt},
		{Label: "Consolidation", Value: strconv.FormatBool(s.ConsolidationEnabled)},
		{Label: "Consolidation model", Value: s.ConsolidationModelProfileID},
		{Label: "Consolidation prompt", Value: s.ConsolidationPrompt},
	})
}

func newScopeCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scope",
		Aliases: []string{"scopes"},
		Short:   "Manage the scopes of a memory store",
		Long: `A scope decides which documents of a memory store are processed and what tag
the facts and observations it produces carry.

  SLICED    keeps a separate memory for each value of the tag key in --match.
            A document tagged user=bob makes records tagged <key>=bob.
  UNSLICED  makes one memory, tagged <key>=<value>, from every document whose
            tags hold --match, or from every document when --match is empty.

A scope is named by its key, or by key=value when two scopes share a key, or
by its id. Its kind, key and value never change; delete and create it instead.
To set every scope at once, edit the store's spec and use memory store apply.`,
	}
	cmd.AddCommand(newScopeList(app), newScopeGet(app), newScopeCreate(app), newScopeUpdate(app), newScopeDelete(app))
	return cmd
}

func newScopeList(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "list <store>",
		Aliases: []string{"ls"},
		Short:   "List the scopes of a memory store",
		Example: "  partite memory scope list support",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getStore(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			rows := make([][]string, len(store.Scopes))
			for i, s := range store.Scopes {
				rows[i] = []string{s.ID, s.Kind, s.Key, s.Value, s.Match, strconv.FormatBool(s.ExtractionEnabled), strconv.FormatBool(s.ConsolidationEnabled)}
			}
			return app.Printer().List(store.Scopes, []string{"id", "kind", "key", "value", "match", "extract", "consolidate"}, rows)
		},
	}
}

func newScopeGet(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "get <store> <scope>",
		Aliases: []string{"show"},
		Short:   "Show one scope of a memory store",
		Example: "  partite memory scope get support user",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getStore(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			s, err := findScope(store, args[1])
			if err != nil {
				return err
			}
			return showScope(app, s)
		},
	}
}

// scopeFlags are the flags of scope create and update.
type scopeFlags struct {
	scope                                 Scope
	extractionPrompt, consolidationPrompt string
}

func (sf *scopeFlags) add(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&sf.scope.Match, "match", "", "tag key that selects the documents this scope reads")
	f.StringVar(&sf.scope.Description, "description", "", "what this scope is for")
	f.BoolVar(&sf.scope.ExtractionEnabled, "extraction-enabled", false, "extract facts from the documents this scope reads")
	f.StringVar(&sf.scope.ExtractionModelProfileID, "extraction-model-profile", "", "model profile (name, provider/name or id) that extraction runs on")
	f.StringVar(&sf.extractionPrompt, "extraction-prompt", "", "extra instructions for extraction; @file reads a file")
	f.BoolVar(&sf.scope.ConsolidationEnabled, "consolidation-enabled", false, "fold the facts of this scope into observations")
	f.StringVar(&sf.scope.ConsolidationModelProfileID, "consolidation-model-profile", "", "model profile (name, provider/name or id) that consolidation runs on")
	f.StringVar(&sf.consolidationPrompt, "consolidation-prompt", "", "extra instructions for consolidation; @file reads a file")
}

// read finishes the flags: it reads @file values and resolves model profiles.
func (sf *scopeFlags) read(ctx context.Context, app *cmdutil.App) error {
	var err error
	if sf.scope.ExtractionPrompt, err = app.ReadValue(sf.extractionPrompt); err != nil {
		return err
	}
	if sf.scope.ConsolidationPrompt, err = app.ReadValue(sf.consolidationPrompt); err != nil {
		return err
	}
	return resolveScopeRefs(ctx, app, &sf.scope)
}

func newScopeCreate(app *cmdutil.App) *cobra.Command {
	var sf scopeFlags
	cmd := &cobra.Command{
		Use:   "create <store> <key>",
		Short: "Add a scope to a memory store",
		Long: `Add a scope to a memory store. The scope also processes the documents the
store already holds. Extraction and consolidation each need a model profile,
and the store needs an embedding profile.`,
		Example: `  # A separate memory for each user, from documents tagged user=...
  partite memory scope create support user --kind sliced --match user \
    --extraction-enabled --extraction-model-profile fast

  # One memory shared by everyone, tagged audience=everyone
  partite memory scope create support audience --kind unsliced --value everyone \
    --extraction-enabled --extraction-model-profile fast \
    --consolidation-enabled --consolidation-model-profile fast`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			store, err := getStore(ctx, app, args[0])
			if err != nil {
				return err
			}
			if err := sf.read(ctx, app); err != nil {
				return err
			}
			sf.scope.Key = args[1]
			input, err := newScopeInput(sf.scope)
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.MemoryCreateScope(ctx, c, store.ID, input)
			if err != nil {
				return err
			}
			s := scopeFromAPI(resp.CreateMemoryStoreScope)
			app.Printer().Done("Added scope %s to memory store %s (%s).", scopeSummary(s), store.Name, s.ID)
			if app.Format() == output.Table {
				return nil
			}
			return showScope(app, s)
		},
	}
	cmd.Flags().StringVar(&sf.scope.Kind, "kind", "", "SLICED or UNSLICED (required)")
	cmd.Flags().StringVar(&sf.scope.Value, "value", "", "tag value an UNSLICED scope writes under its key")
	sf.add(cmd)
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.RegisterFlagCompletionFunc("kind", cobra.FixedCompletions([]string{"SLICED", "UNSLICED"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newScopeUpdate(app *cmdutil.App) *cobra.Command {
	var sf scopeFlags
	cmd := &cobra.Command{
		Use:   "update <store> <scope>",
		Short: "Change a scope of a memory store",
		Long: `Change a scope. Only the flags you pass change. Pass an empty value to clear
a text flag. The kind, key and value of a scope cannot change.`,
		Example: `  partite memory scope update support user --consolidation-enabled --consolidation-model-profile fast
  partite memory scope update support audience=everyone --extraction-prompt @extract.txt`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			store, err := getStore(ctx, app, args[0])
			if err != nil {
				return err
			}
			cur, err := findScope(store, args[1])
			if err != nil {
				return err
			}
			if err := sf.read(ctx, app); err != nil {
				return err
			}
			f := cmd.Flags()
			want := cur
			for flag, apply := range map[string]func(){
				"match":                       func() { want.Match = sf.scope.Match },
				"description":                 func() { want.Description = sf.scope.Description },
				"extraction-enabled":          func() { want.ExtractionEnabled = sf.scope.ExtractionEnabled },
				"extraction-model-profile":    func() { want.ExtractionModelProfileID = sf.scope.ExtractionModelProfileID },
				"extraction-prompt":           func() { want.ExtractionPrompt = sf.scope.ExtractionPrompt },
				"consolidation-enabled":       func() { want.ConsolidationEnabled = sf.scope.ConsolidationEnabled },
				"consolidation-model-profile": func() { want.ConsolidationModelProfileID = sf.scope.ConsolidationModelProfileID },
				"consolidation-prompt":        func() { want.ConsolidationPrompt = sf.scope.ConsolidationPrompt },
			} {
				if f.Changed(flag) {
					apply()
				}
			}
			changes := scopeChanges(cur, want)
			if changes == nil {
				app.Printer().Done("Scope %s of memory store %s already has those settings.", scopeSummary(cur), store.Name)
				if app.Format() == output.Table {
					return nil
				}
				return showScope(app, cur)
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.MemoryUpdateScope(ctx, c, cur.ID, changes)
			if err != nil {
				return err
			}
			s := scopeFromAPI(resp.UpdateMemoryStoreScope)
			app.Printer().Done("Updated scope %s of memory store %s.", scopeSummary(s), store.Name)
			if app.Format() == output.Table {
				return nil
			}
			return showScope(app, s)
		},
	}
	sf.add(cmd)
	return cmd
}

func newScopeDelete(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <store> <scope>...",
		Aliases: []string{"rm"},
		Short:   "Delete scopes, with the facts and observations they produced",
		Long: `Delete scopes of a memory store, with the facts and observations they
produced. The documents they read stay.`,
		Example: "  partite memory scope delete support audience=everyone --yes",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			store, err := getStore(ctx, app, args[0])
			if err != nil {
				return err
			}
			ids := make([]string, len(args)-1)
			names := make([]string, len(args)-1)
			for i, ref := range args[1:] {
				s, err := findScope(store, ref)
				if err != nil {
					return err
				}
				ids[i], names[i] = s.ID, scopeSummary(s)
			}
			what := fmt.Sprintf("%s of memory store %s (%s)", cmdutil.Plural(len(ids), "scope"), store.Name, strings.Join(names, "; "))
			if err := app.Confirm(fmt.Sprintf("Delete %s, with the facts and observations they produced?", what)); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			if _, err := gql.MemoryDeleteScopes(ctx, c, ids); err != nil {
				return err
			}
			app.Printer().Done("Deleted %s.", what)
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": ids})
			}
			return nil
		},
	}
}
