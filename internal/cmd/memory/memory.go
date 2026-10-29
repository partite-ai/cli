// Package memory manages memory stores, memory interaction profiles and what
// the stores hold.
package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/spf13/cobra"
)

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage memory stores, memory profiles and what memory holds",
		Long: `Memory lets agents remember across conversations.

A memory store holds documents (the turns of conversations, or what agents
write), the facts extracted from them, and the observations built from those
facts. Scopes decide which documents a store processes and what tag the
records they produce carry. A mesh reads and writes one store.

A memory profile (memory interaction profile) states how one agent uses memory:
what is put in its prompt, and which tools it has to search or write memory.

  partite memory store ...        stores and their settings
  partite memory scope ...        the scopes of one store
  partite memory profile ...      memory interaction profiles
  partite memory documents STORE  what a store holds, and recall to search it`,
	}
	cmd.AddCommand(
		newStoreCmd(app),
		newScopeCmd(app),
		newProfileCmd(app),
		newDocumentsCmd(app),
		newDocumentCmd(app),
		newChunksCmd(app),
		newChunkCmd(app),
		newFactsCmd(app),
		newFactCmd(app),
		newObservationsCmd(app),
		newObservationCmd(app),
		newRecallCmd(app),
	)
	return cmd
}

// parseEnum reads a flag or spec value into one of the server's enum values.
// Case and dashes do not matter, so "keys-from-request" reads as KEYS_FROM_REQUEST.
func parseEnum[T ~string](what, s string, all []T) (T, error) {
	want := strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
	for _, v := range all {
		if string(v) == want {
			return v, nil
		}
	}
	return "", cmdutil.UsageError("%s must be one of %s, got %q", what, enumList(all), s)
}

func enumList[T ~string](all []T) string {
	names := make([]string, len(all))
	for i, v := range all {
		names[i] = string(v)
	}
	return strings.Join(names, ", ")
}

// MemoryTag is a key and a value that a memory record carries.
type MemoryTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func formatMemoryTags(tags []MemoryTag) string {
	parts := make([]string, len(tags))
	for i, t := range tags {
		parts[i] = t.Key + "=" + t.Value
	}
	return strings.Join(parts, ", ")
}

// parseTagFilter reads a --tag flag in key=value form. An empty flag is no filter.
func parseTagFilter(s string) (*gql.MemoryTagInput, error) {
	if s == "" {
		return nil, nil
	}
	key, value, ok := strings.Cut(s, "=")
	if !ok || key == "" {
		return nil, cmdutil.UsageError("--tag must be key=value, got %q", s)
	}
	return &gql.MemoryTagInput{Key: key, Value: value}, nil
}

// resolveEmbeddingProfile turns an embedding profile name or id into an id.
func resolveEmbeddingProfile(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	return cmdutil.Resolve(ctx, ref, "cemp", "embedding profile", "embedding-profile", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[cmdutil.Named], error) {
			resp, err := gql.MemoryFindEmbeddingProfiles(ctx, c, first, after, &name)
			if err != nil {
				return cmdutil.Page[cmdutil.Named]{}, err
			}
			conn := resp.EmbeddingProfileSummaries
			items := make([]cmdutil.Named, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = cmdutil.Named{ID: e.Node.Id, Name: e.Node.Name}
			}
			return cmdutil.Page[cmdutil.Named]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	})
}

// resolveModelProfile turns a model profile name, provider/name or id into an id.
func resolveModelProfile(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	provider, name, qualified := strings.Cut(ref, "/")
	if !qualified {
		name = ref
	}
	return cmdutil.Resolve(ctx, ref, "cmpr", "model profile", "model-profile", func(ctx context.Context, _ string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[cmdutil.Named], error) {
			resp, err := gql.MemoryFindModelProfiles(ctx, c, first, after, &name)
			if err != nil {
				return cmdutil.Page[cmdutil.Named]{}, err
			}
			conn := resp.ModelProfileSummaries
			var items []cmdutil.Named
			for _, e := range conn.Edges {
				if qualified && e.Node.ProviderName != provider {
					continue
				}
				// Name is ref so that Resolve's exact match sees provider/name.
				n := e.Node.Name
				if qualified {
					n = e.Node.ProviderName + "/" + e.Node.Name
				}
				items = append(items, cmdutil.Named{ID: e.Node.Id, Name: n})
			}
			return cmdutil.Page[cmdutil.Named]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	})
}

// optRef resolves ref with resolve, keeping "" as "".
func optRef(ctx context.Context, app *cmdutil.App, ref string, resolve func(context.Context, *cmdutil.App, string) (string, error)) (string, error) {
	if ref == "" {
		return "", nil
	}
	return resolve(ctx, app, ref)
}

// clearable is a PATCH value for a field that "" clears.
func clearable(s string) gql.Optional[string] {
	if s == "" {
		return gql.Optional[string]{Set: true, Nil: true}
	}
	return gql.Opt(s)
}

func recordNotFound(kind, id, store string) error {
	return &cmdutil.Error{Code: cmdutil.ExitNotFound, Msg: fmt.Sprintf("memory store %s holds no %s %q", store, kind, id)}
}
