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

// What memory stores hold: documents and their chunks, facts and
// observations. These are read-only, apart from delete.

type Speaker struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type Document struct {
	ID               string      `json:"id"`
	ExternalID       string      `json:"external_id,omitempty"`
	MediaType        string      `json:"media_type,omitempty"`
	Time             string      `json:"time"`
	IngestedAt       string      `json:"ingested_at"`
	ConversationID   string      `json:"conversation_id,omitempty"`
	Sequence         int         `json:"sequence"`
	Speaker          *Speaker    `json:"speaker,omitempty"`
	Tags             []MemoryTag `json:"tags"`
	Preview          string      `json:"preview"`
	PreviewTruncated bool        `json:"preview_truncated"`
}

type Chunk struct {
	ID         string      `json:"id"`
	DocumentID string      `json:"document_id"`
	Index      int         `json:"index"`
	Text       string      `json:"text"`
	Sequence   int         `json:"sequence"`
	Time       string      `json:"time"`
	Tags       []MemoryTag `json:"tags"`
}

type Fact struct {
	ID           string      `json:"id"`
	Text         string      `json:"text"`
	Class        string      `json:"class"`
	Origin       string      `json:"origin"`
	State        string      `json:"state"`
	MergedIntoID string      `json:"merged_into_id,omitempty"`
	MergedReason string      `json:"merged_reason,omitempty"`
	ObservedAt   string      `json:"observed_at"`
	PeriodStart  string      `json:"period_start,omitempty"`
	PeriodEnd    string      `json:"period_end,omitempty"`
	ChunkID      string      `json:"chunk_id,omitempty"`
	ScopeID      string      `json:"scope_id,omitempty"`
	Tags         []MemoryTag `json:"tags"`
	EntityIDs    []string    `json:"entity_ids"`
}

type Observation struct {
	ID          string     `json:"id"`
	Text        string     `json:"text"`
	Topic       string     `json:"topic"`
	Class       string     `json:"class"`
	ScopeID     string     `json:"scope_id,omitempty"`
	ScopeTag    *MemoryTag `json:"scope_tag,omitempty"`
	FactIDs     []string   `json:"fact_ids"`
	PeriodStart string     `json:"period_start,omitempty"`
	PeriodEnd   string     `json:"period_end,omitempty"`
	UpdatedAt   string     `json:"updated_at"`
	SaidAt      string     `json:"said_at,omitempty"`
}

// tagsFromAPI converts any of the generated tag types, which all embed
// MemoryTagDetails.
func tagsFromAPI[T any, P interface {
	*T
	GetKey() string
	GetValue() string
}](tags []T) []MemoryTag {
	out := make([]MemoryTag, len(tags))
	for i := range tags {
		t := P(&tags[i])
		out[i] = MemoryTag{Key: t.GetKey(), Value: t.GetValue()}
	}
	return out
}

func documentFromAPI(d gql.MemoryDocumentDetails) Document {
	doc := Document{
		ID:               d.Id,
		ExternalID:       cmdutil.Deref(d.ExternalID),
		MediaType:        cmdutil.Deref(d.MediaType),
		Time:             d.Time,
		IngestedAt:       d.IngestedAt,
		ConversationID:   cmdutil.Deref(d.ConversationID),
		Sequence:         d.Sequence,
		Tags:             tagsFromAPI(d.Tags),
		Preview:          d.Preview,
		PreviewTruncated: d.PreviewTruncated,
	}
	if d.Speaker != nil {
		doc.Speaker = &Speaker{Name: d.Speaker.Name, Role: d.Speaker.Role}
	}
	return doc
}

func chunkFromAPI(d gql.MemoryChunkDetails) Chunk {
	return Chunk{ID: d.Id, DocumentID: d.DocumentID, Index: d.Index, Text: d.Text, Sequence: d.Sequence, Time: d.Time, Tags: tagsFromAPI(d.Tags)}
}

func factFromAPI(d gql.MemoryFactDetails) Fact {
	return Fact{
		ID:           d.Id,
		Text:         d.Text,
		Class:        string(d.Class),
		Origin:       string(d.Origin),
		State:        string(d.State),
		MergedIntoID: cmdutil.Deref(d.MergedIntoID),
		MergedReason: cmdutil.Deref(d.MergedReason),
		ObservedAt:   d.ObservedAt,
		PeriodStart:  cmdutil.Deref(d.PeriodStart),
		PeriodEnd:    cmdutil.Deref(d.PeriodEnd),
		ChunkID:      cmdutil.Deref(d.ChunkID),
		ScopeID:      cmdutil.Deref(d.ScopeID),
		Tags:         tagsFromAPI(d.Tags),
		EntityIDs:    d.EntityIDs,
	}
}

func observationFromAPI(d gql.MemoryObservationDetails) Observation {
	o := Observation{
		ID:          d.Id,
		Text:        d.Text,
		Topic:       d.Topic,
		Class:       string(d.Class),
		ScopeID:     cmdutil.Deref(d.ScopeID),
		FactIDs:     d.FactIDs,
		PeriodStart: cmdutil.Deref(d.PeriodStart),
		PeriodEnd:   cmdutil.Deref(d.PeriodEnd),
		UpdatedAt:   d.UpdatedAt,
		SaidAt:      cmdutil.Deref(d.SaidAt),
	}
	if d.ScopeTag != nil {
		o.ScopeTag = &MemoryTag{Key: d.ScopeTag.Key, Value: d.ScopeTag.Value}
	}
	return o
}

func documentFields(d Document) []output.Field {
	speaker := ""
	if d.Speaker != nil {
		speaker = d.Speaker.Name + " (" + d.Speaker.Role + ")"
	}
	preview := d.Preview
	if d.PreviewTruncated {
		preview += " …"
	}
	return []output.Field{
		{Label: "ID", Value: d.ID},
		{Label: "Time", Value: d.Time},
		{Label: "Ingested", Value: d.IngestedAt},
		{Label: "Conversation", Value: d.ConversationID},
		{Label: "Sequence", Value: strconv.Itoa(d.Sequence)},
		{Label: "Speaker", Value: speaker},
		{Label: "External id", Value: d.ExternalID},
		{Label: "Media type", Value: d.MediaType},
		{Label: "Tags", Value: formatMemoryTags(d.Tags)},
		{Label: "Preview", Value: preview},
	}
}

func factFields(f Fact) []output.Field {
	return []output.Field{
		{Label: "ID", Value: f.ID},
		{Label: "Text", Value: f.Text},
		{Label: "Class", Value: f.Class},
		{Label: "Origin", Value: f.Origin},
		{Label: "State", Value: f.State},
		{Label: "Merged into", Value: f.MergedIntoID},
		{Label: "Merged because", Value: f.MergedReason},
		{Label: "Observed", Value: f.ObservedAt},
		{Label: "Holds from", Value: f.PeriodStart},
		{Label: "Holds until", Value: f.PeriodEnd},
		{Label: "Chunk", Value: f.ChunkID},
		{Label: "Scope", Value: f.ScopeID},
		{Label: "Tags", Value: formatMemoryTags(f.Tags)},
	}
}

func observationFields(o Observation) []output.Field {
	tag := ""
	if o.ScopeTag != nil {
		tag = o.ScopeTag.Key + "=" + o.ScopeTag.Value
	}
	return []output.Field{
		{Label: "ID", Value: o.ID},
		{Label: "Topic", Value: o.Topic},
		{Label: "Text", Value: o.Text},
		{Label: "Class", Value: o.Class},
		{Label: "Scope", Value: o.ScopeID},
		{Label: "Scope tag", Value: tag},
		{Label: "Facts", Value: strings.Join(o.FactIDs, ", ")},
		{Label: "Holds from", Value: o.PeriodStart},
		{Label: "Holds until", Value: o.PeriodEnd},
		{Label: "Said", Value: o.SaidAt},
		{Label: "Updated", Value: o.UpdatedAt},
	}
}

func chunkFields(c Chunk) []output.Field {
	return []output.Field{
		{Label: "ID", Value: c.ID},
		{Label: "Document", Value: c.DocumentID},
		{Label: "Index", Value: strconv.Itoa(c.Index)},
		{Label: "Time", Value: c.Time},
		{Label: "Tags", Value: formatMemoryTags(c.Tags)},
		{Label: "Text", Value: c.Text},
	}
}

// listFlags are the flags every list of records shares.
type listFlags struct {
	limit   int
	search  string
	tag     string
	orderBy string
	asc     bool
}

func (lf *listFlags) add(cmd *cobra.Command, orderBy string, orders []string) {
	f := cmd.Flags()
	f.IntVar(&lf.limit, "limit", 100, "most results to return; 0 returns all")
	f.StringVar(&lf.search, "search", "", "only records whose text matches")
	f.StringVar(&lf.tag, "tag", "", "only records that carry this tag, as key=value")
	f.StringVar(&lf.orderBy, "order-by", orderBy, "sort by "+strings.Join(orders, " or "))
	f.BoolVar(&lf.asc, "asc", false, "oldest first (default newest first)")
	_ = cmd.RegisterFlagCompletionFunc("order-by", cobra.FixedCompletions(orders, cobra.ShellCompDirectiveNoFileComp))
}

func (lf *listFlags) order() gql.Order {
	if lf.asc {
		return gql.OrderAsc
	}
	return gql.OrderDesc
}

// checkRecordIDs rejects ids that cannot be memory record ids, which are numbers.
func checkRecordIDs(ids ...string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			return cmdutil.UsageError("%q is not a memory record id; record ids are numbers, as the lists show", id)
		}
	}
	return nil
}

// optEnum parses a flag that may be empty, for an optional enum argument.
func optEnum[T ~string](flag, s string, all []T) (*T, error) {
	if s == "" {
		return nil, nil
	}
	v, err := parseEnum("--"+flag, s, all)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func newDocumentsCmd(app *cmdutil.App) *cobra.Command {
	var lf listFlags
	var conversation string
	cmd := &cobra.Command{
		Use:     "documents <store>",
		Aliases: []string{"docs"},
		Short:   "List the documents of a memory store",
		Long: `List the documents of a memory store: the turns of conversations it took in,
and what agents wrote to it. A document shows a preview of its text; read all
of it with partite memory chunks STORE DOCUMENT.`,
		Example: `  partite memory documents support
  partite memory documents support --conversation conv-123 --asc
  partite memory documents support --tag user=bob --search refund -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			store, err := StoreResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			tag, err := parseTagFilter(lf.tag)
			if err != nil {
				return err
			}
			orderBy, err := parseEnum("--order-by", lf.orderBy, gql.AllMemoryDocumentOrderBy)
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			docs, err := cmdutil.ListAll(ctx, lf.limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Document], error) {
				resp, err := gql.MemoryListDocuments(ctx, c, store, first, after, orderBy, lf.order(), cmdutil.OptString(lf.search), cmdutil.OptString(conversation), tag)
				if err != nil {
					return cmdutil.Page[Document]{}, err
				}
				conn := resp.MemoryDocuments
				items := make([]Document, len(conn.Edges))
				for i, e := range conn.Edges {
					items[i] = documentFromAPI(e.Node.MemoryDocumentDetails)
				}
				return cmdutil.Page[Document]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
			})
			if err != nil {
				return err
			}
			rows := make([][]string, len(docs))
			for i, d := range docs {
				speaker := ""
				if d.Speaker != nil {
					speaker = d.Speaker.Name
					if speaker == "" {
						speaker = d.Speaker.Role
					}
				}
				rows[i] = []string{d.ID, cmdutil.FormatTime(d.Time), d.ConversationID, speaker, formatMemoryTags(d.Tags), d.Preview}
			}
			if docs == nil {
				docs = []Document{}
			}
			return app.Printer().List(docs, []string{"id", "time", "conversation", "speaker", "tags", "preview"}, rows)
		},
	}
	lf.add(cmd, "time", []string{"time", "ingested"})
	cmd.Flags().StringVar(&conversation, "conversation", "", "only documents of this conversation")
	return cmd
}

func newFactsCmd(app *cmdutil.App) *cobra.Command {
	var lf listFlags
	var origin, state, document, conversation string
	cmd := &cobra.Command{
		Use:   "facts <store>",
		Short: "List the facts of a memory store",
		Long: `List the facts of a memory store. A fact is one claim, extracted from a
document by a scope or written by an agent. Merged facts, which another fact
replaced, are listed too; pass --state active to leave them out.`,
		Example: `  partite memory facts support
  partite memory facts support --tag user=bob --state active
  partite memory facts support --document 1234 --origin stated`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			store, err := StoreResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			tag, err := parseTagFilter(lf.tag)
			if err != nil {
				return err
			}
			orderBy, err := parseEnum("--order-by", lf.orderBy, gql.AllMemoryFactOrderBy)
			if err != nil {
				return err
			}
			if err := checkRecordIDs(document); err != nil {
				return err
			}
			originV, err := optEnum("origin", origin, gql.AllMemoryOrigin)
			if err != nil {
				return err
			}
			stateV, err := optEnum("state", state, gql.AllMemoryFactState)
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			facts, err := cmdutil.ListAll(ctx, lf.limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Fact], error) {
				resp, err := gql.MemoryListFacts(ctx, c, store, first, after, orderBy, lf.order(), cmdutil.OptString(lf.search), tag, originV, stateV, cmdutil.OptString(document), cmdutil.OptString(conversation))
				if err != nil {
					return cmdutil.Page[Fact]{}, err
				}
				conn := resp.MemoryFacts
				items := make([]Fact, len(conn.Edges))
				for i, e := range conn.Edges {
					items[i] = factFromAPI(e.Node.MemoryFactDetails)
				}
				return cmdutil.Page[Fact]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
			})
			if err != nil {
				return err
			}
			rows := make([][]string, len(facts))
			for i, f := range facts {
				rows[i] = []string{f.ID, cmdutil.FormatTime(f.ObservedAt), f.State, f.Origin, formatMemoryTags(f.Tags), f.Text}
			}
			if facts == nil {
				facts = []Fact{}
			}
			return app.Printer().List(facts, []string{"id", "observed", "state", "origin", "tags", "text"}, rows)
		},
	}
	lf.add(cmd, "observed", []string{"observed", "written"})
	f := cmd.Flags()
	f.StringVar(&origin, "origin", "", "only STATED or INFERRED facts")
	f.StringVar(&state, "state", "", "only ACTIVE or MERGED facts")
	f.StringVar(&document, "document", "", "only facts extracted from this document id")
	f.StringVar(&conversation, "conversation", "", "only facts from this conversation")
	return cmd
}

func newObservationsCmd(app *cmdutil.App) *cobra.Command {
	var lf listFlags
	var class, fact string
	cmd := &cobra.Command{
		Use:     "observations <store>",
		Aliases: []string{"obs"},
		Short:   "List the observations of a memory store",
		Long: `List the observations of a memory store. An observation is a claim about one
topic, built from facts when a scope consolidates. Observations are the long
term memory of a store.`,
		Example: `  partite memory observations support
  partite memory observations support --tag audience=everyone --class world
  partite memory observations support --fact 1234`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			store, err := StoreResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			tag, err := parseTagFilter(lf.tag)
			if err != nil {
				return err
			}
			orderBy, err := parseEnum("--order-by", lf.orderBy, gql.AllMemoryObservationOrderBy)
			if err != nil {
				return err
			}
			if err := checkRecordIDs(fact); err != nil {
				return err
			}
			classV, err := optEnum("class", class, gql.AllMemoryClass)
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			obs, err := cmdutil.ListAll(ctx, lf.limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Observation], error) {
				resp, err := gql.MemoryListObservations(ctx, c, store, first, after, orderBy, lf.order(), cmdutil.OptString(lf.search), tag, classV, cmdutil.OptString(fact))
				if err != nil {
					return cmdutil.Page[Observation]{}, err
				}
				conn := resp.MemoryObservations
				items := make([]Observation, len(conn.Edges))
				for i, e := range conn.Edges {
					items[i] = observationFromAPI(e.Node.MemoryObservationDetails)
				}
				return cmdutil.Page[Observation]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
			})
			if err != nil {
				return err
			}
			rows := make([][]string, len(obs))
			for i, o := range obs {
				tag := ""
				if o.ScopeTag != nil {
					tag = o.ScopeTag.Key + "=" + o.ScopeTag.Value
				}
				rows[i] = []string{o.ID, cmdutil.FormatTime(o.UpdatedAt), tag, strconv.Itoa(len(o.FactIDs)), o.Topic, o.Text}
			}
			if obs == nil {
				obs = []Observation{}
			}
			return app.Printer().List(obs, []string{"id", "updated", "tag", "facts", "topic", "text"}, rows)
		},
	}
	lf.add(cmd, "updated", []string{"updated", "opened"})
	cmd.Flags().StringVar(&class, "class", "", "only WORLD or EXPERIENCE observations")
	cmd.Flags().StringVar(&fact, "fact", "", "only observations built from this fact id")
	return cmd
}

func newChunksCmd(app *cmdutil.App) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "chunks <store> <document-id>",
		Short: "List the chunks of a document, which hold its whole text",
		Long: `List the chunks of a document in order. Chunks are the parts a document's
text was cut into, and what a search finds.`,
		Example: `  partite memory chunks support 1234
  partite memory chunks support 1234 -o json | jq -r '.[].text'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := checkRecordIDs(args[1]); err != nil {
				return err
			}
			store, err := StoreResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			chunks, err := cmdutil.ListAll(ctx, limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Chunk], error) {
				resp, err := gql.MemoryListChunks(ctx, c, store, args[1], first, after)
				if err != nil {
					return cmdutil.Page[Chunk]{}, err
				}
				conn := resp.MemoryDocumentChunks
				items := make([]Chunk, len(conn.Edges))
				for i, e := range conn.Edges {
					items[i] = chunkFromAPI(e.Node.MemoryChunkDetails)
				}
				return cmdutil.Page[Chunk]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
			})
			if err != nil {
				return err
			}
			rows := make([][]string, len(chunks))
			for i, ch := range chunks {
				rows[i] = []string{ch.ID, strconv.Itoa(ch.Index), ch.Text}
			}
			if chunks == nil {
				chunks = []Chunk{}
			}
			return app.Printer().List(chunks, []string{"id", "index", "text"}, rows)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "most chunks to return (default: all)")
	return cmd
}

// recordCmd builds the get and delete verbs for one kind of record.
func recordCmd[T any](app *cmdutil.App, kind, use, plural string,
	get func(ctx context.Context, app *cmdutil.App, store, id string) (*T, error),
	fields func(T) []output.Field,
	del func(ctx context.Context, app *cmdutil.App, store string, ids []string) error,
	deleteNote string,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: fmt.Sprintf("Show or delete one %s of a memory store", kind),
	}
	getCmd := &cobra.Command{
		Use:     "get <store> <id>",
		Aliases: []string{"show"},
		Short:   fmt.Sprintf("Show a %s", kind),
		Example: fmt.Sprintf("  partite memory %s get support 1234", use),
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkRecordIDs(args[1]); err != nil {
				return err
			}
			store, err := StoreResource.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			v, err := get(cmd.Context(), app, store, args[1])
			if err != nil {
				return err
			}
			if v == nil {
				return recordNotFound(kind, args[1], args[0])
			}
			return app.Printer().Object(*v, args[1], fields(*v))
		},
	}
	deleteCmd := &cobra.Command{
		Use:     "delete <store> <id>...",
		Aliases: []string{"rm"},
		Short:   "Delete " + plural,
		Long:    fmt.Sprintf("Delete %s of a memory store. %s", plural, deleteNote),
		Example: fmt.Sprintf("  partite memory %s delete support 1234 --yes", use),
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := StoreResource.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			ids := args[1:]
			if err := checkRecordIDs(ids...); err != nil {
				return err
			}
			what := fmt.Sprintf("%s from memory store %s", cmdutil.Plural(len(ids), kind), args[0])
			if err := app.Confirm(fmt.Sprintf("Delete %s? %s", what, deleteNote)); err != nil {
				return err
			}
			if err := del(cmd.Context(), app, store, ids); err != nil {
				return err
			}
			app.Printer().Done("Deleted %s.", what)
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": ids})
			}
			return nil
		},
	}
	cmd.AddCommand(getCmd, deleteCmd)
	return cmd
}

func newDocumentCmd(app *cmdutil.App) *cobra.Command {
	return recordCmd(app, "document", "document", "documents",
		func(ctx context.Context, app *cmdutil.App, store, id string) (*Document, error) {
			c, err := app.GQL()
			if err != nil {
				return nil, err
			}
			resp, err := gql.MemoryGetDocument(ctx, c, store, id)
			if err != nil || resp.MemoryDocument == nil {
				return nil, err
			}
			d := documentFromAPI(resp.MemoryDocument.MemoryDocumentDetails)
			return &d, nil
		},
		documentFields,
		func(ctx context.Context, app *cmdutil.App, store string, ids []string) error {
			c, err := app.GQL()
			if err != nil {
				return err
			}
			_, err = gql.MemoryDeleteDocuments(ctx, c, store, ids)
			return err
		},
		"Their chunks and the facts extracted from them go too.")
}

func newChunkCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "chunk",
		Short:   "Show one chunk of a memory store",
		Example: "  partite memory chunk get support 1234",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "get <store> <id>",
		Aliases: []string{"show"},
		Short:   "Show a chunk",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := checkRecordIDs(args[1]); err != nil {
				return err
			}
			store, err := StoreResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.MemoryGetChunk(ctx, c, store, args[1])
			if err != nil {
				return err
			}
			if resp.MemoryChunk == nil {
				return recordNotFound("chunk", args[1], args[0])
			}
			ch := chunkFromAPI(resp.MemoryChunk.MemoryChunkDetails)
			return app.Printer().Object(ch, ch.ID, chunkFields(ch))
		},
	})
	return cmd
}

func newFactCmd(app *cmdutil.App) *cobra.Command {
	return recordCmd(app, "fact", "fact", "facts",
		func(ctx context.Context, app *cmdutil.App, store, id string) (*Fact, error) {
			c, err := app.GQL()
			if err != nil {
				return nil, err
			}
			resp, err := gql.MemoryGetFact(ctx, c, store, id)
			if err != nil || resp.MemoryFact == nil {
				return nil, err
			}
			f := factFromAPI(resp.MemoryFact.MemoryFactDetails)
			return &f, nil
		},
		factFields,
		func(ctx context.Context, app *cmdutil.App, store string, ids []string) error {
			c, err := app.GQL()
			if err != nil {
				return err
			}
			_, err = gql.MemoryDeleteFacts(ctx, c, store, ids)
			return err
		},
		"The observations built from them are written again.")
}

func newObservationCmd(app *cmdutil.App) *cobra.Command {
	return recordCmd(app, "observation", "observation", "observations",
		func(ctx context.Context, app *cmdutil.App, store, id string) (*Observation, error) {
			c, err := app.GQL()
			if err != nil {
				return nil, err
			}
			resp, err := gql.MemoryGetObservation(ctx, c, store, id)
			if err != nil || resp.MemoryObservation == nil {
				return nil, err
			}
			o := observationFromAPI(resp.MemoryObservation.MemoryObservationDetails)
			return &o, nil
		},
		observationFields,
		func(ctx context.Context, app *cmdutil.App, store string, ids []string) error {
			c, err := app.GQL()
			if err != nil {
				return err
			}
			_, err = gql.MemoryDeleteObservations(ctx, c, store, ids)
			return err
		},
		"Their facts go back to be consolidated again.")
}
