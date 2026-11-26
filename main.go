// Deprecated: use ./cmd/socratex
//
//   go build -o socratex ./cmd/socratex
//   ./socratex -config config.json
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "socratex v2: please build from ./cmd/socratex")
	fmt.Fprintln(os.Stderr, "  go build -o socratex ./cmd/socratex")
	fmt.Fprintln(os.Stderr, "  ./socratex -config config.json")
	os.Exit(1)
}
