package main

import (
	"fmt"
	"os"

	"mirrors/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}
