package memory

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

type RecallRecord struct {
	ID           string      `json:"id"`
	Type         string      `json:"type"`
	Text         string      `json:"text"`
	Topic        string      `json:"topic,omitempty"`
	Score        float64     `json:"score"`
	Tokens       int         `json:"tokens"`
	TextRank     int         `json:"text_rank"`
	MeaningRank  int         `json:"meaning_rank"`
	TextScore    float64     `json:"text_score"`
	MeaningScore float64     `json:"meaning_score"`
	Recent       int         `json:"recent"`
	FactCount    int         `json:"fact_count"`
	Tags         []MemoryTag `json:"tags"`
}

type RecallTiming struct {
	Total   int `json:"total"`
	Embed   int `json:"embed"`
	Meaning int `json:"meaning"`
	Text    int `json:"text"`
	Read    int `json:"read"`
}

type Recall struct {
	Weighed       int            `json:"weighed"`
	Budget        int            `json:"budget"`
	Spent         int            `json:"spent"`
	Depth         int            `json:"depth"`
	TextFound     int            `json:"text_found"`
	MeaningFound  int            `json:"meaning_found"`
	MinSimilarity float64        `json:"min_similarity"`
	EmbedError    string         `json:"embed_error,omitempty"`
	TookMS        RecallTiming   `json:"took_ms"`
	Records       []RecallRecord `json:"records"`
	Dropped       []RecallRecord `json:"dropped"`
}

func recallRecordFromAPI(d gql.MemoryRecallRecordDetails) RecallRecord {
	return RecallRecord{
		ID:           d.Id,
		Type:         string(d.Type),
		Text:         d.Text,
		Topic:        cmdutil.Deref(d.Topic),
		Score:        d.Score,
		Tokens:       d.Tokens,
		TextRank:     d.TextRank,
		MeaningRank:  d.MeaningRank,
		TextScore:    d.TextScore,
		MeaningScore: d.MeaningScore,
		Recent:       d.Recent,
		FactCount:    d.FactCount,
		Tags:         tagsFromAPI(d.Tags),
	}
}

func newRecallCmd(app *cmdutil.App) *cobra.Command {
	var conversation, tag string
	var types []string
	var budget int
	var omitSources, showDropped bool
	var minSimilarity float64
	cmd := &cobra.Command{
		Use:   "recall <store> [query]",
		Short: "Search a memory store the way an agent's memory does",
		Long: `Search a memory store and show what came back, and why. It runs the same
search an agent's memory does, by words and, where the store has an embedding
profile, by meaning. It reads the store and changes nothing.

Each record shows its score, its rank in the search over words (text) and the
search over meaning, and its cosine similarity to the query. A record the
search found but the token budget left out is dropped; show those with
--dropped. A record in neither list did not match, or was filtered out first.

With no query, recall reads the newest turns of the --conversation given.`,
		Example: `  partite memory recall support "refund policy"
  partite memory recall support "what does bob like" --tag user=bob --type fact --type observation
  partite memory recall support "pricing" --min-similarity -1 --dropped
  partite memory recall support --conversation conv-123`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			store, err := StoreResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			var query *string
			if len(args) == 2 {
				query = &args[1]
			}
			if query == nil && conversation == "" {
				return cmdutil.UsageError("give a query, or --conversation to read its newest turns")
			}
			tagV, err := parseTagFilter(tag)
			if err != nil {
				return err
			}
			var recordTypes []gql.MemoryRecordType
			for _, t := range types {
				v, err := parseEnum("--type", t, gql.AllMemoryRecordType)
				if err != nil {
					return err
				}
				recordTypes = append(recordTypes, v)
			}
			var minSim *float64
			if cmd.Flags().Changed("min-similarity") {
				minSim = &minSimilarity
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.MemoryRecall(ctx, c, store, query, cmdutil.OptString(conversation), recordTypes, tagV, budget, omitSources, minSim)
			if err != nil {
				return err
			}
			r := resp.MemoryRecall
			out := Recall{
				Weighed:       r.Weighed,
				Budget:        r.Budget,
				Spent:         r.Spent,
				Depth:         r.Depth,
				TextFound:     r.TextFound,
				MeaningFound:  r.MeaningFound,
				MinSimilarity: r.MinSimilarity,
				EmbedError:    cmdutil.Deref(r.EmbedError),
				TookMS:        RecallTiming{Total: r.Took.Total, Embed: r.Took.Embed, Meaning: r.Took.Meaning, Text: r.Took.Text, Read: r.Took.Read},
				Records:       make([]RecallRecord, len(r.Records)),
				Dropped:       make([]RecallRecord, len(r.Dropped)),
			}
			for i, rec := range r.Records {
				out.Records[i] = recallRecordFromAPI(rec.MemoryRecallRecordDetails)
			}
			for i, rec := range r.Dropped {
				out.Dropped[i] = recallRecordFromAPI(rec.MemoryRecallRecordDetails)
			}
			return printRecall(app, out, showDropped)
		},
	}
	f := cmd.Flags()
	f.StringVar(&conversation, "conversation", "", "search the chunks of this conversation only")
	f.StringSliceVar(&types, "type", nil, "record types to search: chunk, fact or observation; repeat for more (default: all)")
	f.StringVar(&tag, "tag", "", "only records that carry this tag, as key=value")
	f.IntVar(&budget, "budget", 2000, "tokens the records that come back may use")
	f.BoolVar(&omitSources, "omit-sources", false, "leave out a record that another record in the answer was made from, as context injections do")
	f.Float64Var(&minSimilarity, "min-similarity", 0, "how near a record must be to the query to come back from the search over meaning, from -1 to 1 (default: the engine's floor)")
	f.BoolVar(&showDropped, "dropped", false, "also list the records the budget left out")
	_ = cmd.RegisterFlagCompletionFunc("type", cobra.FixedCompletions([]string{"chunk", "fact", "observation"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func printRecall(app *cmdutil.App, r Recall, showDropped bool) error {
	if app.Format() != output.Table {
		if app.Format() == output.ID {
			for _, rec := range r.Records {
				fmt.Fprintln(app.Out, rec.ID)
			}
			return nil
		}
		return app.Printer().Data(r)
	}
	w := app.Out
	fmt.Fprintf(w, "%s, %d of %d tokens. Weighed %d; words found %d, meaning found %d (min similarity %.2f). Took %dms.\n",
		cmdutil.Plural(len(r.Records), "record"), r.Spent, r.Budget, r.Weighed, r.TextFound, r.MeaningFound, r.MinSimilarity, r.TookMS.Total)
	if r.EmbedError != "" {
		fmt.Fprintf(w, "The query has no vector, so only words were searched: %s\n", r.EmbedError)
	}
	fmt.Fprintln(w)
	if err := printRecallRecords(app, r.Records); err != nil {
		return err
	}
	if !showDropped {
		if len(r.Dropped) > 0 {
			fmt.Fprintf(w, "\n%s left out by the budget; see them with --dropped.\n", cmdutil.Plural(len(r.Dropped), "record"))
		}
		return nil
	}
	fmt.Fprintf(w, "\nLeft out by the budget (%d):\n", len(r.Dropped))
	return printRecallRecords(app, r.Dropped)
}

func printRecallRecords(app *cmdutil.App, records []RecallRecord) error {
	rows := make([][]string, len(records))
	for i, rec := range records {
		text := rec.Text
		if rec.Topic != "" {
			text = rec.Topic + ": " + text
		}
		rows[i] = []string{rec.ID, strings.ToLower(rec.Type), fmt.Sprintf("%.3f", rec.Score), strconv.Itoa(rec.Tokens),
			rank(rec.TextRank), rank(rec.MeaningRank), fmt.Sprintf("%.3f", rec.MeaningScore), formatMemoryTags(rec.Tags), text}
	}
	return app.Printer().List(records, []string{"id", "type", "score", "tokens", "text#", "meaning#", "similarity", "tags", "text"}, rows)
}

func rank(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}
