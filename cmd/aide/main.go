// Command aide manages isolated AI development environments (namespaces).
package main

import (
	"context"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// run is the testable entry point. Exit codes: 0 ok, 1 error, 2 usage.
func run(ctx context.Context, args []string, env func(string) string, stdin *os.File, stdout, stderr *os.File) int {
	fmt.Fprintln(stderr, "aide: not implemented")
	return 1
}
