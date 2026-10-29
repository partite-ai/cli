// Package export writes resources out as terraform for the partiteai provider.
package export

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/accesspoint"
	"github.com/partite-ai/partite-cli/internal/cmd/agent"
	"github.com/partite-ai/partite-cli/internal/cmd/apikey"
	"github.com/partite-ai/partite-cli/internal/cmd/boundary"
	"github.com/partite-ai/partite-cli/internal/cmd/clienttaskdef"
	"github.com/partite-ai/partite-cli/internal/cmd/mcp"
	"github.com/partite-ai/partite-cli/internal/cmd/memory"
	"github.com/partite-ai/partite-cli/internal/cmd/mesh"
	"github.com/partite-ai/partite-cli/internal/cmd/modelprovider"
	"github.com/partite-ai/partite-cli/internal/cmd/modelset"
	"github.com/partite-ai/partite-cli/internal/cmd/particle"
	"github.com/partite-ai/partite-cli/internal/cmd/skill"
	"github.com/partite-ai/partite-cli/internal/cmd/webhook"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

type kind struct {
	name    string // the CLI's word for it
	path    string // the /tfexport/ path segment
	resolve func(ctx context.Context, app *cmdutil.App, ref string) (string, error)
	// includes are what else can be exported with it, as the server's
	// include_ query parameters without the prefix.
	includes []string
}

func idOnly(prefix, what string) func(context.Context, *cmdutil.App, string) (string, error) {
	return func(_ context.Context, _ *cmdutil.App, ref string) (string, error) {
		if !cmdutil.IsID(ref, prefix) {
			return "", cmdutil.UsageError("give the %s's id (%s_...)", what, prefix)
		}
		return ref, nil
	}
}

var kinds = []kind{
	{"mesh", "mesh", mesh.Meshes.Resolve, []string{"agents", "model_sets", "model_providers", "mcp_connections", "installed_particles", "task_definitions", "skills", "memory", "boundaries"}},
	{"agent", "agent", agent.Resource.Resolve, []string{"mcp_connections", "installed_particles", "task_definitions", "skills", "memory"}},
	{"model-set", "modelset", modelset.Sets.Resolve, []string{"model_providers"}},
	{"model-provider", "modelprovider", modelprovider.Providers.Resolve, nil},
	{"api-key", "apikey", apikey.Resource.Resolve, nil},
	{"mcp", "mcpconnection", mcp.Resource.Resolve, nil},
	{"webhook", "webhookdestination", webhook.Resource.Resolve, []string{"mesh"}},
	{"particle-artifact", "particleartifact", idOnly("cpar", "particle artifact"), nil},
	{"particle", "installedparticle", particle.Resource.Resolve, []string{"artifact"}},
	{"task-definition", "taskdefinition", clienttaskdef.Resource.Resolve, nil},
	{"skill", "skill", skill.Resource.Resolve, nil},
	{"memory-store", "memorystore", memory.StoreResource.Resolve, []string{"model_providers"}},
	{"memory-profile", "memoryinteractionprofile", memory.ProfileResource.Resolve, nil},
	{"boundary", "boundary", boundary.Boundaries.Resolve, nil},
	{"access-point", "accesspoint", accesspoint.AccessPoints.Resolve, []string{"boundaries"}},
}

func New(app *cmdutil.App) *cobra.Command {
	var names []string
	for _, k := range kinds {
		names = append(names, k.name)
	}
	var include []string
	var all, importBlocks bool
	var outFile string
	cmd := &cobra.Command{
		Use:   "export <kind> <name-or-id>",
		Short: "Write a resource out as terraform (enterprise)",
		Long: `Write a resource, and optionally what it uses, as terraform for the
partiteai provider. Use it to move configuration made by hand or with this
CLI into terraform.

Kinds: ` + strings.Join(names, ", ") + `

--include adds what the resource uses. What each kind can include:
` + includeHelp() + `

--import-blocks adds import blocks, so that terraform adopts the existing
resources instead of creating new ones.`,
		Example: `  partite export mesh support-bot --all --import-blocks -O support-bot.tf
  partite export agent triage --include skills,task_definitions
  partite export model-provider anthropic`,
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return names, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !app.Enterprise() {
				return &cmdutil.Error{
					Code: cmdutil.ExitUsage,
					Msg:  fmt.Sprintf("export needs an enterprise server, and %s is not one", app.Context.URL),
					Hint: fmt.Sprintf("If it is, run: partite context set %s --enterprise=true", app.ContextName),
				}
			}
			i := slices.IndexFunc(kinds, func(k kind) bool { return k.name == args[0] })
			if i < 0 {
				return cmdutil.UsageError("unknown kind %q; use one of %s", args[0], strings.Join(names, ", "))
			}
			k := kinds[i]
			q := url.Values{}
			wanted := include
			if all {
				wanted = k.includes
			}
			for _, inc := range wanted {
				inc = strings.ReplaceAll(inc, "-", "_")
				if !slices.Contains(k.includes, inc) {
					return cmdutil.UsageError("a %s export cannot include %q; it can include %s", k.name, inc, orNothing(k.includes))
				}
				q.Set("include_"+inc, "true")
			}
			if importBlocks {
				q.Set("include_import_blocks", "true")
			}

			id, err := k.resolve(cmd.Context(), app, args[1])
			if err != nil {
				return err
			}
			rest, err := app.REST()
			if err != nil {
				return err
			}
			path := "/tfexport/" + k.path + "/" + url.PathEscape(id)
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			resp, err := rest.Do(cmd.Context(), http.MethodGet, path, nil, nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			out := app.Out
			if outFile != "" && outFile != "-" {
				f, err := os.Create(outFile)
				if err != nil {
					return err
				}
				defer f.Close()
				out = f
			}
			if _, err := io.Copy(out, resp.Body); err != nil {
				return err
			}
			if outFile != "" && outFile != "-" {
				fmt.Fprintf(app.Err, "Wrote %s.\n", outFile)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&include, "include", nil, "also export what the resource uses; see above")
	cmd.Flags().BoolVar(&all, "all", false, "include everything the kind can include")
	cmd.Flags().BoolVar(&importBlocks, "import-blocks", false, "add import blocks for the existing resources")
	cmd.Flags().StringVarP(&outFile, "out", "O", "", "write to this file instead of standard output")
	cmd.MarkFlagsMutuallyExclusive("include", "all")
	return cmd
}

func includeHelp() string {
	var b strings.Builder
	for _, k := range kinds {
		if len(k.includes) > 0 {
			fmt.Fprintf(&b, "  %-16s %s\n", k.name, strings.Join(k.includes, ", "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func orNothing(s []string) string {
	if len(s) == 0 {
		return "nothing"
	}
	return strings.Join(s, ", ")
}
