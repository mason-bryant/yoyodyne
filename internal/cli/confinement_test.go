package cli

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
)

// The initialization writer is held to the same topology matrix as every other
// repository-confined writer, rather than to a set of cases of its own. It is
// the first thing that ever writes into a project, so a scaffold that landed
// outside is a configuration the operator was told the path of and cannot find,
// in a place nothing commits and nothing reviews.
func TestTheInitializationWriterIsConfinedToTheProject(t *testing.T) {
	t.Parallel()

	writertest.Run(t, writertest.Writer{
		Name:      "yoyo init",
		Directory: config.DirectoryName,
		File:      config.FileName,
		Write: func(t *testing.T, root string) error {
			_, _, err := initializeProject(root, "example-product", false)
			return err
		},
	})
}
