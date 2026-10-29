package graphql

import "encoding/json"

// UpdateSchedule is written by hand so a field can be cleared with null. The
// API leaves out what is not set and clears what is set to null.
type UpdateSchedule struct {
	Name                  Optional[string]          `json:"name,omitzero"`
	Description           Optional[string]          `json:"description,omitzero"`
	MeshID                Optional[string]          `json:"meshID,omitzero"`
	MeshVersionLabel      Optional[string]          `json:"meshVersionLabel,omitzero"`
	RouteID               Optional[string]          `json:"routeID,omitzero"`
	InputName             Optional[string]          `json:"inputName,omitzero"`
	Input                 Optional[json.RawMessage] `json:"input,omitzero"`
	ConversationTags      Optional[[]*Tag]          `json:"conversationTags,omitzero"`
	NotificationChannelID Optional[string]          `json:"notificationChannelID,omitzero"`
	CronExpression        Optional[string]          `json:"cronExpression,omitzero"`
	Enabled               Optional[bool]            `json:"enabled,omitzero"`
}
