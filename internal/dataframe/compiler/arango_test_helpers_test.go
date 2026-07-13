package compiler

import "os"

func compilerArangoTarget() (string, string, string) {
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	return url, database, project
}
