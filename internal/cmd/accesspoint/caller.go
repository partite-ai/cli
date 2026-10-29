package accesspoint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/callerkey"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// tokenLifetime is how long each token the CLI signs lasts. A new one is made
// when it runs out, so a long chat keeps working.
const tokenLifetime = 5 * time.Minute

// Claims a token sets itself, which --claim may not.
var registeredClaims = []string{"iss", "sub", "aud", "exp", "nbf", "iat", "jti"}

// CallerFlags make the commands that talk to a mesh go through an access
// point, as a caller the CLI makes up: a subject and claims, in a token
// signed with the context's caller key.
type CallerFlags struct {
	AccessPoint string
	Subject     string
	Claim       []string
	Claims      string
}

// CallerHelp says how to talk to a mesh through an access point.
const CallerHelp = `With --access-point, the command goes through that access point as a caller
you make up: --subject and --claim set what its boundaries see. The CLI signs
the caller's tokens itself with the context's caller key, so the access point
must trust it first: partite access-point trust-cli <access point>. Names of
meshes and routes are still looked up with your own login.`

// Register adds the flags. persistent puts them on the command's
// subcommands too.
func (f *CallerFlags) Register(cmd *cobra.Command, persistent bool) {
	flags := cmd.Flags()
	if persistent {
		flags = cmd.PersistentFlags()
	}
	flags.StringVar(&f.AccessPoint, "access-point", "", "go through this access point (name or id) as a made-up caller")
	flags.StringVar(&f.Subject, "subject", "partite-cli", "with --access-point, the caller's subject (sub)")
	flags.StringArrayVar(&f.Claim, "claim", nil, "with --access-point, a claim of the caller as name=value; a value that is JSON, such as 3 or true, is used as JSON; repeat for more")
	flags.StringVar(&f.Claims, "claims", "", "with --access-point, the caller's claims as a JSON object; @file reads a file; --claim adds to them")
}

// Apply points the app's REST client at the access point, when the flags ask
// for one.
func (f *CallerFlags) Apply(cmd *cobra.Command, app *cmdutil.App) error {
	flags := cmd.Flags()
	if f.AccessPoint == "" {
		if flags.Changed("subject") || flags.Changed("claim") || flags.Changed("claims") {
			return cmdutil.UsageError("--subject, --claim and --claims need --access-point")
		}
		return nil
	}
	ctx := cmd.Context()
	claims, err := f.parseClaims(app)
	if err != nil {
		return err
	}
	if f.Subject == "" {
		return cmdutil.UsageError("--subject may not be empty")
	}
	ap, err := resolveAndGet(ctx, app, f.AccessPoint)
	if err != nil {
		return err
	}
	key, err := callerkey.Load(app.ContextName)
	if err != nil {
		return err
	}
	if key == nil || !slices.ContainsFunc(ap.Issuers, func(i Issuer) bool { return i.Issuer == key.Issuer() }) {
		return &cmdutil.Error{
			Code: cmdutil.ExitUsage,
			Msg:  fmt.Sprintf("access point %s does not trust this CLI's tokens", ap.Name),
			Hint: "Let it with: partite access-point trust-cli " + ap.Name,
		}
	}
	source := oauth2.ReuseTokenSource(nil, callerTokenSource{key: key, audience: ap.URL, subject: f.Subject, claims: claims})
	app.SetREST(&cmdutil.REST{
		BaseURL: strings.TrimRight(ap.URL, "/"),
		HTTP:    &http.Client{Transport: &oauth2.Transport{Base: cmdutil.PlainHTTP().Transport, Source: source}},
	})
	return nil
}

func (f *CallerFlags) parseClaims(app *cmdutil.App) (map[string]any, error) {
	claims := map[string]any{}
	if f.Claims != "" {
		v, err := app.ReadValue(f.Claims)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(v), &claims); err != nil {
			return nil, cmdutil.UsageError("--claims must be a JSON object: %v", err)
		}
	}
	for _, c := range f.Claim {
		name, value, ok := strings.Cut(c, "=")
		if !ok || name == "" {
			return nil, cmdutil.UsageError("--claim must be name=value, got %q", c)
		}
		var v any
		if json.Unmarshal([]byte(value), &v) != nil {
			v = value
		}
		claims[name] = v
	}
	for name := range claims {
		if slices.Contains(registeredClaims, name) {
			return nil, cmdutil.UsageError("the CLI sets the %s claim itself; use --subject for sub", name)
		}
	}
	return claims, nil
}

type callerTokenSource struct {
	key      *callerkey.Key
	audience string
	subject  string
	claims   map[string]any
}

func (s callerTokenSource) Token() (*oauth2.Token, error) {
	now := time.Now()
	token, err := s.key.Sign(s.audience, s.subject, s.claims, now, tokenLifetime)
	if err != nil {
		return nil, err
	}
	return &oauth2.Token{AccessToken: token, TokenType: "Bearer", Expiry: now.Add(tokenLifetime)}, nil
}

func newTrustCLI(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "trust-cli <name-or-id>",
		Short: "Let this CLI talk to meshes through an access point as any caller",
		Long: `Add this context's caller key to the access point's issuers, so that run,
chat and conversation can go through the access point with --access-point,
as a caller with any subject and claims. Use it to try boundaries out.

The key is made the first time and kept with the context's credentials. Anyone
with the key can call through the access point as anyone, so trust it only on
access points you test with, and take it away with untrust-cli.`,
		Example: `  partite access-point trust-cli partners
  partite chat support --label prod --access-point partners --subject alice --claim tier=gold`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ap, err := resolveAndGet(ctx, app, args[0])
			if err != nil {
				return err
			}
			key, err := callerkey.LoadOrCreate(app.ContextName)
			if err != nil {
				return err
			}
			issuers := slices.DeleteFunc(slices.Clone(ap.Issuers), func(i Issuer) bool { return i.Issuer == key.Issuer() })
			issuers = append(issuers, Issuer{Issuer: key.Issuer(), Jwks: key.JWKS()})
			updated, err := setIssuers(ctx, app, ap.ID, issuers)
			if err != nil {
				return err
			}
			app.Printer().Done("Access point %s now trusts tokens this CLI signs, as issuer %s.", updated.Name, key.Issuer())
			if app.Format() == output.Table {
				return nil
			}
			return AccessPoints.Show(app, updated)
		},
	}
}

func newUntrustCLI(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "untrust-cli <name-or-id>",
		Short:   "Stop an access point trusting this CLI's tokens",
		Long:    "Take this context's caller key off the access point's issuers.",
		Example: "  partite access-point untrust-cli partners",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ap, err := resolveAndGet(ctx, app, args[0])
			if err != nil {
				return err
			}
			key, err := callerkey.Load(app.ContextName)
			if err != nil {
				return err
			}
			trusted := func(i Issuer) bool { return key != nil && i.Issuer == key.Issuer() }
			if !slices.ContainsFunc(ap.Issuers, trusted) {
				app.Printer().Done("Access point %s does not trust this CLI's tokens.", ap.Name)
				if app.Format() == output.Table {
					return nil
				}
				return AccessPoints.Show(app, ap)
			}
			updated, err := setIssuers(ctx, app, ap.ID, slices.DeleteFunc(slices.Clone(ap.Issuers), trusted))
			if err != nil {
				return err
			}
			app.Printer().Done("Access point %s no longer trusts this CLI's tokens.", updated.Name)
			if app.Format() == output.Table {
				return nil
			}
			return AccessPoints.Show(app, updated)
		},
	}
}

func resolveAndGet(ctx context.Context, app *cmdutil.App, ref string) (AccessPoint, error) {
	id, err := AccessPoints.Resolve(ctx, app, ref)
	if err != nil {
		return AccessPoint{}, err
	}
	return AccessPoints.Get(ctx, app, id)
}

func setIssuers(ctx context.Context, app *cmdutil.App, id string, issuers []Issuer) (AccessPoint, error) {
	in, err := issuerInputs(issuers)
	if err != nil {
		return AccessPoint{}, err
	}
	return update(ctx, app, id, &gql.UpdateAccessPoint{Issuers: gql.Opt(in)})
}
