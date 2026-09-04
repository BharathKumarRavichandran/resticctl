package profile

import "slices"

// ResticCommand configures persistent arguments for one Restic command. The
// arguments are kept as a vector and are never interpreted by a shell.
type ResticCommand struct {
	Args []string `json:"args,omitempty"`
}

var supportedResticCommandPaths = []string{
	"backup", "cache", "cat", "check", "copy", "diff", "dump", "features", "find", "forget", "generate", "init",
	"key", "key add", "key list", "key passwd", "key remove", "list", "ls", "migrate", "mount", "prune",
	"rebuild-index", "recover", "repair", "repair index", "repair packs", "repair snapshots", "restore", "rewrite",
	"self-update", "snapshots", "stats", "tag", "unlock", "options", "version",
}

// IsSupportedResticCommand reports whether path is an explicitly supported
// top-level command or command/subcommand pair.
func IsSupportedResticCommand(path string) bool {
	return slices.Contains(supportedResticCommandPaths, path)
}
