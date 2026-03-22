package main

import "fmt"

var (
	version   = "v0.0.7"
	commit    = "unknown"
	buildTime = "unknown"
)

func versionString() string {
	return fmt.Sprintf("version=%s commit=%s build_time=%s", version, commit, buildTime)
}
