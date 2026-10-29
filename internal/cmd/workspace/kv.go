package workspace

import (
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

func newKV(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kv",
		Short: "Read and write a workspace's key-values",
		Long: `Each workspace has a key-value store. Particles keep their state in the
workspace set as their kv_workspace_id.`,
	}
	cmd.AddCommand(newKVList(app), newKVGet(app), newKVSet(app), newKVDelete(app))
	return cmd
}

func newKVList(app *cmdutil.App) *cobra.Command {
	var conversation, prefix string
	cmd := &cobra.Command{
		Use:     "list <workspace>",
		Aliases: []string{"ls"},
		Short:   "List the keys in a workspace",
		Example: "  partite workspace kv list notes\n  partite workspace kv list notes --prefix user/",
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
			resp, err := gql.ToolsWorkspaceKeyNames(ctx, c, id, cmdutil.OptString(conversation), prefix)
			if err != nil {
				return err
			}
			keys := resp.WorkspaceKeyNames
			if keys == nil {
				keys = []string{}
			}
			rows := make([][]string, len(keys))
			for i, k := range keys {
				rows[i] = []string{k}
			}
			return app.Printer().List(keys, []string{"key"}, rows)
		},
	}
	addConversationFlag(cmd, &conversation)
	cmd.Flags().StringVar(&prefix, "prefix", "", "only list keys that start with this")
	return cmd
}

func newKVGet(app *cmdutil.App) *cobra.Command {
	var conversation string
	cmd := &cobra.Command{
		Use:   "get <workspace> <key>",
		Short: "Print the value of a key",
		Long: `Print the value of a key. The table format prints the value as is; json and
yaml print {"key": ..., "value": ...}.`,
		Example: "  partite workspace kv get notes user/42",
		Args:    cobra.ExactArgs(2),
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
			resp, err := gql.ToolsWorkspaceKeyValue(ctx, c, id, cmdutil.OptString(conversation), args[1])
			if err != nil {
				return err
			}
			if app.Format() == output.Table || app.Format() == output.ID {
				fmt.Fprint(app.Out, resp.WorkspaceKeyValue)
				if !strings.HasSuffix(resp.WorkspaceKeyValue, "\n") {
					fmt.Fprintln(app.Out)
				}
				return nil
			}
			return app.Printer().Data(map[string]string{"key": args[1], "value": resp.WorkspaceKeyValue})
		},
	}
	addConversationFlag(cmd, &conversation)
	return cmd
}

func newKVSet(app *cmdutil.App) *cobra.Command {
	var conversation string
	cmd := &cobra.Command{
		Use:   "set <workspace> <key> <value>",
		Short: "Set the value of a key",
		Long:  "Set the value of a key, replacing any value it had. A value of @file reads a file and @- reads standard input.",
		Example: `  partite workspace kv set notes greeting hello
  partite workspace kv set notes config @config.json`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			value, err := app.ReadValue(args[2])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsSetWorkspaceKeyValue(ctx, c, id, cmdutil.OptString(conversation), args[1], value)
			if err != nil {
				return err
			}
			r := resp.SetWorkspaceKeyValue
			if err := operationError(r.Success, r.Message, "could not set "+args[1]); err != nil {
				return err
			}
			app.Printer().Done("Set %s.", args[1])
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]string{"key": args[1], "value": value})
			}
			return nil
		},
	}
	addConversationFlag(cmd, &conversation)
	return cmd
}

func newKVDelete(app *cmdutil.App) *cobra.Command {
	var conversation string
	cmd := &cobra.Command{
		Use:     "delete <workspace> <key>...",
		Aliases: []string{"rm"},
		Short:   "Delete keys",
		Long:    "Delete keys. Deleting a key that does not exist is not an error.",
		Example: "  partite workspace kv delete notes greeting --yes",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			keys := args[1:]
			if err := app.Confirm(fmt.Sprintf("Delete %s from workspace %s?", strings.Join(keys, ", "), args[0])); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			for _, k := range keys {
				resp, err := gql.ToolsDeleteWorkspaceKeyValue(ctx, c, id, cmdutil.OptString(conversation), k)
				if err != nil {
					return err
				}
				r := resp.DeleteWorkspaceKeyValue
				if err := operationError(r.Success, r.Message, "could not delete "+k); err != nil {
					return err
				}
			}
			app.Printer().Done("Deleted %s.", strings.Join(keys, ", "))
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": keys})
			}
			return nil
		},
	}
	addConversationFlag(cmd, &conversation)
	return cmd
}
