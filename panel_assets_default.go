//go:build !panel_embed

package main

import "io/fs"

func resolvePanelAssets(_ string) fs.FS {
	return nil
}
