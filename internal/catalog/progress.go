package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

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
