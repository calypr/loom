package ingest

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type EventSink func(event string, fields map[string]any)

func emitEvent(sink EventSink, event string, fields map[string]any) {
	if sink != nil {
		sink(event, fields)
		return
	}
	out := map[string]any{"event": event}
	for k, v := range fields {
		out[k] = v
	}
	if b, err := json.Marshal(out); err == nil {
		fmt.Fprintln(os.Stdout, string(b))
	}
}

func SecondsSince(start time.Time) float64 {
	return float64(time.Since(start).Milliseconds()) / 1000
}
