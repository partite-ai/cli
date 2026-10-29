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

// Store is the view of a memory store printed by get and read by -f.
type Store struct {
	ID                         string  `json:"id,omitempty"`
	Name                       string  `json:"name"`
	Description                string  `json:"description,omitempty"`
	EmbeddingProfileID         string  `json:"embedding_profile_id,omitempty"`
	UserNameTagKey             string  `json:"user_name_tag_key,omitempty"`
	IngestConversations        bool    `json:"ingest_conversations"`
	IndexConversationMessages  bool    `json:"index_conversation_messages"`
	ExtractIntervalSeconds     int     `json:"extract_interval_seconds,omitempty"`
	ConsolidateIntervalSeconds int     `json:"consolidate_interval_seconds,omitempty"`
	Scopes                     []Scope `json:"scopes"`
	CreatedAt                  string  `json:"created_at,omitempty"`
	UpdatedAt                  string  `json:"updated_at,omitempty"`
}

func storeFromAPI(d gql.MemoryStoreDetails) Store {
	s := Store{
		ID:                         d.Id,
		Name:                       d.Name,
		Description:                cmdutil.Deref(d.Description),
		UserNameTagKey:             cmdutil.Deref(d.UserNameTagKey),
		IngestConversations:        d.IngestConversations,
		IndexConversationMessages:  d.IndexConversationMessages,
		ExtractIntervalSeconds:     d.ExtractIntervalSeconds,
		ConsolidateIntervalSeconds: d.ConsolidateIntervalSeconds,
		Scopes:                     make([]Scope, len(d.Scopes)),
		CreatedAt:                  d.CreatedAt,
		UpdatedAt:                  d.UpdatedAt,
	}
	if d.EmbeddingProfile != nil {
		s.EmbeddingProfileID = d.EmbeddingProfile.Id
	}
	for i, sc := range d.Scopes {
		s.Scopes[i] = scopeFromAPI(sc.MemoryStoreScopeDetails)
	}
	return s
}

var StoreResource = &cmdutil.Resource[Store]{
	WebList:  "memory/stores",
	WebPage:  "memory/stores/%s",
	WebLink:  "memory-store",
	Kind:     "memory store",
	Command:  "memory store",
	IDPrefix: "cmst",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Store, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Store], error) {
			resp, err := gql.MemoryListStores(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Store]{}, err
			}
			conn := resp.MemoryStores
			items := make([]Store, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = storeFromAPI(e.Node.MemoryStoreDetails)
			}
			return cmdutil.Page[Store]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Store, error) {
		c, err := app.GQL()
		if err != nil {
			return Store{}, err
		}
		resp, err := gql.GetMemoryStore(ctx, c, id)
		if err != nil {
			return Store{}, err
		}
		if resp.MemoryStore == nil {
			return Store{}, cmdutil.NotFound("memory store", id, "partite memory store list")
		}
		return storeFromAPI(*resp.MemoryStore), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteMemoryStore(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(s Store) string { return s.ID },
	Name:    func(s Store) string { return s.Name },
	Columns: []string{"id", "name", "ingest", "scopes", "embedding profile", "created"},
	Row: func(s Store) []string {
		return []string{s.ID, s.Name, strconv.FormatBool(s.IngestConversations), strconv.Itoa(len(s.Scopes)), s.EmbeddingProfileID, cmdutil.FormatTime(s.CreatedAt)}
	},
	Fields: func(s Store) []output.Field {
		fields := []output.Field{
			{Label: "ID", Value: s.ID},
			{Label: "Name", Value: s.Name},
			{Label: "Description", Value: s.Description},
			{Label: "Embedding profile", Value: s.EmbeddingProfileID},
			{Label: "Ingest conversations", Value: strconv.FormatBool(s.IngestConversations)},
			{Label: "Index messages", Value: strconv.FormatBool(s.IndexConversationMessages)},
			{Label: "User name tag key", Value: s.UserNameTagKey},
			{Label: "Extract every", Value: interval(s.ExtractIntervalSeconds)},
			{Label: "Consolidate every", Value: interval(s.ConsolidateIntervalSeconds)},
		}
		for _, sc := range s.Scopes {
			fields = append(fields, output.Field{Label: "Scope", Value: scopeSummary(sc)})
		}
		return append(fields,
			output.Field{Label: "Created", Value: s.CreatedAt},
			output.Field{Label: "Updated", Value: s.UpdatedAt},
		)
	},
}

const storeSpecExample = `name: support
description: What support conversations taught us
ingest_conversations: true
index_conversation_messages: true
embedding_profile_id: openai-small     # a name or an id
user_name_tag_key: user
extract_interval_seconds: 300
consolidate_interval_seconds: 28800
scopes:
  # One memory for each user: facts from a document tagged user=bob carry user=bob.
  - key: user
    kind: SLICED
    match: user
    extraction_enabled: true
    extraction_model_profile_id: fast     # a name, provider/name or an id
    consolidation_enabled: true
    consolidation_model_profile_id: fast
  # One shared memory of everything, tagged audience=everyone.
  - key: audience
    kind: UNSLICED
    value: everyone
    description: What holds for every user
    extraction_enabled: true
    extraction_model_profile_id: fast
    extraction_prompt: Keep only facts about the product.`

func newStoreCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "store",
		Aliases: []string{"stores"},
		Short:   "Manage memory stores",
		Long: `A memory store is what a mesh remembers. It can take in the turns of every
conversation (ingest_conversations), embed them so they can be found by
meaning (index_conversation_messages, which needs an embedding profile), and
extract facts and observations through its scopes.

A scope selects documents by a tag key (match) and tags the records it
produces with its own key. A SLICED scope keeps a separate memory for each
value of the tag it matches; an UNSLICED scope makes one memory, tagged
key=value. Scopes that extract or consolidate need a model profile, and the
store then needs an embedding profile.

Example spec for apply:

` + storeSpecExample,
	}
	get := StoreResource.GetCmd(app)
	get.Long = strings.ReplaceAll(get.Long, "memory store.yaml", "store.yaml")
	view := StoreResource.ViewCmd(app)
	view.Example = "  partite memory store view\n  partite memory store view support"
	del := StoreResource.DeleteCmd(app)
	del.Short = "Delete memory stores, with everything they hold"
	del.Example = "  partite memory store delete support\n  partite memory store delete support --yes"
	cmd.AddCommand(
		StoreResource.ListCmd(app),
		get,
		view,
		newStoreCreate(app),
		newStoreUpdate(app),
		StoreResource.ApplyCmd(app, `  partite memory store apply -f support-memory.yaml
  partite memory store get support -o yaml > s.yaml && partite memory store apply -f s.yaml

Scopes are matched by kind, key and value. A scope in the file that the store
lacks is created, one the store has that the file lacks is deleted with the
facts and observations it produced, and the rest are updated in place.`, func(ctx context.Context, spec Store, id string) (Store, error) {
			if id == "" {
				return createStore(ctx, app, spec)
			}
			return applyStore(ctx, app, id, spec)
		}),
		del,
	)
	return cmd
}

// resolveStoreRefs turns the names in spec into ids.
func resolveStoreRefs(ctx context.Context, app *cmdutil.App, spec *Store) error {
	var err error
	if spec.EmbeddingProfileID, err = optRef(ctx, app, spec.EmbeddingProfileID, resolveEmbeddingProfile); err != nil {
		return err
	}
	for i := range spec.Scopes {
		if err := resolveScopeRefs(ctx, app, &spec.Scopes[i]); err != nil {
			return err
		}
	}
	return nil
}

func createStore(ctx context.Context, app *cmdutil.App, spec Store) (Store, error) {
	if err := resolveStoreRefs(ctx, app, &spec); err != nil {
		return Store{}, err
	}
	input := &gql.NewMemoryStore{
		Name:                       spec.Name,
		Description:                gql.OptPtr(cmdutil.OptString(spec.Description)),
		IngestConversations:        spec.IngestConversations,
		IndexConversationMessages:  spec.IndexConversationMessages,
		EmbeddingProfileID:         gql.OptPtr(cmdutil.OptString(spec.EmbeddingProfileID)),
		UserNameTagKey:             gql.OptPtr(cmdutil.OptString(spec.UserNameTagKey)),
		ExtractIntervalSeconds:     spec.ExtractIntervalSeconds,
		ConsolidateIntervalSeconds: spec.ConsolidateIntervalSeconds,
	}
	for _, sc := range spec.Scopes {
		n, err := newScopeInput(sc)
		if err != nil {
			return Store{}, err
		}
		input.Scopes = append(input.Scopes, n)
	}
	c, err := app.GQL()
	if err != nil {
		return Store{}, err
	}
	resp, err := gql.CreateMemoryStore(ctx, c, input)
	if err != nil {
		return Store{}, err
	}
	return storeFromAPI(resp.CreateMemoryStore), nil
}

// applyStore makes the store with this id match spec, scopes included, in one
// update so the store and its scopes change together.
func applyStore(ctx context.Context, app *cmdutil.App, id string, spec Store) (Store, error) {
	if err := resolveStoreRefs(ctx, app, &spec); err != nil {
		return Store{}, err
	}
	cur, err := StoreResource.Get(ctx, app, id)
	if err != nil {
		return Store{}, err
	}
	input := &gql.UpdateMemoryStore{
		Name:                      gql.Opt(spec.Name),
		Description:               clearable(spec.Description),
		IngestConversations:       gql.Opt(spec.IngestConversations),
		IndexConversationMessages: gql.Opt(spec.IndexConversationMessages),
		EmbeddingProfileID:        clearable(spec.EmbeddingProfileID),
		UserNameTagKey:            clearable(spec.UserNameTagKey),
	}
	if spec.ExtractIntervalSeconds != 0 {
		input.ExtractIntervalSeconds = gql.Opt(spec.ExtractIntervalSeconds)
	}
	if spec.ConsolidateIntervalSeconds != 0 {
		input.ConsolidateIntervalSeconds = gql.Opt(spec.ConsolidateIntervalSeconds)
	}
	if err := reconcileScopes(cur.Scopes, spec.Scopes, input); err != nil {
		return Store{}, err
	}
	return updateStore(ctx, app, id, input)
}

// reconcileScopes fills the scope lists of input so that the store's scopes
// become want. A scope is known by its kind, key and value, which never change.
func reconcileScopes(have, want []Scope, input *gql.UpdateMemoryStore) error {
	byIdentity := make(map[string]Scope, len(have))
	for _, sc := range have {
		byIdentity[sc.identity()] = sc
	}
	seen := map[string]bool{}
	for _, sc := range want {
		n, err := newScopeInput(sc)
		if err != nil {
			return err
		}
		ident := sc.identity()
		if seen[ident] {
			return cmdutil.UsageError("the scope %s is listed twice", scopeSummary(sc))
		}
		seen[ident] = true
		cur, ok := byIdentity[ident]
		if !ok {
			input.CreateScopes = append(input.CreateScopes, n)
			continue
		}
		delete(byIdentity, ident)
		if changes := scopeChanges(cur, sc); changes != nil {
			input.UpdateScopes = append(input.UpdateScopes, &gql.MemoryStoreScopeEdit{ID: cur.ID, Changes: changes})
		}
	}
	for _, sc := range byIdentity {
		input.DeleteScopeIDs = append(input.DeleteScopeIDs, sc.ID)
	}
	return nil
}

func updateStore(ctx context.Context, app *cmdutil.App, id string, input *gql.UpdateMemoryStore) (Store, error) {
	c, err := app.GQL()
	if err != nil {
		return Store{}, err
	}
	resp, err := gql.ModifyMemoryStore(ctx, c, id, input)
	if err != nil {
		return Store{}, err
	}
	return storeFromAPI(resp.UpdateMemoryStore), nil
}

func addStoreFlags(cmd *cobra.Command, spec *Store, embedding *string) {
	f := cmd.Flags()
	f.StringVar(&spec.Description, "description", "", "description")
	f.StringVar(embedding, "embedding-profile", "", "embedding profile name or id that records are embedded with")
	f.StringVar(&spec.UserNameTagKey, "user-name-tag-key", "", "request tag key that holds the name of the person speaking")
	f.BoolVar(&spec.IngestConversations, "ingest-conversations", false, "store every turn of every conversation")
	f.BoolVar(&spec.IndexConversationMessages, "index-conversation-messages", false, "embed those turns so they can be found by meaning")
	f.IntVar(&spec.ExtractIntervalSeconds, "extract-interval", 0, "seconds between extraction runs (default 300)")
	f.IntVar(&spec.ConsolidateIntervalSeconds, "consolidate-interval", 0, "seconds between consolidation runs (default 28800)")
}

func newStoreCreate(app *cmdutil.App) *cobra.Command {
	var spec Store
	var embedding, file string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a memory store",
		Long: `Create a memory store. Simple settings are flags; scopes come from a spec
file (-f) or are added later with partite memory scope create.`,
		Example: `  partite memory store create support --ingest-conversations
  partite memory store create support --ingest-conversations --index-conversation-messages --embedding-profile openai-small
  partite memory store create -f support-memory.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				var fromFile Store
				if err := app.ReadSpec(file, &fromFile); err != nil {
					return err
				}
				fromFile.mergeFlags(cmd, spec, embedding)
				spec = fromFile
			} else {
				spec.EmbeddingProfileID = embedding
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			s, err := createStore(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created memory store %s (%s).", s.Name, s.ID)
			if app.Format() == output.Table {
				return nil
			}
			return StoreResource.Show(app, s)
		},
	}
	addStoreFlags(cmd, &spec, &embedding)
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the store from a YAML or JSON file; flags override it")
	return cmd
}

// mergeFlags copies the flags that were passed from flags onto s.
func (s *Store) mergeFlags(cmd *cobra.Command, flags Store, embedding string) {
	f := cmd.Flags()
	if f.Changed("description") {
		s.Description = flags.Description
	}
	if f.Changed("embedding-profile") {
		s.EmbeddingProfileID = embedding
	}
	if f.Changed("user-name-tag-key") {
		s.UserNameTagKey = flags.UserNameTagKey
	}
	if f.Changed("ingest-conversations") {
		s.IngestConversations = flags.IngestConversations
	}
	if f.Changed("index-conversation-messages") {
		s.IndexConversationMessages = flags.IndexConversationMessages
	}
	if f.Changed("extract-interval") {
		s.ExtractIntervalSeconds = flags.ExtractIntervalSeconds
	}
	if f.Changed("consolidate-interval") {
		s.ConsolidateIntervalSeconds = flags.ConsolidateIntervalSeconds
	}
}

func newStoreUpdate(app *cmdutil.App) *cobra.Command {
	var spec Store
	var embedding, name string
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a memory store's settings",
		Long: `Change a memory store's settings. Only the flags you pass change. Pass an
empty value to clear --description, --embedding-profile or --user-name-tag-key.
Change scopes with partite memory scope, or all at once with apply.`,
		Example: `  partite memory store update support --ingest-conversations=false
  partite memory store update support --embedding-profile openai-small --index-conversation-messages`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := StoreResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			input := &gql.UpdateMemoryStore{}
			changed := false
			if f.Changed("name") {
				input.Name, changed = gql.Opt(name), true
			}
			if f.Changed("description") {
				input.Description, changed = clearable(spec.Description), true
			}
			if f.Changed("embedding-profile") {
				ref, err := optRef(ctx, app, embedding, resolveEmbeddingProfile)
				if err != nil {
					return err
				}
				input.EmbeddingProfileID, changed = clearable(ref), true
			}
			if f.Changed("user-name-tag-key") {
				input.UserNameTagKey, changed = clearable(spec.UserNameTagKey), true
			}
			if f.Changed("ingest-conversations") {
				input.IngestConversations, changed = gql.Opt(spec.IngestConversations), true
			}
			if f.Changed("index-conversation-messages") {
				input.IndexConversationMessages, changed = gql.Opt(spec.IndexConversationMessages), true
			}
			if f.Changed("extract-interval") {
				input.ExtractIntervalSeconds, changed = gql.Opt(spec.ExtractIntervalSeconds), true
			}
			if f.Changed("consolidate-interval") {
				input.ConsolidateIntervalSeconds, changed = gql.Opt(spec.ConsolidateIntervalSeconds), true
			}
			if !changed {
				return cmdutil.UsageError("nothing to change; see --help for the flags")
			}
			s, err := updateStore(ctx, app, id, input)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated memory store %s.", s.Name)
			if app.Format() == output.Table {
				return nil
			}
			return StoreResource.Show(app, s)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	addStoreFlags(cmd, &spec, &embedding)
	return cmd
}

func interval(seconds int) string {
	if seconds == 0 {
		return "default"
	}
	return fmt.Sprintf("%ds", seconds)
}
