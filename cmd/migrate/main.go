// Command migrate is the only supported entry point for PostgreSQL schema
// changes. The migration engine and first schema migration are added by DB-001.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const usageText = `Usage: migrate <command>

ThinkPixelAR PostgreSQL migrations run only through this explicit command.
API replicas never migrate automatically.

Commands will be enabled by DB-001 when the first schema migration is added.
`

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = io.WriteString(stderr, usageText) }
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() == 0 {
		_, _ = io.WriteString(stdout, usageText)
		return 0
	}

	fmt.Fprintf(stderr, "migrate: command %q is not available until DB-001 adds the migration framework and first schema\n", flags.Arg(0))
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
