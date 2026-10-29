// Package contextcmd manages contexts: named servers and how to log in to them.
package contextcmd

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/partite-ai/partite-cli/internal/config"
	"github.com/partite-ai/partite-cli/internal/credstore"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"ctx"},
		Short:   "Choose which Partite server to talk to",
		Long: `A context is a named server and how to log in to it. Two are built in:

  cloud  https://api.partite.ai  (log in with partite login)
  local  http://localhost:8080   (a local mesh; no login)

Other servers take an API key.

Pick one for a single command with --context or PARTITE_CONTEXT.`,
	}
	cmd.AddCommand(newList(app), newUse(app), newShow(app), newSet(app), newDelete(app))
	return cmd
}

type contextView struct {
	Name       string `json:"name"`
	Current    bool   `json:"current"`
	URL        string `json:"url"`
	Auth       string `json:"auth"`
	Org        string `json:"org,omitempty"`
	APIKeyID   string `json:"api_key_id,omitempty"`
	Enterprise *bool  `json:"enterprise,omitempty"`
	WebURL     string `json:"web_url,omitempty"`
	LoggedIn   bool   `json:"logged_in"`
}

func view(app *cmdutil.App, name string) contextView {
	c := app.Config.Contexts[name]
	v := contextView{Name: name, Current: name == app.ContextName, URL: c.URL, Auth: string(c.Auth), Org: c.Org, APIKeyID: c.APIKeyID, Enterprise: c.Enterprise, WebURL: c.WebURL}
	if c.Auth == config.AuthNone {
		v.LoggedIn = true
	} else if creds, err := credstore.Get(name); err == nil && creds != nil {
		v.LoggedIn = creds.AccessToken != "" || creds.APIToken != "" || creds.APIKeySecret != ""
	}
	return v
}

func newList(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List contexts",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var views []contextView
			var rows [][]string
			for _, name := range app.Config.Names() {
				v := view(app, name)
				views = append(views, v)
				mark := ""
				if v.Current {
					mark = "*"
				}
				rows = append(rows, []string{name, mark, v.URL, v.Auth, yesNo(v.LoggedIn)})
			}
			return app.Printer().List(views, []string{"name", "current", "url", "auth", "logged in"}, rows)
		},
	}
}

func newUse(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:               "use <name>",
		Short:             "Make a context the default",
		Example:           "  partite context use local",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := app.Config.Contexts[args[0]]; !ok {
				return cmdutil.Errorf(cmdutil.ExitNotFound, "List contexts with: partite context list", "there is no context named %q", args[0])
			}
			app.Config.Current = args[0]
			if err := app.Config.Save(); err != nil {
				return err
			}
			fmt.Fprintf(app.Err, "Now using %s (%s).\n", args[0], app.Config.Contexts[args[0]].URL)
			return nil
		},
	}
}

func newShow(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:               "show [name]",
		Short:             "Show a context (default: the current one)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeNames(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := app.ContextName
			if len(args) == 1 {
				name = args[0]
			}
			if _, ok := app.Config.Contexts[name]; !ok {
				return cmdutil.Errorf(cmdutil.ExitNotFound, "List contexts with: partite context list", "there is no context named %q", name)
			}
			v := view(app, name)
			ent := ""
			if v.Enterprise != nil {
				ent = strconv.FormatBool(*v.Enterprise)
			}
			return app.Printer().Object(v, v.Name, []output.Field{
				{Label: "Name", Value: v.Name},
				{Label: "URL", Value: v.URL},
				{Label: "Auth", Value: v.Auth},
				{Label: "Org", Value: v.Org},
				{Label: "API key", Value: v.APIKeyID},
				{Label: "Enterprise", Value: ent},
				{Label: "Web app", Value: v.WebURL},
				{Label: "Logged in", Value: yesNo(v.LoggedIn)},
			})
		},
	}
}

func newSet(app *cmdutil.App) *cobra.Command {
	var rawURL, authMethod, org, enterprise, webURL string
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Create or change a context",
		Long: `Create a context, or change one. Only the flags you pass are changed.

--auth is one of:
  login    log in with partite login; Partite cloud only
  api-key  use an API key stored with partite login --with-api-key (the
           default for a new context)
  none     send no credentials, for a local mesh without REQUIRE_AUTH`,
		Example: `  partite context set acme --url https://partite.acme.example.com
  partite context set local2 --url http://localhost:9090 --auth none --enterprise=false
  partite context set cloud --org org_1234abcd`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			c, exists := app.Config.Contexts[name]
			if !exists {
				if rawURL == "" {
					return cmdutil.UsageError("--url is required for a new context")
				}
				c = &config.Context{Auth: config.AuthAPIKey}
				app.Config.Contexts[name] = c
			}
			if rawURL != "" {
				u, err := url.Parse(rawURL)
				if err != nil || u.Scheme == "" || u.Host == "" {
					return cmdutil.UsageError("--url must be a full URL, like https://api.partite.ai")
				}
				c.URL = rawURL
			}
			if authMethod != "" {
				switch config.AuthMethod(authMethod) {
				case config.AuthLogin, config.AuthAPIKey, config.AuthNone:
					c.Auth = config.AuthMethod(authMethod)
				default:
					return cmdutil.UsageError("--auth must be login, api-key or none")
				}
			}
			if cmd.Flags().Changed("web-url") {
				if webURL != "" {
					if u, err := url.Parse(webURL); err != nil || u.Scheme == "" || u.Host == "" {
						return cmdutil.UsageError("--web-url must be a full URL, like https://app.partite.ai")
					}
				}
				c.WebURL = webURL
			}
			if cmd.Flags().Changed("org") {
				c.Org = org
			}
			if enterprise != "" {
				switch enterprise {
				case "auto":
					c.Enterprise = nil
				default:
					b, err := strconv.ParseBool(enterprise)
					if err != nil {
						return cmdutil.UsageError("--enterprise must be true, false or auto")
					}
					c.Enterprise = &b
				}
			}
			if err := app.Config.Save(); err != nil {
				return err
			}
			verb := "Updated"
			if !exists {
				verb = "Created"
			}
			fmt.Fprintf(app.Err, "%s context %s.\n", verb, name)
			if c.Auth == config.AuthAPIKey && c.APIKeyID == "" {
				fmt.Fprintf(app.Err, "Store its API key with: partite login --context %s --with-api-key\n", name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rawURL, "url", "", "server URL")
	cmd.Flags().StringVar(&authMethod, "auth", "", "how to authenticate: login, api-key or none")
	cmd.Flags().StringVar(&org, "org", "", "Kinde org code to log in to; empty to choose at login")
	cmd.Flags().StringVar(&enterprise, "enterprise", "", "whether the server has enterprise features: true, false or auto")
	cmd.Flags().StringVar(&webURL, "web-url", "", "URL of the server's Partite web app, which view commands open; empty for none")
	return cmd
}

func newDelete(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:               "delete <name>",
		Aliases:           []string{"rm"},
		Short:             "Delete a context and its stored credentials",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, ok := app.Config.Contexts[name]; !ok {
				return cmdutil.Errorf(cmdutil.ExitNotFound, "List contexts with: partite context list", "there is no context named %q", name)
			}
			if err := credstore.Delete(name); err != nil {
				return err
			}
			if err := credstore.DeleteCallerKey(name); err != nil {
				return err
			}
			delete(app.Config.Contexts, name)
			if app.Config.Current == name {
				app.Config.Current = config.DefaultContext
			}
			if err := app.Config.Save(); err != nil {
				return err
			}
			if config.IsBuiltin(name) {
				fmt.Fprintf(app.Err, "Reset context %s to its defaults.\n", name)
			} else {
				fmt.Fprintf(app.Err, "Deleted context %s.\n", name)
			}
			return nil
		},
	}
}

func completeNames(app *cmdutil.App) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.Load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return cfg.Names(), cobra.ShellCompDirectiveNoFileComp
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
