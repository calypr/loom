package querysvc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"arangodb-proto/internal/dbio"
	"arangodb-proto/internal/store"
)

const (
	edgeCollection              = "fhir_edge"
	patientFileRollupCollection = "patient_file_rollup"
	scalarIndexCollection       = "fhir_scalar_index"
)

func openBackend(ctx context.Context, opts dbio.ConnectionOptions) (store.Backend, error) {
	return dbio.OpenBackend(ctx, opts)
}

func emit(event string, fields map[string]any) {
	payload := map[string]any{"event": event}
	for key, value := range fields {
		payload[key] = value
	}
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Fprintln(os.Stdout, string(data))
}

func secondsSince(start time.Time) float64 {
	return time.Since(start).Seconds()
}

func helperBootstrapSpec(collections []store.CollectionSpec, truncate bool) store.BootstrapSpec {
	for i := range collections {
		collections[i].Truncate = truncate
	}
	return store.BootstrapSpec{Collections: collections}
}

func writeJSONLine(writer *bufio.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	return writer.WriteByte('\n')
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
