// Package particle uploads particles and manages installed particles: their
// credentials, workspaces and tools.
package particle

import (
	"context"
	"fmt"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/partite-ai/partite-cli/internal/cmd/workspace"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Installed is the view of an installed particle printed by get and read by
// -f. Credentials show only their name and method; the *_wo secrets are
// write-only.
type Installed struct {
	ID             string       `json:"id,omitempty"`
	Name           string       `json:"name"`
	ArtifactID     string       `json:"artifact_id"`
	ActiveArtifact *ArtifactRef `json:"active_artifact,omitempty"`
	Credentials    []Credential `json:"credentials,omitempty"`
	KVWorkspaceID  string       `json:"kv_workspace_id,omitempty"`
	Mounts         []Mount      `json:"mounts,omitempty"`
	CreatedAt      string       `json:"created_at,omitempty"`
	UpdatedAt      string       `json:"updated_at,omitempty"`
}

type ArtifactRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Mount struct {
	MountName   string `json:"mount_name"`
	WorkspaceID string `json:"workspace_id"`
}

func installedFromAPI(d gql.ToolsInstalledParticle) Installed {
	p := Installed{
		ID:             d.Id,
		Name:           d.Name,
		ArtifactID:     d.ActiveArtifact.Id,
		ActiveArtifact: &ArtifactRef{ID: d.ActiveArtifact.Id, Name: d.ActiveArtifact.Name, Version: d.ActiveArtifact.Version},
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
	}
	for _, c := range d.Credentials {
		p.Credentials = append(p.Credentials, Credential{Name: c.Name, Method: c.Method})
	}
	if d.KvWorkspace != nil {
		p.KVWorkspaceID = d.KvWorkspace.Id
	}
	for _, m := range d.Mounts {
		p.Mounts = append(p.Mounts, Mount{MountName: m.MountName, WorkspaceID: m.Workspace.Id})
	}
	return p
}

var Resource = &cmdutil.Resource[Installed]{
	WebList:  "tools/particles",
	WebPage:  "tools/particles/%s",
	WebLink:  "particle",
	Kind:     "installed particle",
	Command:  "particle",
	IDPrefix: "cipa",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Installed, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Installed], error) {
			resp, err := gql.ToolsListInstalledParticles(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Installed]{}, err
			}
			conn := resp.InstalledParticles
			items := make([]Installed, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = installedFromAPI(e.Node)
			}
			return cmdutil.Page[Installed]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Installed, error) {
		c, err := app.GQL()
		if err != nil {
			return Installed{}, err
		}
		resp, err := gql.ToolsGetInstalledParticle(ctx, c, id)
		if err != nil {
			return Installed{}, err
		}
		if resp.InstalledParticle == nil {
			return Installed{}, cmdutil.NotFound("installed particle", id, "partite particle list")
		}
		return installedFromAPI(*resp.InstalledParticle), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteInstalledParticle(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(p Installed) string { return p.ID },
	Name:    func(p Installed) string { return p.Name },
	Columns: []string{"id", "name", "artifact", "version", "credentials", "created"},
	Row: func(p Installed) []string {
		art, version := "", ""
		if a := p.ActiveArtifact; a != nil {
			art, version = a.Name, a.Version
		}
		return []string{p.ID, p.Name, art, version, formatCredentials(p.Credentials), cmdutil.FormatTime(p.CreatedAt)}
	},
	Fields: func(p Installed) []output.Field {
		art := p.ArtifactID
		if a := p.ActiveArtifact; a != nil {
			art = fmt.Sprintf("%s %s (%s)", a.Name, a.Version, a.ID)
		}
		mounts := make([]string, len(p.Mounts))
		for i, m := range p.Mounts {
			mounts[i] = m.MountName + " -> " + m.WorkspaceID
		}
		return []output.Field{
			{Label: "ID", Value: p.ID},
			{Label: "Name", Value: p.Name},
			{Label: "Artifact", Value: art},
			{Label: "Credentials", Value: formatCredentials(p.Credentials)},
			{Label: "KV workspace", Value: p.KVWorkspaceID},
			{Label: "Mounts", Value: strings.Join(mounts, "\n")},
			{Label: "Created", Value: p.CreatedAt},
			{Label: "Updated", Value: p.UpdatedAt},
		}
	},
}

func formatCredentials(creds []Credential) string {
	parts := make([]string, len(creds))
	for i, c := range creds {
		parts[i] = c.Name + "=" + c.Method
	}
	return strings.Join(parts, ", ")
}

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "particle",
		Aliases: []string{"particles"},
		Short:   "Upload, install and call particles",
		Long: `A particle is a packaged set of tools that runs inside Partite. Upload a
.particle file to get an artifact, then install the artifact under a name with
its credentials and workspaces. Agents use installed particles.

Credentials are write-only: they are sent to the server and never shown again.`,
	}
	cmd.AddCommand(
		newUpload(app),
		newArtifact(app),
		Resource.ListCmd(app),
		Resource.GetCmd(app),
		Resource.ViewCmd(app),
		newInstall(app),
		newUpdate(app),
		Resource.ApplyCmd(app, "  partite particle get weather -o yaml > weather.yaml\n  partite particle apply -f weather.yaml", func(ctx context.Context, spec Installed, id string) (Installed, error) {
			return apply(ctx, app, spec, id)
		}),
		uninstallCmd(app),
		newTools(app),
		newCall(app),
		newPing(app),
		newAuthorize(app),
	)
	return cmd
}

func uninstallCmd(app *cmdutil.App) *cobra.Command {
	cmd := Resource.DeleteCmd(app)
	cmd.Aliases = append(cmd.Aliases, "uninstall")
	cmd.Short = "Uninstall particles, with their credentials"
	cmd.Long = "Uninstall particles and delete their credentials. A particle that an agent uses cannot be uninstalled. The uploaded artifacts are kept."
	return cmd
}

// credentialInput adds oauth2ExchangeId, which gql.ParticleCredentialInput
// does not have yet.
type credentialInput struct {
	gql.ParticleCredentialInput
	OAuth2ExchangeID string `json:"oauth2ExchangeId,omitempty"`
}

type createInput struct {
	Name          string                          `json:"name"`
	ArtifactID    string                          `json:"artifactID"`
	Credentials   []credentialInput               `json:"credentials"`
	KvWorkspaceID *string                         `json:"kvWorkspaceID,omitempty"`
	Mounts        []gql.NewInstalledParticleMount `json:"mounts"`
}

type updateInput struct {
	InstalledParticleID string                           `json:"installedParticleID"`
	Name                *string                          `json:"name,omitempty"`
	ArtifactID          *string                          `json:"artifactID,omitempty"`
	Credentials         []credentialInput                `json:"credentials,omitempty"`
	KvWorkspaceID       *string                          `json:"kvWorkspaceID,omitempty"`
	Mounts              *[]gql.NewInstalledParticleMount `json:"mounts,omitempty"`
}

// send runs one of the generated particle mutations with our own input type.
func send(ctx context.Context, app *cmdutil.App, opName, query string, input, out any) error {
	c, err := app.GQL()
	if err != nil {
		return err
	}
	return c.MakeRequest(ctx,
		&graphql.Request{OpName: opName, Query: query, Variables: map[string]any{"input": input}},
		&graphql.Response{Data: out})
}

func createInstalled(ctx context.Context, app *cmdutil.App, in createInput) (Installed, error) {
	if in.Mounts == nil {
		in.Mounts = []gql.NewInstalledParticleMount{}
	}
	var resp gql.ToolsCreateInstalledParticleResponse
	if err := send(ctx, app, "ToolsCreateInstalledParticle", gql.ToolsCreateInstalledParticle_Operation, in, &resp); err != nil {
		return Installed{}, err
	}
	return installedFromAPI(resp.CreateInstalledParticle), nil
}

func updateInstalled(ctx context.Context, app *cmdutil.App, in updateInput) (Installed, error) {
	var resp gql.ToolsUpdateInstalledParticleResponse
	if err := send(ctx, app, "ToolsUpdateInstalledParticle", gql.ToolsUpdateInstalledParticle_Operation, in, &resp); err != nil {
		return Installed{}, err
	}
	return installedFromAPI(resp.UpdateInstalledParticle), nil
}

// resolveWorkspace accepts a workspace name or id.
func resolveWorkspace(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	return workspace.Resource.Resolve(ctx, app, ref)
}

func resolveMounts(ctx context.Context, app *cmdutil.App, mounts []Mount) ([]gql.NewInstalledParticleMount, error) {
	out := make([]gql.NewInstalledParticleMount, len(mounts))
	for i, m := range mounts {
		id, err := resolveWorkspace(ctx, app, m.WorkspaceID)
		if err != nil {
			return nil, err
		}
		out[i] = gql.NewInstalledParticleMount{MountName: m.MountName, WorkspaceID: id}
	}
	return out, nil
}

func parseMounts(flags []string) ([]Mount, error) {
	mounts := make([]Mount, len(flags))
	for i, f := range flags {
		name, ws, ok := strings.Cut(f, "=")
		if !ok || name == "" || ws == "" {
			return nil, cmdutil.UsageError("--mount must be mount-name=workspace, got %q", f)
		}
		mounts[i] = Mount{MountName: name, WorkspaceID: ws}
	}
	return mounts, nil
}

// credentialInputs returns the credentials that carry a secret. The others
// only name what is stored already, so they are left out.
func credentialInputs(creds []Credential) []credentialInput {
	var out []credentialInput
	for _, c := range creds {
		if in, ok := c.input(); ok {
			out = append(out, in)
		}
	}
	return out
}

func apply(ctx context.Context, app *cmdutil.App, spec Installed, id string) (Installed, error) {
	if spec.ArtifactID == "" {
		return Installed{}, cmdutil.UsageError("artifact_id is required")
	}
	artID, err := artifactID(ctx, app, spec.ArtifactID)
	if err != nil {
		return Installed{}, err
	}
	var kv *string
	if spec.KVWorkspaceID != "" {
		ws, err := resolveWorkspace(ctx, app, spec.KVWorkspaceID)
		if err != nil {
			return Installed{}, err
		}
		kv = &ws
	}
	mounts, err := resolveMounts(ctx, app, spec.Mounts)
	if err != nil {
		return Installed{}, err
	}
	if id == "" {
		return createInstalled(ctx, app, createInput{
			Name:          spec.Name,
			ArtifactID:    artID,
			Credentials:   credentialInputs(spec.Credentials),
			KvWorkspaceID: kv,
			Mounts:        mounts,
		})
	}
	cur, err := Resource.Get(ctx, app, id)
	if err != nil {
		return Installed{}, err
	}
	in := updateInput{
		InstalledParticleID: id,
		Name:                &spec.Name,
		Credentials:         credentialInputs(spec.Credentials),
		KvWorkspaceID:       kv,
	}
	if artID != cur.ArtifactID {
		in.ArtifactID = &artID
	}
	if spec.Mounts != nil || len(cur.Mounts) > 0 {
		in.Mounts = &mounts
	}
	return updateInstalled(ctx, app, in)
}

func newInstall(app *cmdutil.App) *cobra.Command {
	var file, artifact, kvWorkspace string
	var mountFlags []string
	var creds credentialFlags
	cmd := &cobra.Command{
		Use:     "install <name> --artifact <artifact-id-or-file>",
		Aliases: []string{"create"},
		Short:   "Install a particle under a name",
		Long: `Install a particle artifact under a name. --artifact takes an artifact id, or
a .particle file, which is uploaded first.

Credentials: pass --credential name=method with that method's secret flags, or
use -f for several. On a terminal, required credentials that are not passed
are asked for. For an OAuth2 method, either pass --client-id and
--client-secret (client-credentials grant) or run partite particle authorize
first and pass --oauth-exchange.

Run partite particle artifact get <id> to see the credentials, methods and
mounts an artifact declares.`,
		Example: `  partite particle install weather --artifact weather.particle
  partite particle install weather --artifact cpar_7Hq2... \
      --credential openweather=apikey --api-key-env OPENWEATHER_KEY
  partite particle install notes --artifact notes.particle \
      --kv-workspace notes --mount docs=shared-docs
  partite particle install crm -f crm.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var spec Installed
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			if artifact != "" {
				spec.ArtifactID = artifact
			}
			if spec.ArtifactID == "" {
				return cmdutil.UsageError("--artifact is required")
			}
			artID, err := artifactID(ctx, app, spec.ArtifactID)
			if err != nil {
				return err
			}
			spec.ArtifactID = artID
			art, err := getArtifact(ctx, app, artID)
			if err != nil {
				return err
			}
			if spec.Credentials, err = creds.merge(app, art, spec.Credentials, true); err != nil {
				return err
			}
			if kvWorkspace != "" {
				spec.KVWorkspaceID = kvWorkspace
			}
			if cmd.Flags().Changed("mount") {
				if spec.Mounts, err = parseMounts(mountFlags); err != nil {
					return err
				}
			}
			p, err := apply(ctx, app, spec, "")
			if err != nil {
				return err
			}
			app.Printer().Done("Installed particle %s (%s) from %s %s.", p.Name, p.ID, art.Name, art.Version)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, p)
		},
	}
	cmd.Flags().StringVar(&artifact, "artifact", "", "artifact id, or a .particle file to upload")
	cmd.Flags().StringVar(&kvWorkspace, "kv-workspace", "", "workspace that holds the particle's key-value state, when it uses one")
	cmd.Flags().StringArrayVar(&mountFlags, "mount", nil, "bind a mount the particle declares to a workspace, as mount-name=workspace; repeat for more")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the installed particle from a YAML or JSON file")
	creds.add(cmd)
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, artifact, kvWorkspace string
	var mountFlags []string
	var noMounts bool
	var creds credentialFlags
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change an installed particle's artifact, credentials or workspaces",
		Long: `Change an installed particle. Only what you pass changes. --artifact moves it
to another artifact, such as a new version; a .particle file is uploaded first.
--mount replaces all mounts.`,
		Example: `  partite particle update weather --artifact weather-1.1.particle
  partite particle update weather --credential openweather=apikey --api-key @key.txt
  partite particle update notes --mount docs=other-docs`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			f := cmd.Flags()
			if !f.Changed("name") && artifact == "" && kvWorkspace == "" && !f.Changed("mount") && !noMounts && !creds.given() {
				return cmdutil.UsageError("nothing to change; pass --name, --artifact, --credential, --kv-workspace or --mount")
			}
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			cur, err := Resource.Get(ctx, app, id)
			if err != nil {
				return err
			}
			in := updateInput{InstalledParticleID: id}
			if f.Changed("name") {
				in.Name = &name
			}
			artID := cur.ArtifactID
			if artifact != "" {
				if artID, err = artifactID(ctx, app, artifact); err != nil {
					return err
				}
				in.ArtifactID = &artID
			}
			if creds.given() {
				art, err := getArtifact(ctx, app, artID)
				if err != nil {
					return err
				}
				list, err := creds.merge(app, art, nil, false)
				if err != nil {
					return err
				}
				in.Credentials = credentialInputs(list)
			}
			if kvWorkspace != "" {
				ws, err := resolveWorkspace(ctx, app, kvWorkspace)
				if err != nil {
					return err
				}
				in.KvWorkspaceID = &ws
			}
			if noMounts {
				in.Mounts = &[]gql.NewInstalledParticleMount{}
			}
			if f.Changed("mount") {
				parsed, err := parseMounts(mountFlags)
				if err != nil {
					return err
				}
				mounts, err := resolveMounts(ctx, app, parsed)
				if err != nil {
					return err
				}
				in.Mounts = &mounts
			}
			p, err := updateInstalled(ctx, app, in)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated installed particle %s.", p.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, p)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&artifact, "artifact", "", "artifact id, or a .particle file to upload, to move the particle to")
	cmd.Flags().StringVar(&kvWorkspace, "kv-workspace", "", "workspace that holds the particle's key-value state")
	cmd.Flags().StringArrayVar(&mountFlags, "mount", nil, "bind a mount to a workspace, as mount-name=workspace, replacing all mounts; repeat for more")
	cmd.Flags().BoolVar(&noMounts, "no-mounts", false, "remove all mounts")
	creds.add(cmd)
	return cmd
}
