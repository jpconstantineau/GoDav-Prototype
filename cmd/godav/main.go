package main

import (
	"github.com/jpconstantineau/GoDav-Prototype/internal/cli"
	"os"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr)) }
