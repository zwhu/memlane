package engine

import (
	"io/fs"

	"github.com/zwhu/memlane"
)

// BootstrapAssets contains pins, the dependency lock and lightweight adapters.
// Cognee and model binaries remain separately prepared external components.
var BootstrapAssets fs.FS = func() fs.FS {
	assets, err := fs.Sub(memlane.RuntimeAssets, "engine")
	if err != nil {
		panic(err)
	}
	return assets
}()
