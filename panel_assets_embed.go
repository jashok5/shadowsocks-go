//go:build panel_embed

package main

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed panel/dist
var panelEmbedded embed.FS

func resolvePanelAssets(mode string) fs.FS {
	if strings.ToLower(strings.TrimSpace(mode)) != "prod" {
		return nil
	}
	sub, err := fs.Sub(panelEmbedded, "panel/dist")
	if err != nil {
		return nil
	}
	return sub
}
