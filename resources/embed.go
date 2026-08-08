package resources

import "embed"

// FS contains versioned vault templates and optional AI client skills.
//
//go:embed vault-templates skills
var FS embed.FS
