package recipient

import (
	"embed"
	"io/fs"
)

// embeddedDist contains the built recipient SPA. CI and make verify build
// web/recipient/dist before Go package loading so generated assets stay out of git.
//
//go:embed dist
var embeddedDist embed.FS

func DistFS() fs.FS {
	dist, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		panic(err)
	}
	return dist
}
