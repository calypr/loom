//go:build ignore

package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/fhir/semantic"
)

func main() {
	index, err := schema.GeneratedIndex()
	if err != nil {
		log.Fatal(err)
	}
	registry, err := semantic.GeneratedDatatypeRegistry()
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(registry.Coverage(index)); err != nil {
		log.Fatal(err)
	}
}
