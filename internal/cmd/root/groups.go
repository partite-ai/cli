package root

import (
	"github.com/partite-ai/partite-cli/internal/cmd/accesspoint"
	"github.com/partite-ai/partite-cli/internal/cmd/agent"
	"github.com/partite-ai/partite-cli/internal/cmd/apikey"
	"github.com/partite-ai/partite-cli/internal/cmd/boundary"
	"github.com/partite-ai/partite-cli/internal/cmd/clienttaskdef"
	"github.com/partite-ai/partite-cli/internal/cmd/conversation"
	"github.com/partite-ai/partite-cli/internal/cmd/embeddingprofile"
	"github.com/partite-ai/partite-cli/internal/cmd/eval"
	"github.com/partite-ai/partite-cli/internal/cmd/export"
	"github.com/partite-ai/partite-cli/internal/cmd/form"
	"github.com/partite-ai/partite-cli/internal/cmd/mcp"
	"github.com/partite-ai/partite-cli/internal/cmd/memory"
	"github.com/partite-ai/partite-cli/internal/cmd/mesh"
	"github.com/partite-ai/partite-cli/internal/cmd/modelprofile"
	"github.com/partite-ai/partite-cli/internal/cmd/modelprovider"
	"github.com/partite-ai/partite-cli/internal/cmd/modelset"
	"github.com/partite-ai/partite-cli/internal/cmd/particle"
	"github.com/partite-ai/partite-cli/internal/cmd/route"
	"github.com/partite-ai/partite-cli/internal/cmd/run"
	"github.com/partite-ai/partite-cli/internal/cmd/schedule"
	"github.com/partite-ai/partite-cli/internal/cmd/skill"
	"github.com/partite-ai/partite-cli/internal/cmd/trace"
	"github.com/partite-ai/partite-cli/internal/cmd/webhook"
	"github.com/partite-ai/partite-cli/internal/cmd/workspace"
)

// All is every command group. Help lists them by name within each section.
var All = []Group{
	run.New,
	run.NewChat,
	conversation.New,
	trace.New,
	mesh.New,
	route.New,
	accesspoint.New,
	boundary.New,
	webhook.New,
	agent.New,
	clienttaskdef.New,
	skill.New,
	mcp.New,
	particle.New,
	workspace.New,
	modelprovider.New,
	modelprofile.New,
	modelset.New,
	embeddingprofile.New,
	memory.New,
	eval.New,
	schedule.New,
	apikey.New,
	form.New,
	export.New,
}
