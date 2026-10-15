package main

import (
	"os"

	"github.com/rosenhouse-bot-sandbox/lg/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.RealDeps()))
}
