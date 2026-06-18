package proto

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func Emit(event string, fields map[string]any) {
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
