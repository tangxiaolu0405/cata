package main

import (
	"fmt"
	"os"

	"cata/internal/cata/brain"
)

func main() {
	if err := brain.EnsureCataLayout(); err != nil {
		fmt.Fprintf(os.Stderr, "layout: %v\n", err)
		os.Exit(1)
	}
	brain.MigrateAllLearningFragments()
	brain.MigrateAllLongMemoryCompact()
	fmt.Println("long memory migration + compact done")
}
