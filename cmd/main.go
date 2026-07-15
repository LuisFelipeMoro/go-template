// main.go
package main

import (
	"os"

	"github.com/luisfelipecoelho/go-template/internal/cli"
)

func main() {
	// cobra has already written the error to stderr; main only sets the exit code.
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
