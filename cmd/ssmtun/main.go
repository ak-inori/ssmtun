// Command ssmtun manages supervised AWS SSM port-forwarding tunnels.
package main

import (
	"context"
	"os"

	"github.com/ak-inori/ssmtun/internal/cli"
)

func main() {
	os.Exit(cli.Execute(context.Background()))
}
