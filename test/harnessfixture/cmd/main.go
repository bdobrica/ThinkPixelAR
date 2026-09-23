// Command cmd is a test fixture, excluded from production images and registries.
package main

import (
	"context"
	"github.com/bdobrica/ThinkPixelAR/test/harnessfixture"
	"os"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--handshake" {
		if (harnessfixture.Adapter{Path: os.Args[2]}).Expect(context.Background(), "hello fixture.v1", "fixture.v1") != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 2 || harnessfixture.Serve(os.Args[1], os.Stdout) != nil {
		os.Exit(1)
	}
}
