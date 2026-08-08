package main

import (
	"os"

	"llm-wiki/internal/app"
)

func main() {
	root := app.NewRootCommand()
	if err := root.Execute(); err != nil {
		code := app.RenderFailure(root, err)
		os.Exit(code)
	}
}
