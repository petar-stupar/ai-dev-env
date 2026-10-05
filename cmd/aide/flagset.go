package main

import (
	"flag"
	"io"
)

type flagSet = flag.FlagSet

func newFS(name string) *flagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}
