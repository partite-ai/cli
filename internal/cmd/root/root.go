// Package root builds the partite command tree.
package root

import (
	"github.com/partite-ai/partite-cli/internal/cmd/authcmd"
	"github.com/partite-ai/partite-cli/internal/cmd/contextcmd"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

const longHelp = `Manage Partite from the command line.

Commands take a resource and a verb: partite <resource> <verb> [args].
Anywhere a command takes an id, it also takes the resource's name.

Output is a table at a terminal and JSON otherwise. Use -o json, -o yaml or
-o id to choose. Errors go to stderr; with -o json they are JSON too.

Exit codes:
  0  success
  1  error
  2  bad usage, or a confirmation is needed (pass --yes)
  3  not logged in, or the login expired
  4  permission denied
  5  not found
  6  conflict
  7  the mesh request failed
  8  the mesh request was cancelled
  9  the mesh request is waiting for a task (see partite conversation task)
  10 the mesh request was still running when --timeout passed
  130 cancelled

Credentials, first match wins:
  PARTITE_API_TOKEN                      an API key token
  PARTITE_TOKEN                          an OAuth access token
  the context's stored login or API key  see partite login, partite context

Other environment variables:
  PARTITE_CONTEXT     context to use, like --context
  PARTITE_URL         server URL, overriding the context's
  PARTITE_CONFIG_DIR  where config.yaml lives (default ~/.config/partite)
  PARTITE_NO_KEYRING  store secrets in a 0600 file, not the OS keychain`

// Group builds a top-level command.
type Group func(app *cmdutil.App) *cobra.Command

// New builds the root command with the given groups. main passes All.
func New(app *cmdutil.App, groups ...Group) *cobra.Command {
	// Commands that talk to a mesh have their own pre-run hooks, which run
	// after the root's.
	cobra.EnableTraverseRunHooks = true
	root := &cobra.Command{
		Use:           "partite",
		Short:         "Manage Partite from the command line",
		Long:          longHelp,
		Version:       cmdutil.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return app.Init()
		},
	}
	root.SetIn(app.In)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &cmdutil.Error{Code: cmdutil.ExitUsage, Msg: err.Error(), Hint: "See: " + cmd.CommandPath() + " --help"}
	})

	pf := root.PersistentFlags()
	pf.StringVar(&app.ContextFlag, "context", "", "context (server) to use; see partite context list")
	pf.StringVarP(&app.OutputFlag, "output", "o", "", "output format: table, json, yaml or id")
	pf.BoolVarP(&app.Yes, "yes", "y", false, "do not ask for confirmation")
	pf.BoolVar(&app.NoInput, "no-input", false, "never prompt; fail instead")

	root.AddGroup(
		&cobra.Group{ID: "config", Title: "Configuration:"},
		&cobra.Group{ID: "runtime", Title: "Running meshes:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
	)
	root.SetHelpCommandGroupID("setup")
	root.SetCompletionCommandGroupID("setup")

	for _, c := range authcmd.New(app) {
		c.GroupID = "setup"
		root.AddCommand(c)
	}
	ctxCmd := contextcmd.New(app)
	ctxCmd.GroupID = "setup"
	root.AddCommand(ctxCmd)

	for _, g := range groups {
		c := g(app)
		if c.GroupID == "" {
			c.GroupID = "config"
		}
		root.AddCommand(c)
	}
	return root
}
