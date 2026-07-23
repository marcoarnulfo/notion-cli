// Command notion-track keeps a Notion task database in sync.
package main

import (
	"os"

	"github.com/marcoarnulfo/notion-cli/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
