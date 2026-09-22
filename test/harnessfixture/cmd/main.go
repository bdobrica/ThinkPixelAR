// Command cmd is a test fixture, excluded from production images and registries.
package main

import (
	"github.com/bdobrica/ThinkPixelAR/test/harnessfixture"
	"os"
)

func main() {
	if len(os.Args) != 2 || harnessfixture.Serve(os.Args[1], os.Stdout) != nil {
		os.Exit(1)
	}
}
