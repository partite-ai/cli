// Package accesspoint manages access points: entry points into meshes for
// callers that bring their own tokens, and for clients the access point issues
// tokens to.
package accesspoint

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/boundary"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Issuer is an outside token issuer the access point accepts tokens from.
type Issuer struct {
	Issuer string `json:"issuer"`
	// Left out, it is the access point's URL.
	Audience      string          `json:"audience,omitempty"`
	CheckAudience *bool           `json:"check_audience,omitempty"`
	JwksURL       string          `json:"jwks_url,omitempty"`
	Jwks          json.RawMessage `json:"jwks,omitempty"`
}

type Secret struct {
	ID         string `json:"id"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// Client is a client the access point issues tokens to.
type Client struct {
	ID            string          `json:"id,omitempty"`
	AccessPointID string          `json:"access_point_id,omitempty"`
	ClientID      string          `json:"client_id,omitempty"`
	Name          string          `json:"name"`
	Claims        json.RawMessage `json:"claims,omitempty"`
	RedirectURIs  []string        `json:"redirect_uris"`
	Secrets       []Secret        `json:"secrets,omitempty"`
	CreatedAt     string          `json:"created_at,omitempty"`
	UpdatedAt     string          `json:"updated_at,omitempty"`
}

// AccessPoint is the view printed by get and read by -f. Clients are shown
// but not read; manage them with partite access-point client.
type AccessPoint struct {
	ID                         string        `json:"id,omitempty"`
	Name                       string        `json:"name"`
	Description                string        `json:"description,omitempty"`
	URL                        string        `json:"url,omitempty"`
	Tags                       []cmdutil.Tag `json:"tags"`
	AuthorizationServerURL     string        `json:"authorization_server_url,omitempty"`
	ClientTokenLifetimeSeconds int           `json:"client_token_lifetime_seconds,omitempty"`
	Issuers                    []Issuer      `json:"issuers"`
	BoundaryIDs                []string      `json:"boundary_ids"`
	Clients                    []Client      `json:"clients,omitempty"`
	CreatedAt                  string        `json:"created_at,omitempty"`
	UpdatedAt                  string        `json:"updated_at,omitempty"`

	boundaryNames []string
}

func clientFromAPI(d gql.AccessPointClientView) Client {
	c := Client{
		ID:            d.Id,
		AccessPointID: d.AccessPointID,
		ClientID:      d.ClientId,
		Name:          d.Name,
		Claims:        d.Claims,
		RedirectURIs:  d.RedirectUris,
		Secrets:       make([]Secret, len(d.Secrets)),
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
	if string(c.Claims) == "{}" || string(c.Claims) == "null" {
		c.Claims = nil
	}
	for i, s := range d.Secrets {
		c.Secrets[i] = Secret{ID: s.Id, ExpiresAt: cmdutil.Deref(s.ExpiresAt), LastUsedAt: cmdutil.Deref(s.LastUsedAt), CreatedAt: s.CreatedAt}
	}
	return c
}

func fromAPI(d gql.AccessPointView) AccessPoint {
	ap := AccessPoint{
		ID:                         d.Id,
		Name:                       d.Name,
		Description:                cmdutil.Deref(d.Description),
		URL:                        d.Url,
		Tags:                       cmdutil.TagsFromAPI(d.Tags),
		AuthorizationServerURL:     cmdutil.Deref(d.AuthorizationServerUrl),
		ClientTokenLifetimeSeconds: d.ClientTokenLifetimeSeconds,
		Issuers:                    make([]Issuer, len(d.Issuers)),
		BoundaryIDs:                make([]string, len(d.Boundaries)),
		Clients:                    make([]Client, len(d.Clients)),
		CreatedAt:                  d.CreatedAt,
		UpdatedAt:                  d.UpdatedAt,
	}
	for i, is := range d.Issuers {
		ap.Issuers[i] = Issuer{
			Issuer:        is.Issuer,
			Audience:      cmdutil.Deref(is.Audience),
			CheckAudience: cmdutil.Ptr(is.Audience != nil),
			JwksURL:       cmdutil.Deref(is.JwksUrl),
		}
		if is.Jwks != nil {
			ap.Issuers[i].Jwks = *is.Jwks
		}
	}
	for i, b := range d.Boundaries {
		ap.BoundaryIDs[i] = b.Id
		ap.boundaryNames = append(ap.boundaryNames, b.Name)
	}
	for i, c := range d.Clients {
		ap.Clients[i] = clientFromAPI(c.AccessPointClientView)
	}
	return ap
}

func issuerInputs(issuers []Issuer) ([]gql.AccessPointIssuerInput, error) {
	in := make([]gql.AccessPointIssuerInput, len(issuers))
	for i, is := range issuers {
		in[i] = gql.AccessPointIssuerInput{Issuer: is.Issuer, JwksURL: cmdutil.OptString(is.JwksURL), JWKS: is.Jwks}
		switch {
		case is.CheckAudience != nil && !*is.CheckAudience:
			if is.Audience != "" {
				return nil, cmdutil.UsageError("issuer %s sets audience with check_audience false", is.Issuer)
			}
			in[i].Audience = gql.OptNil[string](nil)
		case is.Audience != "":
			in[i].Audience = gql.Opt(is.Audience)
		}
	}
	return in, nil
}

// boundaryAssociations resolves boundary names or ids.
func boundaryAssociations(ctx context.Context, app *cmdutil.App, refs []string) ([]gql.NewBoundaryAccessPointAssociation, error) {
	in := make([]gql.NewBoundaryAccessPointAssociation, len(refs))
	for i, ref := range refs {
		id, err := boundary.Boundaries.Resolve(ctx, app, ref)
		if err != nil {
			return nil, err
		}
		in[i] = gql.NewBoundaryAccessPointAssociation{BoundaryID: id}
	}
	return in, nil
}

var AccessPoints = &cmdutil.Resource[AccessPoint]{
	Kind:     "access point",
	Command:  "access-point",
	IDPrefix: "capt",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]AccessPoint, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[AccessPoint], error) {
			resp, err := gql.AccessPointList(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[AccessPoint]{}, err
			}
			conn := resp.AccessPoints
			items := make([]AccessPoint, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[AccessPoint]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (AccessPoint, error) {
		c, err := app.GQL()
		if err != nil {
			return AccessPoint{}, err
		}
		resp, err := gql.AccessPointGet(ctx, c, id)
		if err != nil {
			return AccessPoint{}, err
		}
		if resp.AccessPoint == nil {
			return AccessPoint{}, cmdutil.NotFound("access point", id, "partite access-point list")
		}
		return fromAPI(*resp.AccessPoint), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		_, err = gql.AccessPointDelete(ctx, c, ids)
		return err
	},
	ID:      func(ap AccessPoint) string { return ap.ID },
	Name:    func(ap AccessPoint) string { return ap.Name },
	Columns: []string{"id", "name", "url", "clients", "boundaries", "created"},
	Row: func(ap AccessPoint) []string {
		return []string{ap.ID, ap.Name, ap.URL, fmt.Sprint(len(ap.Clients)), strings.Join(ap.boundaryNames, ","), cmdutil.FormatTime(ap.CreatedAt)}
	},
	Fields: func(ap AccessPoint) []output.Field {
		issuers := make([]string, len(ap.Issuers))
		for i, is := range ap.Issuers {
			s := is.Issuer
			switch {
			case is.CheckAudience != nil && !*is.CheckAudience:
				s += " (audience not checked)"
			case is.Audience != "":
				s += " (audience " + is.Audience + ")"
			}
			issuers[i] = s
		}
		clients := make([]string, len(ap.Clients))
		for i, c := range ap.Clients {
			clients[i] = fmt.Sprintf("%s (%s): client id %s, %s", c.Name, c.ID, c.ClientID, cmdutil.Plural(len(c.Secrets), "secret"))
		}
		lifetime := ""
		if ap.ClientTokenLifetimeSeconds != 0 {
			lifetime = (time.Duration(ap.ClientTokenLifetimeSeconds) * time.Second).String()
		}
		return []output.Field{
			{Label: "ID", Value: ap.ID},
			{Label: "Name", Value: ap.Name},
			{Label: "Description", Value: ap.Description},
			{Label: "URL", Value: ap.URL},
			{Label: "Tags", Value: cmdutil.FormatTags(ap.Tags)},
			{Label: "Authorization server", Value: ap.AuthorizationServerURL},
			{Label: "Client token lifetime", Value: lifetime},
			{Label: "Issuers", Value: strings.Join(issuers, "\n")},
			{Label: "Clients", Value: strings.Join(clients, "\n")},
			{Label: "Boundaries", Value: strings.Join(ap.boundaryNames, ", ")},
			{Label: "Created", Value: ap.CreatedAt},
			{Label: "Updated", Value: ap.UpdatedAt},
		}
	},
}

const longHelp = `An access point lets callers outside your organization into your meshes.
Callers reach it at its URL with a token, and carry its tags.

Tokens come from two places. Issuers are outside identity providers whose
tokens the access point accepts, such as your own login service. Clients are
programs the access point issues tokens to itself, with a client id and
secret; manage them with partite access-point client.

Boundaries attached to an access point check every conversation created
through it.`

const specExample = `  # access-point.yaml
  name: customers
  description: Our customer portal
  tags:
    - name: audience
      value: customers
  issuers:
    - issuer: https://login.example.com
      jwks_url: https://login.example.com/.well-known/jwks.json
  boundary_ids:
    - no-refunds                     # a boundary name or id`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "access-point",
		Aliases: []string{"access-points"},
		Short:   "Manage access points, which let outside callers into meshes",
		Long:    longHelp,
	}
	cmd.AddCommand(
		AccessPoints.ListCmd(app),
		AccessPoints.GetCmd(app),
		newCreate(app),
		newUpdate(app),
		AccessPoints.ApplyCmd(app, `  partite access-point get customers -o yaml > access-point.yaml
  # edit access-point.yaml
  partite access-point apply -f access-point.yaml

`+specExample, func(ctx context.Context, spec AccessPoint, id string) (AccessPoint, error) {
			if id == "" {
				return create(ctx, app, spec)
			}
			return replace(ctx, app, id, spec)
		}),
		AccessPoints.DeleteCmd(app),
		newRotateKey(app),
		newTrustCLI(app),
		newUntrustCLI(app),
		newClientCmd(app),
		newSecretCmd(app),
	)
	return cmd
}

func create(ctx context.Context, app *cmdutil.App, spec AccessPoint) (AccessPoint, error) {
	issuers, err := issuerInputs(spec.Issuers)
	if err != nil {
		return AccessPoint{}, err
	}
	boundaries, err := boundaryAssociations(ctx, app, spec.BoundaryIDs)
	if err != nil {
		return AccessPoint{}, err
	}
	in := &gql.NewAccessPoint{
		Name:                   spec.Name,
		Description:            cmdutil.OptString(spec.Description),
		Tags:                   cmdutil.TagsToAPI(spec.Tags),
		AuthorizationServerURL: cmdutil.OptString(spec.AuthorizationServerURL),
		Issuers:                issuers,
		Boundaries:             boundaries,
	}
	if spec.ClientTokenLifetimeSeconds != 0 {
		in.ClientTokenLifetimeSeconds = &spec.ClientTokenLifetimeSeconds
	}
	c, err := app.GQL()
	if err != nil {
		return AccessPoint{}, err
	}
	resp, err := gql.AccessPointCreate(ctx, c, in)
	if err != nil {
		return AccessPoint{}, err
	}
	return fromAPI(resp.CreateAccessPoint), nil
}

// replace makes the access point with this id match spec in full. Its
// clients are left as they are.
func replace(ctx context.Context, app *cmdutil.App, id string, spec AccessPoint) (AccessPoint, error) {
	issuers, err := issuerInputs(spec.Issuers)
	if err != nil {
		return AccessPoint{}, err
	}
	boundaries, err := boundaryAssociations(ctx, app, spec.BoundaryIDs)
	if err != nil {
		return AccessPoint{}, err
	}
	u := &gql.UpdateAccessPoint{
		Name:                   gql.Opt(spec.Name),
		Description:            gql.Opt(spec.Description),
		Tags:                   gql.Opt(cmdutil.TagsToAPI(spec.Tags)),
		AuthorizationServerURL: gql.OptNil(cmdutil.OptString(spec.AuthorizationServerURL)),
		Issuers:                gql.Opt(issuers),
		Boundaries:             gql.Opt(boundaries),
	}
	if spec.ClientTokenLifetimeSeconds != 0 {
		u.ClientTokenLifetimeSeconds = gql.Opt(spec.ClientTokenLifetimeSeconds)
	}
	return update(ctx, app, id, u)
}

func update(ctx context.Context, app *cmdutil.App, id string, u *gql.UpdateAccessPoint) (AccessPoint, error) {
	c, err := app.GQL()
	if err != nil {
		return AccessPoint{}, err
	}
	resp, err := gql.AccessPointUpdate(ctx, c, id, u)
	if err != nil {
		return AccessPoint{}, err
	}
	return fromAPI(resp.UpdateAccessPoint), nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec AccessPoint
	var file string
	var tags []string
	var lifetime time.Duration
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an access point",
		Long: `Create an access point. Issuers come from -f. Add clients afterwards with
partite access-point client create.`,
		Example: `  partite access-point create partners --boundary partner-limits
  partite access-point create -f access-point.yaml

` + specExample,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			flags := spec
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
				if f.Changed("description") {
					spec.Description = flags.Description
				}
				if f.Changed("authorization-server-url") {
					spec.AuthorizationServerURL = flags.AuthorizationServerURL
				}
				if f.Changed("boundary") {
					spec.BoundaryIDs = flags.BoundaryIDs
				}
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			if f.Changed("tag") {
				t, err := cmdutil.ParseTags(tags)
				if err != nil {
					return err
				}
				spec.Tags = t
			}
			if f.Changed("client-token-lifetime") {
				spec.ClientTokenLifetimeSeconds = int(lifetime.Seconds())
			}
			ap, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created access point %s (%s) at %s.", ap.Name, ap.ID, ap.URL)
			if app.Format() == output.Table {
				return nil
			}
			return AccessPoints.Show(app, ap)
		},
	}
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag every caller carries, as name=value; repeat for more")
	cmd.Flags().StringVar(&spec.AuthorizationServerURL, "authorization-server-url", "", "where MCP clients are told to log in")
	cmd.Flags().DurationVar(&lifetime, "client-token-lifetime", 0, "how long a token issued to a client lasts, from 5m to 24h (default 1h)")
	cmd.Flags().StringSliceVar(&spec.BoundaryIDs, "boundary", nil, "boundary name or id to attach; repeat for more")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the access point from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description, authServer string
	var tags, boundaries []string
	var noBoundaries bool
	var lifetime time.Duration
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change an access point's settings or boundaries",
		Long: `Change an access point. Only the flags you pass change. To change issuers,
use partite access-point apply. To change clients, use partite access-point
client.`,
		Example: `  partite access-point update partners --boundary partner-limits --boundary no-refunds
  partite access-point update partners --client-token-lifetime 15m`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := AccessPoints.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var u gql.UpdateAccessPoint
			if f.Changed("name") {
				u.Name = gql.Opt(name)
			}
			if f.Changed("description") {
				u.Description = gql.Opt(description)
			}
			if f.Changed("tag") {
				t, err := cmdutil.ParseTags(tags)
				if err != nil {
					return err
				}
				u.Tags = gql.Opt(cmdutil.TagsToAPI(t))
			}
			if f.Changed("authorization-server-url") {
				u.AuthorizationServerURL = gql.OptNil(cmdutil.OptString(authServer))
			}
			if f.Changed("client-token-lifetime") {
				u.ClientTokenLifetimeSeconds = gql.Opt(int(lifetime.Seconds()))
			}
			if f.Changed("boundary") && noBoundaries {
				return cmdutil.UsageError("pass --boundary or --no-boundaries, not both")
			}
			if f.Changed("boundary") || noBoundaries {
				b, err := boundaryAssociations(ctx, app, boundaries)
				if err != nil {
					return err
				}
				u.Boundaries = gql.Opt(b)
			}
			if !u.Name.Set && !u.Description.Set && !u.Tags.Set && !u.AuthorizationServerURL.Set && !u.ClientTokenLifetimeSeconds.Set && !u.Boundaries.Set {
				return cmdutil.UsageError("nothing to change; pass --name, --description, --tag, --authorization-server-url, --client-token-lifetime, --boundary or --no-boundaries")
			}
			ap, err := update(ctx, app, id, &u)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated access point %s.", ap.Name)
			if app.Format() == output.Table {
				return nil
			}
			return AccessPoints.Show(app, ap)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tags as name=value, replacing the current ones; repeat for more")
	cmd.Flags().StringVar(&authServer, "authorization-server-url", "", "where MCP clients are told to log in; empty removes it")
	cmd.Flags().DurationVar(&lifetime, "client-token-lifetime", 0, "how long a token issued to a client lasts, from 5m to 24h")
	cmd.Flags().StringSliceVar(&boundaries, "boundary", nil, "boundary name or id, replacing the current ones; repeat for more")
	cmd.Flags().BoolVar(&noBoundaries, "no-boundaries", false, "detach every boundary")
	return cmd
}

func newRotateKey(app *cmdutil.App) *cobra.Command {
	var dropPrevious bool
	cmd := &cobra.Command{
		Use:   "rotate-key <name-or-id>",
		Short: "Replace the key that signs the tokens an access point issues",
		Long: `Replace the key that signs the tokens the access point issues to its
clients. Keys rotate on their own once a year. The old key keeps checking the
tokens it signed until they expire. With --drop-previous they stop working at
once, which logs every client out.`,
		Example: "  partite access-point rotate-key partners\n  partite access-point rotate-key partners --drop-previous --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := AccessPoints.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			if dropPrevious {
				if err := app.Confirm(fmt.Sprintf("Rotate the key of access point %s and stop every token it issued from working?", args[0])); err != nil {
					return err
				}
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			if _, err := gql.AccessPointRotateKey(ctx, c, id, dropPrevious); err != nil {
				return err
			}
			app.Printer().Done("Rotated the signing key of access point %s.", args[0])
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"id": id, "dropped_previous": dropPrevious})
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dropPrevious, "drop-previous", false, "stop tokens signed by the old key from working at once")
	return cmd
}
