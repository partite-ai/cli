// Package eval manages evaluation: judge metrics, computed metrics, rubrics,
// monitors that score a mesh's conversations, and the evaluations they write.
// It needs an enterprise server.
package eval

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "eval",
		Aliases: []string{"evals"},
		Short:   "Score conversations with metrics, rubrics and monitors (enterprise)",
		Long: `Evaluation scores a mesh's conversations as they happen. It needs an
enterprise server.

The pieces fit together like this:

  judge-metric     a question a model answers about a conversation, such as
                   "did the agent answer what was asked?", as a rating
  computed-metric  a number Partite measures itself, such as latency or cost
  rubric           a set of judge and computed metrics, each with a weight,
                   that add up to one score
  monitor          scores a share of one mesh's conversations against a rubric
  evaluation       one conversation request scored against a rubric
  trace-run        an on-demand evaluation of a trace, with the
                   evaluations it made
  report           scores of a mesh against a rubric over time, by version

A typical setup:

  partite eval judge-metric list
  partite eval rubric create support-quality --judge-metric Helpfulness --computed-metric latency_ms
  partite eval monitor create --mesh support --rubric support-quality --model-set judges --sample-rate 0.1
  partite eval report support support-quality --since 7d`,
	}
	cmd.AddCommand(
		newJudgeMetric(app),
		newComputedMetric(app),
		newRubric(app),
		newMonitor(app),
		newEvaluation(app),
		newTraceRun(app),
		newReport(app),
	)
	return cmd
}

// formatScore prints a score or value for tables, or "-" when there is none.
func formatScore(v *float64) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// formatRatio prints a 0..1 ratio as a percentage.
func formatRatio(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", *v*100)
}

// parseSince reads a duration such as 90m, 24h or 7d.
func parseSince(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil || n <= 0 {
			return 0, cmdutil.UsageError("--since must be a duration such as 30m, 24h or 7d, got %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, cmdutil.UsageError("--since must be a duration such as 30m, 24h or 7d, got %q", s)
	}
	return d, nil
}

// upperChoice upper-cases an enum value given on the command line and checks
// it is one of choices. flag names the flag or field in the error.
func upperChoice[T ~string](flag, value string, choices []T) (T, error) {
	v := T(strings.ToUpper(strings.TrimSpace(value)))
	for _, c := range choices {
		if c == v {
			return v, nil
		}
	}
	names := make([]string, len(choices))
	for i, c := range choices {
		names[i] = string(c)
	}
	return "", cmdutil.UsageError("%s must be one of %s, got %q", flag, strings.Join(names, ", "), value)
}

func choiceNames[T ~string](choices []T) []string {
	names := make([]string, len(choices))
	for i, c := range choices {
		names[i] = string(c)
	}
	return names
}
