package graphql

// EditSkillFiles changes some files of a skill and leaves the rest.
type EditSkillFiles struct {
	Put    []SkillSourceFile `json:"put,omitempty"`
	Remove []string          `json:"remove,omitempty"`
}
