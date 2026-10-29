package root

import (
	"regexp"
	"strings"
	"testing"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

var mention = regexp.MustCompile(`partite((?: [a-z][a-z-]*)+)`)

// Help text and examples name other commands. This checks that each one
// exists, so a renamed command does not leave stale help behind.
func TestHelpNamesOnlyCommandsThatExist(t *testing.T) {
	root := New(cmdutil.NewApp(), All...)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, text := range []string{c.Short, c.Long, c.Example} {
			for _, m := range mention.FindAllStringSubmatch(text, -1) {
				words := strings.Fields(m[1])
				for i, w := range words {
					if w == "and" || w == "or" {
						words = words[:i]
						break
					}
				}
				found, rest, err := root.Find(words)
				if err != nil || found == root {
					t.Errorf("%s: help names %q, which is not a command", c.CommandPath(), "partite"+m[1])
					continue
				}
				if found.HasSubCommands() && len(rest) > 0 && !found.Runnable() {
					t.Errorf("%s: help names %q, but %s has no subcommand %q", c.CommandPath(), "partite"+m[1], found.CommandPath(), rest[0])
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// Every command must say what it does.
func TestEveryCommandHasAShortDescription(t *testing.T) {
	root := New(cmdutil.NewApp(), All...)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Short == "" && !c.Hidden {
			t.Errorf("%s has no Short", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
