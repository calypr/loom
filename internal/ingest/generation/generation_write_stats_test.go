package generation

import (
	"testing"
	"time"
)

func TestGenerationWriteStatsSummarizesRequestsByClass(t *testing.T) {
	stats := &generationWriteStats{samples: make(map[string][]generationWriteSample)}
	for i := 20; i > 0; i-- {
		stats.record("edge", generationWriteSample{bytes: 10, permitWait: time.Second, duration: time.Duration(i) * time.Second})
	}
	stats.record("vertex", generationWriteSample{bytes: 3, duration: time.Second})
	summary := stats.summary()
	edges := summary["edge"]
	if edges["requests"] != 20 || edges["request_bytes"] != int64(200) ||
		edges["writer_permit_wait_seconds"] != 20.0 || edges["request_seconds"] != 210.0 ||
		edges["p95_request_seconds"] != 19.0 {
		t.Fatalf("unexpected edge write statistics: %v", edges)
	}
	if vertices := summary["vertex"]; vertices["requests"] != 1 || vertices["request_bytes"] != int64(3) {
		t.Fatalf("unexpected vertex write statistics: %v", vertices)
	}
}
