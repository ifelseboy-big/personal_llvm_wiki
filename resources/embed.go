package resources

import "embed"

// FS contains managed vault content packs and optional AI client skills.
//
//go:embed vault-templates skills
var FS embed.FS
