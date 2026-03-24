package config

import (
	"github.com/osteele/liquid"

	"github.com/junhoyeo/contrabass/internal/types"
)

func RenderPrompt(template string, issue types.Issue) (string, error) {
	engine := liquid.NewEngine()
	engine.StrictVariables()

	bindings := map[string]any{
		"issue": map[string]any{
			"id":          issue.ID,
			"identifier":  issue.Identifier,
			"title":       issue.Title,
			"description": issue.Description,
			"url":         issue.URL,
		},
	}

	return engine.ParseAndRenderString(template, bindings)
}
