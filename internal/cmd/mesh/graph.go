package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
)

// Response is one reply the agent at the end of an input or link can send
// back. MessageType is a JSON schema; a string holding the schema is also
// accepted, as the terraform provider writes it.
type Response struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	MessageType json.RawMessage `json:"message_type"`
}

// Input is where a request enters a mesh: a name and the agent that takes it.
type Input struct {
	MeshID         string     `json:"mesh_id,omitempty"`
	Name           string     `json:"name"`
	AgentID        string     `json:"agent_id,omitempty"`
	AgentVersionID string     `json:"agent_version_id,omitempty"`
	Responses      []Response `json:"responses"`
	CreatedAt      string     `json:"created_at,omitempty"`
	UpdatedAt      string     `json:"updated_at,omitempty"`

	agentName    string
	agentOrdinal int
}

// Link lets one agent of a mesh send work to another. ID is the link's stable
// id, which stays the same across versions of the mesh.
type Link struct {
	ID                 string     `json:"id,omitempty"`
	MeshID             string     `json:"mesh_id,omitempty"`
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	FromAgentID        string     `json:"from_agent_id,omitempty"`
	FromAgentVersionID string     `json:"from_agent_version_id,omitempty"`
	ToAgentID          string     `json:"to_agent_id,omitempty"`
	ToAgentVersionID   string     `json:"to_agent_version_id,omitempty"`
	TransfersControl   bool       `json:"transfers_control"`
	Responses          []Response `json:"responses"`
	CreatedAt          string     `json:"created_at,omitempty"`
	UpdatedAt          string     `json:"updated_at,omitempty"`

	fromName, toName string
}

func inputFromAPI(v gql.MeshInputView) Input {
	in := Input{
		Name:           v.Name,
		AgentID:        v.AgentVersion.Agent.Id,
		AgentVersionID: v.AgentVersion.Id,
		Responses:      make([]Response, len(v.InputResponses)),
		CreatedAt:      v.CreatedAt,
		UpdatedAt:      v.UpdatedAt,
		agentName:      v.AgentVersion.Agent.Name,
		agentOrdinal:   v.AgentVersion.Ordinal,
	}
	for i, r := range v.InputResponses {
		in.Responses[i] = Response{Name: r.Name, Description: r.Description, MessageType: r.MessageType}
	}
	return in
}

func linkFromAPI(v gql.MeshLinkView) Link {
	l := Link{
		ID:                 v.StableID,
		Name:               v.Name,
		Description:        cmdutil.Deref(v.Description),
		FromAgentID:        v.FromAgentVersion.Agent.Id,
		FromAgentVersionID: v.FromAgentVersion.Id,
		ToAgentID:          v.ToAgentVersion.Agent.Id,
		ToAgentVersionID:   v.ToAgentVersion.Id,
		TransfersControl:   v.TransfersControl,
		Responses:          make([]Response, len(v.LinkResponses)),
		CreatedAt:          v.CreatedAt,
		UpdatedAt:          v.UpdatedAt,
		fromName:           v.FromAgentVersion.Agent.Name,
		toName:             v.ToAgentVersion.Agent.Name,
	}
	for i, r := range v.LinkResponses {
		l.Responses[i] = Response{Name: r.Name, Description: r.Description, MessageType: r.MessageType}
	}
	return l
}

// messageTypeText returns the schema as JSON text, unwrapping a schema given
// as a string.
func messageTypeText(owner string, r Response) (string, error) {
	raw := bytes.TrimSpace(r.MessageType)
	if len(raw) == 0 || string(raw) == "null" {
		return "", cmdutil.UsageError("response %q of %s has no message_type; give a JSON schema such as {type: object}", r.Name, owner)
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	return string(raw), nil
}

func inputResponsesToAPI(owner string, rs []Response) ([]gql.NewInputResponse, error) {
	out := make([]gql.NewInputResponse, len(rs))
	for i, r := range rs {
		mt, err := messageTypeText(owner, r)
		if err != nil {
			return nil, err
		}
		out[i] = gql.NewInputResponse{Name: r.Name, Description: r.Description, MessageType: mt}
	}
	return out, nil
}

func linkResponsesToAPI(owner string, rs []Response) ([]gql.NewLinkResponse, error) {
	out := make([]gql.NewLinkResponse, len(rs))
	for i, r := range rs {
		mt, err := messageTypeText(owner, r)
		if err != nil {
			return nil, err
		}
		out[i] = gql.NewLinkResponse{Name: r.Name, Description: r.Description, MessageType: mt}
	}
	return out, nil
}

// sameResponses compares two lists of responses by name, ignoring order and
// JSON formatting.
func sameResponses(a, b []Response) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(rs []Response) []Response {
		out := append([]Response(nil), rs...)
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	a, b = key(a), key(b)
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Description != b[i].Description {
			return false
		}
		ta, errA := messageTypeText("", a[i])
		tb, errB := messageTypeText("", b[i])
		if errA != nil || errB != nil {
			return false
		}
		var va, vb any
		if json.Unmarshal([]byte(ta), &va) != nil || json.Unmarshal([]byte(tb), &vb) != nil || !reflect.DeepEqual(va, vb) {
			return false
		}
	}
	return true
}

// agentRefs resolves agent names and versions, remembering what it looked up.
type agentRefs struct {
	app          *cmdutil.App
	byName       map[string]string
	latest       map[string]string
	versionAgent map[string]string
}

func newAgentRefs(app *cmdutil.App) *agentRefs {
	return &agentRefs{app: app, byName: map[string]string{}, latest: map[string]string{}, versionAgent: map[string]string{}}
}

func (r *agentRefs) agentID(ctx context.Context, ref string) (string, error) {
	if id, ok := r.byName[ref]; ok {
		return id, nil
	}
	c, err := r.app.GQL()
	if err != nil {
		return "", err
	}
	id, err := cmdutil.Resolve(ctx, ref, "cagt", "agent", "agent", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		return cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[cmdutil.Named], error) {
			resp, err := gql.MeshAgentList(ctx, c, first, after, &name)
			if err != nil {
				return cmdutil.Page[cmdutil.Named]{}, err
			}
			conn := resp.Agents
			items := make([]cmdutil.Named, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = cmdutil.Named{ID: e.Node.Id, Name: e.Node.Name}
			}
			return cmdutil.Page[cmdutil.Named]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	})
	if err != nil {
		return "", err
	}
	r.byName[ref] = id
	return id, nil
}

func (r *agentRefs) latestVersion(ctx context.Context, agentID string) (string, error) {
	if id, ok := r.latest[agentID]; ok {
		return id, nil
	}
	c, err := r.app.GQL()
	if err != nil {
		return "", err
	}
	resp, err := gql.MeshAgentLatestVersion(ctx, c, agentID)
	if err != nil {
		return "", err
	}
	if resp.Agent == nil {
		return "", cmdutil.NotFound("agent", agentID, "partite agent list")
	}
	r.latest[agentID] = resp.Agent.LatestVersion.Id
	return resp.Agent.LatestVersion.Id, nil
}

func (r *agentRefs) agentOfVersion(ctx context.Context, versionID string) (string, error) {
	if id, ok := r.versionAgent[versionID]; ok {
		return id, nil
	}
	if !cmdutil.IsID(versionID, "cagv") {
		return "", cmdutil.UsageError("%q is not an agent version id; they start with cagv_", versionID)
	}
	c, err := r.app.GQL()
	if err != nil {
		return "", err
	}
	resp, err := gql.GetAgentIDForVersion(ctx, c, versionID)
	if err != nil {
		return "", err
	}
	r.versionAgent[versionID] = resp.AgentVersion.Agent.Id
	return resp.AgentVersion.Agent.Id, nil
}

// end resolves one end of an input or link. The agent may be a name or id
// and may be left out when the version is given. The version is optional.
// It returns the agent id and the version as given.
func (r *agentRefs) end(ctx context.Context, what, agentRef, versionID string) (string, string, error) {
	if versionID != "" {
		owner, err := r.agentOfVersion(ctx, versionID)
		if err != nil {
			return "", "", err
		}
		if agentRef != "" {
			id, err := r.agentID(ctx, agentRef)
			if err != nil {
				return "", "", err
			}
			if id != owner {
				return "", "", cmdutil.UsageError("%s: agent version %s belongs to agent %s, not %s", what, versionID, owner, agentRef)
			}
		}
		return owner, versionID, nil
	}
	if agentRef == "" {
		return "", "", cmdutil.UsageError("%s needs an agent", what)
	}
	id, err := r.agentID(ctx, agentRef)
	return id, "", err
}

// versionChange says which agent version an end should move to, or "" to
// leave it. A pinned version wins. Otherwise an end that stays on the same
// agent keeps its version, and one that moves goes to the agent's latest.
func (r *agentRefs) versionChange(ctx context.Context, curAgent, curVersion, wantAgent, wantVersion string) (string, error) {
	if wantVersion != "" {
		if wantVersion == curVersion {
			return "", nil
		}
		return wantVersion, nil
	}
	if wantAgent == curAgent {
		return "", nil
	}
	return r.latestVersion(ctx, wantAgent)
}

// resolveInput returns in with AgentID resolved to an id.
func (r *agentRefs) resolveInput(ctx context.Context, in Input) (Input, error) {
	if in.Name == "" {
		return in, cmdutil.UsageError("an input has no name")
	}
	var err error
	in.AgentID, in.AgentVersionID, err = r.end(ctx, "input "+in.Name, in.AgentID, in.AgentVersionID)
	return in, err
}

// resolveLink returns l with both agent ids resolved.
func (r *agentRefs) resolveLink(ctx context.Context, l Link) (Link, error) {
	if l.Name == "" {
		return l, cmdutil.UsageError("a link has no name")
	}
	var err error
	l.FromAgentID, l.FromAgentVersionID, err = r.end(ctx, "link "+l.Name+" (from)", l.FromAgentID, l.FromAgentVersionID)
	if err != nil {
		return l, err
	}
	l.ToAgentID, l.ToAgentVersionID, err = r.end(ctx, "link "+l.Name+" (to)", l.ToAgentID, l.ToAgentVersionID)
	return l, err
}

// addInput adds a resolved input to the mesh's draft.
func addInput(ctx context.Context, app *cmdutil.App, meshID string, in Input) error {
	c, err := app.GQL()
	if err != nil {
		return err
	}
	responses, err := inputResponsesToAPI("input "+in.Name, in.Responses)
	if err != nil {
		return err
	}
	if _, err := gql.MeshInputAdd(ctx, c, meshID, &gql.NewInput{Name: in.Name, AgentID: in.AgentID, Responses: responses}); err != nil {
		return err
	}
	// The server puts a new input on the agent's latest version.
	if in.AgentVersionID != "" {
		_, err = gql.MeshInputUpdate(ctx, c, meshID, in.Name, &gql.UpdateInput{AgentVersionID: gql.Opt(in.AgentVersionID)})
	}
	return err
}

// changeInput updates cur to match the resolved want. It reports whether
// anything changed.
func changeInput(ctx context.Context, app *cmdutil.App, refs *agentRefs, meshID string, cur, want Input) (bool, error) {
	var u gql.UpdateInput
	if want.Name != cur.Name {
		u.Name = gql.Opt(want.Name)
	}
	v, err := refs.versionChange(ctx, cur.AgentID, cur.AgentVersionID, want.AgentID, want.AgentVersionID)
	if err != nil {
		return false, err
	}
	if v != "" {
		u.AgentVersionID = gql.Opt(v)
	}
	if !sameResponses(cur.Responses, want.Responses) {
		rs, err := inputResponsesToAPI("input "+want.Name, want.Responses)
		if err != nil {
			return false, err
		}
		u.Responses = gql.Opt(rs)
	}
	if !u.Name.Set && !u.AgentVersionID.Set && !u.Responses.Set {
		return false, nil
	}
	c, err := app.GQL()
	if err != nil {
		return false, err
	}
	_, err = gql.MeshInputUpdate(ctx, c, meshID, cur.Name, &u)
	return err == nil, err
}

// addLink adds a resolved link to the mesh's draft and returns its id.
func addLink(ctx context.Context, app *cmdutil.App, meshID string, l Link) (string, error) {
	c, err := app.GQL()
	if err != nil {
		return "", err
	}
	responses, err := linkResponsesToAPI("link "+l.Name, l.Responses)
	if err != nil {
		return "", err
	}
	in := &gql.NewLink{
		Name:             l.Name,
		Description:      l.Description,
		FromAgentID:      l.FromAgentID,
		ToAgentID:        l.ToAgentID,
		TransfersControl: l.TransfersControl,
		Responses:        responses,
	}
	if l.FromAgentVersionID != "" {
		in.FromAgentVersionID = gql.Opt(l.FromAgentVersionID)
	}
	if l.ToAgentVersionID != "" {
		in.ToAgentVersionID = gql.Opt(l.ToAgentVersionID)
	}
	resp, err := gql.MeshLinkAdd(ctx, c, meshID, in)
	if err != nil {
		return "", err
	}
	return resp.AddMeshLink.StableID, nil
}

// changeLink updates cur to match the resolved want. It reports whether
// anything changed.
func changeLink(ctx context.Context, app *cmdutil.App, refs *agentRefs, cur, want Link) (bool, error) {
	var u gql.UpdateLink
	changed := false
	if want.Name != cur.Name {
		u.Name, changed = gql.Opt(want.Name), true
	}
	if want.Description != cur.Description {
		u.Description, changed = gql.Opt(want.Description), true
	}
	if want.TransfersControl != cur.TransfersControl {
		u.TransfersControl, changed = gql.Opt(want.TransfersControl), true
	}
	from, err := refs.versionChange(ctx, cur.FromAgentID, cur.FromAgentVersionID, want.FromAgentID, want.FromAgentVersionID)
	if err != nil {
		return false, err
	}
	if from != "" {
		u.FromAgentVersionID, changed = gql.Opt(from), true
	}
	to, err := refs.versionChange(ctx, cur.ToAgentID, cur.ToAgentVersionID, want.ToAgentID, want.ToAgentVersionID)
	if err != nil {
		return false, err
	}
	if to != "" {
		u.ToAgentVersionID, changed = gql.Opt(to), true
	}
	if !sameResponses(cur.Responses, want.Responses) {
		rs, err := linkResponsesToAPI("link "+want.Name, want.Responses)
		if err != nil {
			return false, err
		}
		u.Responses, changed = gql.Opt(rs), true
	}
	if !changed {
		return false, nil
	}
	c, err := app.GQL()
	if err != nil {
		return false, err
	}
	_, err = gql.MeshLinkUpdate(ctx, c, cur.ID, &u)
	return err == nil, err
}

// reconcile makes the draft's inputs and links match the lists. A nil list
// leaves that part alone. Everything is resolved and checked before anything
// changes.
//
// The server needs the agent a link runs from to be reached by an input or
// another link, and when it loses that it deletes the agent's links. So
// inputs are added first, links are added from the inputs outwards, and
// removals come last.
func reconcile(ctx context.Context, app *cmdutil.App, cur Mesh, inputs *[]Input, links *[]Link) error {
	refs := newAgentRefs(app)
	c, err := app.GQL()
	if err != nil {
		return err
	}
	p := app.Printer()

	wantInputs := deref(cur.Inputs)
	if inputs != nil {
		wantInputs = nil
		seen := map[string]bool{}
		for _, in := range *inputs {
			r, err := refs.resolveInput(ctx, in)
			if err != nil {
				return err
			}
			if seen[r.Name] {
				return cmdutil.UsageError("two inputs are named %q", r.Name)
			}
			seen[r.Name] = true
			wantInputs = append(wantInputs, r)
		}
	}
	wantLinks := deref(cur.Links)
	if links != nil {
		wantLinks = nil
		for _, l := range *links {
			r, err := refs.resolveLink(ctx, l)
			if err != nil {
				return err
			}
			wantLinks = append(wantLinks, r)
		}
	}
	wantLinks, err = reachableOrder(wantInputs, wantLinks)
	if err != nil {
		return err
	}

	if inputs != nil {
		curByName := map[string]Input{}
		for _, in := range deref(cur.Inputs) {
			curByName[in.Name] = in
		}
		for _, want := range wantInputs {
			if old, ok := curByName[want.Name]; ok {
				changed, err := changeInput(ctx, app, refs, cur.ID, old, want)
				if err != nil {
					return fmt.Errorf("update input %s: %w", want.Name, err)
				}
				if changed {
					p.Done("Updated input %s.", want.Name)
				}
				continue
			}
			if err := addInput(ctx, app, cur.ID, want); err != nil {
				return fmt.Errorf("add input %s: %w", want.Name, err)
			}
			p.Done("Added input %s.", want.Name)
		}
	}

	if links != nil {
		curLinks := deref(cur.Links)
		taken := map[int]bool{}
		for _, want := range wantLinks {
			i := matchLink(curLinks, taken, want)
			if i >= 0 {
				taken[i] = true
				changed, err := changeLink(ctx, app, refs, curLinks[i], want)
				if err != nil {
					return fmt.Errorf("update link %s: %w", want.Name, err)
				}
				if changed {
					p.Done("Updated link %s.", want.Name)
				}
				continue
			}
			if _, err := addLink(ctx, app, cur.ID, want); err != nil {
				return fmt.Errorf("add link %s: %w", want.Name, err)
			}
			p.Done("Added link %s.", want.Name)
		}
		for i, l := range curLinks {
			if taken[i] {
				continue
			}
			if _, err := gql.DeleteLink(ctx, c, l.ID); err != nil {
				// Removing an earlier link may have removed this one with it.
				now, getErr := Meshes.Get(ctx, app, cur.ID)
				if getErr != nil || hasLink(deref(now.Links), l.ID) {
					return fmt.Errorf("remove link %s: %w", l.Name, err)
				}
			}
			p.Done("Removed link %s.", l.Name)
		}
	}

	if inputs != nil {
		for _, in := range deref(cur.Inputs) {
			if containsInput(wantInputs, in.Name) {
				continue
			}
			if _, err := gql.DeleteInput(ctx, c, cur.ID, in.Name); err != nil {
				return fmt.Errorf("remove input %s: %w", in.Name, err)
			}
			p.Done("Removed input %s.", in.Name)
		}
	}
	return nil
}

// reachableOrder returns the links in an order where each one runs from an
// agent that an input or an earlier link reaches. It fails when a link runs
// from an agent nothing reaches, which the server does not allow.
func reachableOrder(inputs []Input, links []Link) ([]Link, error) {
	reached := map[string]bool{}
	for _, in := range inputs {
		reached[in.AgentID] = true
	}
	var ordered []Link
	done := make([]bool, len(links))
	for progress := true; progress; {
		progress = false
		for i, l := range links {
			if !done[i] && reached[l.FromAgentID] {
				done[i], progress = true, true
				reached[l.ToAgentID] = true
				ordered = append(ordered, l)
			}
		}
	}
	for i, l := range links {
		if !done[i] {
			return nil, cmdutil.UsageError("link %s runs from agent %s, which no input or other link reaches; every link must start at an agent the mesh can reach", l.Name, l.FromAgentID)
		}
	}
	return ordered, nil
}

func hasLink(links []Link, id string) bool {
	for _, l := range links {
		if l.ID == id {
			return true
		}
	}
	return false
}

func containsInput(ins []Input, name string) bool {
	for _, in := range ins {
		if in.Name == name {
			return true
		}
	}
	return false
}

// matchLink finds the current link a wanted one stands for: the one with its
// id, or else the one with the same name from the same agent. It returns -1
// for a new link.
func matchLink(cur []Link, taken map[int]bool, want Link) int {
	if want.ID != "" {
		for i, l := range cur {
			if !taken[i] && l.ID == want.ID {
				return i
			}
		}
	}
	for i, l := range cur {
		if !taken[i] && l.Name == want.Name && l.FromAgentID == want.FromAgentID {
			return i
		}
	}
	return -1
}

func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

func formatResponses(rs []Response) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

func agentLabel(name, id string, ordinal int) string {
	if name == "" {
		return id
	}
	if ordinal > 0 {
		return fmt.Sprintf("%s v%d", name, ordinal)
	}
	return name
}
