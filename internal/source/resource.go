package source

import "encoding/json"

type Resource struct {
	Project           string
	DatasetGeneration string
	ResourceType      string
	LogicalID         string
	AuthResourcePath  string
	Payload           json.RawMessage
}

type ResourceScope struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
}
