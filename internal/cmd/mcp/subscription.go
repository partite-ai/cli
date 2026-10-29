package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Subscription is a tunnel connection's subscription: the conversation tags
// that decide which relays serve which conversations.
type Subscription struct {
	ID              string        `json:"id,omitempty"`
	MCPConnectionID string        `json:"mcp_connection_id"`
	Name            string        `json:"name"`
	Criteria        []cmdutil.Tag `json:"criteria,omitempty"`
	Relays          []Relay       `json:"relays,omitempty"`
	CreatedAt       string        `json:"created_at,omitempty"`
	UpdatedAt       string        `json:"updated_at,omitempty"`

	keys []TunnelKey
}

// Relay is a relay attached to a subscription right now.
type Relay struct {
	AttachedAt     string `json:"attached_at"`
	RemoteAddress  string `json:"remote_address"`
	ForwardedFor   string `json:"forwarded_for,omitempty"`
	TunnelKeyLabel string `json:"tunnel_key_label"`
	ClientName     string `json:"client_name,omitempty"`
	ClientVersion  string `json:"client_version,omitempty"`
}

// TunnelKey is a relay credential. The credential itself is shown only by
// create.
type TunnelKey struct {
	ID                          string `json:"id"`
	Label                       string `json:"label"`
	MCPConnectionID             string `json:"mcp_connection_id"`
	MCPConnectionSubscriptionID string `json:"mcp_connection_subscription_id,omitempty"`
	Attached                    bool   `json:"attached"`
	ExpiresAt                   string `json:"expires_at,omitempty"`
	RevokedAt                   string `json:"revoked_at,omitempty"`
	CreatedAt                   string `json:"created_at,omitempty"`
}

func keyFromAPI(connID, subID string, k gql.McpConnectionTunnelKeyDetails) TunnelKey {
	return TunnelKey{
		ID:                          k.Id,
		Label:                       k.Label,
		MCPConnectionID:             connID,
		MCPConnectionSubscriptionID: subID,
		Attached:                    k.Attached,
		ExpiresAt:                   cmdutil.Deref(k.ExpiresAt),
		RevokedAt:                   cmdutil.Deref(k.RevokedAt),
		CreatedAt:                   k.CreatedAt,
	}
}

func subscriptionFromAPI(connID string, s gql.ToolsMcpSubscription) Subscription {
	sub := Subscription{ID: s.Id, MCPConnectionID: connID, Name: s.Name, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
	for _, c := range s.Criteria {
		sub.Criteria = append(sub.Criteria, cmdutil.Tag{Name: c.Name, Value: c.Value})
	}
	for _, r := range s.Relays {
		sub.Relays = append(sub.Relays, Relay{
			AttachedAt:     r.AttachedAt,
			RemoteAddress:  r.RemoteAddress,
			ForwardedFor:   cmdutil.Deref(r.ForwardedFor),
			TunnelKeyLabel: r.TunnelKeyLabel,
			ClientName:     cmdutil.Deref(r.ClientName),
			ClientVersion:  cmdutil.Deref(r.ClientVersion),
		})
	}
	for _, k := range s.TunnelKeys {
		sub.keys = append(sub.keys, keyFromAPI(connID, s.Id, k))
	}
	return sub
}

// loadSubscriptions resolves a tunnel connection and returns its id and
// subscriptions.
func loadSubscriptions(ctx context.Context, app *cmdutil.App, connRef string) (string, []Subscription, error) {
	connID, err := Resource.Resolve(ctx, app, connRef)
	if err != nil {
		return "", nil, err
	}
	c, err := app.GQL()
	if err != nil {
		return "", nil, err
	}
	resp, err := gql.ToolsMcpConnectionSubscriptions(ctx, c, connID)
	if err != nil {
		return "", nil, err
	}
	conn := resp.McpConnection
	if conn == nil {
		return "", nil, cmdutil.NotFound("MCP connection", connRef, "partite mcp list")
	}
	if conn.Transport != gql.McpTransportTunnel || conn.TunnelTransport == nil {
		return "", nil, cmdutil.UsageError("%s is not a tunnel connection; only tunnel connections have subscriptions and tunnel keys", conn.Name)
	}
	subs := make([]Subscription, len(conn.TunnelTransport.Subscriptions))
	for i, s := range conn.TunnelTransport.Subscriptions {
		subs[i] = subscriptionFromAPI(connID, s)
	}
	return connID, subs, nil
}

// findSubscription resolves a subscription name or id among subs.
func findSubscription(ctx context.Context, subs []Subscription, ref string) (Subscription, error) {
	id, err := cmdutil.Resolve(ctx, ref, "csub", "subscription", "mcp subscription", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		named := make([]cmdutil.Named, len(subs))
		for i, s := range subs {
			named[i] = cmdutil.Named{ID: s.ID, Name: s.Name}
		}
		return named, nil
	})
	if err != nil {
		return Subscription{}, err
	}
	for _, s := range subs {
		if s.ID == id {
			return s, nil
		}
	}
	return Subscription{}, cmdutil.NotFound("subscription", ref, "partite mcp subscription list <connection>")
}

func showSubscription(app *cmdutil.App, s Subscription) error {
	relays := make([]string, len(s.Relays))
	for i, r := range s.Relays {
		relays[i] = fmt.Sprintf("%s from %s since %s", r.TunnelKeyLabel, r.RemoteAddress, r.AttachedAt)
	}
	return app.Printer().Object(s, s.ID, []output.Field{
		{Label: "ID", Value: s.ID},
		{Label: "Name", Value: s.Name},
		{Label: "Connection", Value: s.MCPConnectionID},
		{Label: "Criteria", Value: orDefault(cmdutil.FormatTags(s.Criteria), "(none: matches every conversation)")},
		{Label: "Tunnel keys", Value: fmt.Sprint(len(s.keys))},
		{Label: "Relays", Value: strings.Join(relays, "\n")},
		{Label: "Created", Value: s.CreatedAt},
		{Label: "Updated", Value: s.UpdatedAt},
	})
}

func criteriaToAPI(tags []cmdutil.Tag) []gql.McpConnectionSubscriptionCriterionInput {
	out := make([]gql.McpConnectionSubscriptionCriterionInput, len(tags))
	for i, t := range tags {
		out[i] = gql.McpConnectionSubscriptionCriterionInput{Name: t.Name, Value: t.Value}
	}
	return out
}

func subscriptionDetailsToView(connID string, d gql.McpConnectionSubscriptionDetails) Subscription {
	s := Subscription{ID: d.Id, MCPConnectionID: connID, Name: d.Name, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
	for _, c := range d.Criteria {
		s.Criteria = append(s.Criteria, cmdutil.Tag{Name: c.Name, Value: c.Value})
	}
	return s
}

func newSubscription(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "subscription",
		Aliases: []string{"subscriptions", "sub"},
		Short:   "Manage a tunnel connection's subscriptions",
		Long: `A subscription decides which relays serve which conversations of a tunnel
connection. Its criteria are conversation tags: a conversation is served by a
subscription when it has every tag in the criteria. A subscription with no
criteria serves every conversation.

Relays authenticate with a tunnel key made for one subscription. See
partite mcp tunnel-key.`,
	}
	cmd.AddCommand(
		newSubscriptionList(app),
		newSubscriptionGet(app),
		newSubscriptionCreate(app),
		newSubscriptionUpdate(app),
		newSubscriptionDelete(app),
	)
	return cmd
}

func newSubscriptionList(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "list <connection>",
		Aliases: []string{"ls"},
		Short:   "List a tunnel connection's subscriptions",
		Example: "  partite mcp subscription list laptop",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, subs, err := loadSubscriptions(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			rows := make([][]string, len(subs))
			for i, s := range subs {
				rows[i] = []string{s.ID, s.Name, cmdutil.FormatTags(s.Criteria), fmt.Sprint(len(s.keys)), fmt.Sprint(len(s.Relays))}
			}
			return app.Printer().List(subs, []string{"id", "name", "criteria", "keys", "relays"}, rows)
		},
	}
}

func newSubscriptionGet(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "get <connection> <subscription>",
		Aliases: []string{"show"},
		Short:   "Show a subscription and the relays attached to it",
		Example: "  partite mcp subscription get laptop bob",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, subs, err := loadSubscriptions(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			s, err := findSubscription(cmd.Context(), subs, args[1])
			if err != nil {
				return err
			}
			return showSubscription(app, s)
		},
	}
}

func newSubscriptionCreate(app *cmdutil.App) *cobra.Command {
	var criteria []string
	cmd := &cobra.Command{
		Use:   "create <connection> <name>",
		Short: "Add a subscription to a tunnel connection",
		Example: `  partite mcp subscription create laptop default
  partite mcp subscription create laptop bob --criterion developer=bob`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			tags, err := cmdutil.ParseTags(criteria)
			if err != nil {
				return err
			}
			connID, _, err := loadSubscriptions(ctx, app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.CreateMcpConnectionSubscription(ctx, c, gql.NewMcpConnectionSubscription{
				McpConnectionID: connID,
				Name:            args[1],
				Criteria:        criteriaToAPI(tags),
			})
			if err != nil {
				return err
			}
			s := subscriptionDetailsToView(connID, resp.CreateMcpConnectionSubscription)
			app.Printer().Done("Created subscription %s (%s).", s.Name, s.ID)
			app.Printer().Done("Make a key for its relay with: partite mcp tunnel-key create %s <label> --subscription %s", args[0], s.Name)
			if app.Format() == output.Table {
				return nil
			}
			return showSubscription(app, s)
		},
	}
	cmd.Flags().StringArrayVar(&criteria, "criterion", nil, "conversation tag the subscription requires, as name=value; repeat for more")
	return cmd
}

func newSubscriptionUpdate(app *cmdutil.App) *cobra.Command {
	var name string
	var criteria []string
	var noCriteria bool
	cmd := &cobra.Command{
		Use:   "update <connection> <subscription>",
		Short: "Rename a subscription or change its criteria",
		Example: `  partite mcp subscription update laptop bob --criterion developer=bob --criterion env=dev
  partite mcp subscription update laptop bob --no-criteria`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			f := cmd.Flags()
			if !f.Changed("name") && !f.Changed("criterion") && !noCriteria {
				return cmdutil.UsageError("nothing to change; pass --name, --criterion or --no-criteria")
			}
			connID, subs, err := loadSubscriptions(ctx, app, args[0])
			if err != nil {
				return err
			}
			cur, err := findSubscription(ctx, subs, args[1])
			if err != nil {
				return err
			}
			changes := gql.McpConnectionSubscriptionChanges{}
			if f.Changed("name") {
				changes.Name = &name
			}
			if noCriteria {
				changes.Criteria = gql.Optional[[]gql.McpConnectionSubscriptionCriterionInput]{Set: true, Nil: true}
			}
			if f.Changed("criterion") {
				tags, err := cmdutil.ParseTags(criteria)
				if err != nil {
					return err
				}
				changes.Criteria = gql.Opt(criteriaToAPI(tags))
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ModifyMcpConnectionSubscription(ctx, c, cur.ID, &changes)
			if err != nil {
				return err
			}
			s := subscriptionDetailsToView(connID, resp.UpdateMcpConnectionSubscription)
			app.Printer().Done("Updated subscription %s.", s.Name)
			if app.Format() == output.Table {
				return nil
			}
			return showSubscription(app, s)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringArrayVar(&criteria, "criterion", nil, "conversation tag the subscription requires, as name=value, replacing the current ones; repeat for more")
	cmd.Flags().BoolVar(&noCriteria, "no-criteria", false, "remove all criteria, so the subscription serves every conversation")
	cmd.MarkFlagsMutuallyExclusive("criterion", "no-criteria")
	return cmd
}

func newSubscriptionDelete(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <connection> <subscription>...",
		Aliases: []string{"rm"},
		Short:   "Delete subscriptions and revoke their keys",
		Example: "  partite mcp subscription delete laptop bob --yes",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, subs, err := loadSubscriptions(ctx, app, args[0])
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(args)-1)
			for _, ref := range args[1:] {
				s, err := findSubscription(ctx, subs, ref)
				if err != nil {
					return err
				}
				ids = append(ids, s.ID)
			}
			what := "subscription " + strings.Join(args[1:], ", ")
			if err := app.Confirm(fmt.Sprintf("Delete %s of %s?", what, args[0])); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			for _, id := range ids {
				if _, err := gql.DeleteMcpConnectionSubscription(ctx, c, id); err != nil {
					return err
				}
			}
			app.Printer().Done("Deleted %s.", what)
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": ids})
			}
			return nil
		},
	}
}

func newTunnelKey(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tunnel-key",
		Aliases: []string{"tunnel-keys"},
		Short:   "Manage the keys relays use to serve a tunnel connection",
		Long: `A tunnel key lets a relay serve one subscription of a tunnel connection. Run
the relay with the key in PARTITE_RELAY_TOKEN. The key is shown once, when it
is created.`,
	}
	cmd.AddCommand(newTunnelKeyList(app), newTunnelKeyCreate(app), newTunnelKeyRevoke(app))
	return cmd
}

func newTunnelKeyList(app *cmdutil.App) *cobra.Command {
	var subRef string
	cmd := &cobra.Command{
		Use:     "list <connection>",
		Aliases: []string{"ls"},
		Short:   "List a tunnel connection's keys",
		Example: "  partite mcp tunnel-key list laptop\n  partite mcp tunnel-key list laptop --subscription bob",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, subs, err := loadSubscriptions(ctx, app, args[0])
			if err != nil {
				return err
			}
			if subRef != "" {
				s, err := findSubscription(ctx, subs, subRef)
				if err != nil {
					return err
				}
				subs = []Subscription{s}
			}
			keys := []TunnelKey{}
			var rows [][]string
			for _, s := range subs {
				for _, k := range s.keys {
					keys = append(keys, k)
					rows = append(rows, []string{k.ID, k.Label, s.Name, keyStatus(k), cmdutil.FormatTime(k.ExpiresAt), cmdutil.FormatTime(k.CreatedAt)})
				}
			}
			return app.Printer().List(keys, []string{"id", "label", "subscription", "status", "expires", "created"}, rows)
		},
	}
	cmd.Flags().StringVar(&subRef, "subscription", "", "only list this subscription's keys")
	return cmd
}

func keyStatus(k TunnelKey) string {
	switch {
	case k.RevokedAt != "":
		return "revoked"
	case k.ExpiresAt != "":
		if t, err := time.Parse(time.RFC3339Nano, k.ExpiresAt); err == nil && t.Before(time.Now()) {
			return "expired"
		}
	}
	if !k.Attached {
		return "unattached"
	}
	return "active"
}

func newTunnelKeyCreate(app *cmdutil.App) *cobra.Command {
	var subRef, expiresAt string
	var expiresIn time.Duration
	cmd := &cobra.Command{
		Use:   "create <connection> <label>",
		Short: "Create a relay key and print it",
		Long: `Create a key for a relay that serves one subscription of a tunnel connection.
The key is printed once and cannot be read again. --subscription can be left
out when the connection has only one subscription.`,
		Example: `  partite mcp tunnel-key create laptop laptop-bob --subscription bob
  partite mcp tunnel-key create laptop ci --expires-in 720h -o json | jq -r .credential`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if expiresAt != "" && expiresIn != 0 {
				return cmdutil.UsageError("pass --expires-at or --expires-in, not both")
			}
			if expiresAt != "" {
				t, err := time.Parse(time.RFC3339, expiresAt)
				if err != nil {
					return cmdutil.UsageError("--expires-at must be a time like 2026-12-31T00:00:00Z")
				}
				expiresAt = t.UTC().Format(time.RFC3339)
			}
			if expiresIn != 0 {
				expiresAt = time.Now().Add(expiresIn).UTC().Format(time.RFC3339)
			}
			connID, subs, err := loadSubscriptions(ctx, app, args[0])
			if err != nil {
				return err
			}
			var sub Subscription
			switch {
			case subRef != "":
				if sub, err = findSubscription(ctx, subs, subRef); err != nil {
					return err
				}
			case len(subs) == 1:
				sub = subs[0]
			case len(subs) == 0:
				return &cmdutil.Error{Code: cmdutil.ExitUsage, Msg: fmt.Sprintf("%s has no subscriptions", args[0]), Hint: fmt.Sprintf("Add one with: partite mcp subscription create %s default", args[0])}
			default:
				return cmdutil.UsageError("%s has %d subscriptions; pick one with --subscription", args[0], len(subs))
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.CreateMcpConnectionTunnelKey(ctx, c, gql.NewMcpConnectionTunnelKey{
				Label:                       args[1],
				ExpiresAt:                   cmdutil.OptString(expiresAt),
				McpConnectionSubscriptionID: &sub.ID,
			})
			if err != nil {
				return err
			}
			k := keyFromAPI(connID, sub.ID, resp.CreateMcpConnectionTunnelKey.TunnelKey)
			credential := resp.CreateMcpConnectionTunnelKey.Credential
			if app.Format() != output.Table {
				return app.Printer().Data(struct {
					TunnelKey
					Credential string `json:"credential"`
				}{k, credential})
			}
			fmt.Fprintf(app.Out, "Created tunnel key %s (%s) for subscription %s.\n\n", k.Label, k.ID, sub.Name)
			fmt.Fprintf(app.Out, "  PARTITE_RELAY_TOKEN=%s\n\n", credential)
			fmt.Fprintln(app.Out, "Save the key now. It cannot be shown again.")
			return nil
		},
	}
	cmd.Flags().StringVar(&subRef, "subscription", "", "subscription the key serves (default: the only one)")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "when the key stops working, such as 2026-12-31T00:00:00Z (default: never)")
	cmd.Flags().DurationVar(&expiresIn, "expires-in", 0, "how long the key works, such as 720h (default: forever)")
	return cmd
}

func newTunnelKeyRevoke(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "revoke <connection> <key>...",
		Aliases: []string{"delete", "rm"},
		Short:   "Revoke relay keys",
		Long:    "Revoke relay keys by label or id. A relay using a revoked key is refused on its next message.",
		Example: "  partite mcp tunnel-key revoke laptop laptop-bob --yes",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, subs, err := loadSubscriptions(ctx, app, args[0])
			if err != nil {
				return err
			}
			var all []cmdutil.Named
			for _, s := range subs {
				for _, k := range s.keys {
					if k.RevokedAt == "" {
						all = append(all, cmdutil.Named{ID: k.ID, Name: k.Label})
					}
				}
			}
			ids := make([]string, 0, len(args)-1)
			for _, ref := range args[1:] {
				id, err := cmdutil.Resolve(ctx, ref, "ptk", "tunnel key", "mcp tunnel-key", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
					return all, nil
				})
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			what := "tunnel key " + strings.Join(args[1:], ", ")
			if err := app.Confirm(fmt.Sprintf("Revoke %s of %s?", what, args[0])); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			for _, id := range ids {
				if _, err := gql.RevokeMcpConnectionTunnelKey(ctx, c, id); err != nil {
					return err
				}
			}
			app.Printer().Done("Revoked %s.", what)
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"revoked": ids})
			}
			return nil
		},
	}
}
