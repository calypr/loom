package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"testing"
)

func buildCodecTestGraph(t *testing.T) *AvailabilityGraph {
	t.Helper()
	b := NewAvailabilityGraphBuilder(3, 2)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root", AuthResourcePath: "root-scope"}, "id")
	mustAddVertex(t, b, AvailabilityVertex{ID: "source", ResourceType: "Source", AuthResourcePath: "source-scope"}, "value")
	mustAddVertex(t, b, AvailabilityVertex{ID: "private", ResourceType: "Source", AuthResourcePath: "private-scope"}, "hidden")
	if err := b.AddFeature("source", AvailabilityFeature{
		Kind: "SEMANTIC", ResourceType: "Source", ConceptID: "concept-7", BindingID: "owner-2",
	}); err != nil {
		t.Fatalf("AddFeature(): %v", err)
	}
	mustAddEdge(t, b, "root", "source", "has", "a-denied")
	mustAddEdge(t, b, "root", "source", "has", "z-allowed")
	return mustFinish(t, b)
}

func encodeCodecGraph(t *testing.T, graph *AvailabilityGraph) []byte {
	t.Helper()
	var encoded bytes.Buffer
	written, err := graph.WriteTo(&encoded)
	if err != nil {
		t.Fatalf("WriteTo(): %v", err)
	}
	if written != int64(encoded.Len()) {
		t.Fatalf("WriteTo() count = %d, buffer length = %d", written, encoded.Len())
	}
	return encoded.Bytes()
}

func codecDecodeBudget(graph *AvailabilityGraph, encoded []byte) uint64 {
	budget := graph.Stats().Bytes + uint64(binary.LittleEndian.Uint32(encoded[12:16]))*4
	if uint64(len(encoded)) > budget {
		budget = uint64(len(encoded))
	}
	return budget
}

func recomputeCodecChecksum(encoded []byte) {
	checksum := sha256.Sum256(encoded[:len(encoded)-availabilityGraphCodecChecksumBytes])
	copy(encoded[len(encoded)-availabilityGraphCodecChecksumBytes:], checksum[:])
}

func TestAvailabilityGraphCodecRoundTripIsExactAndDeterministic(t *testing.T) {
	graph := buildCodecTestGraph(t)
	encoded := encodeCodecGraph(t, graph)
	if again := encodeCodecGraph(t, graph); !bytes.Equal(encoded, again) {
		t.Fatal("repeated writes of an immutable graph produced different bytes")
	}
	decoded, err := ReadAvailabilityGraph(bytes.NewReader(encoded), codecDecodeBudget(graph, encoded))
	if err != nil {
		t.Fatalf("ReadAvailabilityGraph(): %v", err)
	}
	if got, want := decoded.Stats(), graph.Stats(); got.Vertices != want.Vertices || got.Edges != want.Edges || got.Features != want.Features || got.Associations != want.Associations {
		t.Fatalf("round-trip counts = %+v, want %+v", got, want)
	}
	if decoded.Stats().Bytes == 0 || decoded.Stats().Bytes > codecDecodeBudget(graph, encoded) {
		t.Fatalf("decoded retained bytes = %d, outside decode budget", decoded.Stats().Bytes)
	}
	query := AvailabilityQuery{
		RootResourceType:  "Root",
		RootIDs:           []string{"root"},
		AuthResourcePaths: []string{"root-scope", "source-scope", "z-allowed"},
		Relations:         []AvailabilityRelation{outbound("Root", "Source", "has")},
	}
	want, err := graph.Find(t.Context(), query)
	if err != nil {
		t.Fatalf("Find(original): %v", err)
	}
	got, err := decoded.Find(t.Context(), query)
	if err != nil {
		t.Fatalf("Find(decoded): %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip witnesses differ:\noriginal: %#v\ndecoded:  %#v", want, got)
	}
	if len(got) != 3 {
		t.Fatalf("restricted round-trip returned %d witnesses, want root field and two source features: %#v", len(got), got)
	}
}

func TestAvailabilityGraphCodecRejectsCorruptionVersionTruncationAndBudget(t *testing.T) {
	graph := buildCodecTestGraph(t)
	encoded := encodeCodecGraph(t, graph)
	budget := codecDecodeBudget(graph, encoded)

	corrupt := append([]byte(nil), encoded...)
	corrupt[len(corrupt)-1] ^= 0x40
	if _, err := ReadAvailabilityGraph(bytes.NewReader(corrupt), budget); err == nil {
		t.Fatal("payload corruption passed checksum validation")
	}

	oldVersion := append([]byte(nil), encoded...)
	binary.LittleEndian.PutUint32(oldVersion[8:12], availabilityGraphCodecVersion-1)
	recomputeCodecChecksum(oldVersion)
	if _, err := ReadAvailabilityGraph(bytes.NewReader(oldVersion), budget); err == nil {
		t.Fatal("old codec version was accepted")
	}

	if _, err := ReadAvailabilityGraph(bytes.NewReader(encoded[:len(encoded)-1]), budget); err == nil {
		t.Fatal("truncated checksum was accepted")
	}
	if _, err := ReadAvailabilityGraph(bytes.NewReader(encoded), uint64(len(encoded)-1)); err == nil {
		t.Fatal("encoding larger than maxBytes was accepted")
	}
	if _, err := ReadAvailabilityGraph(bytes.NewReader(encoded), 0); err == nil {
		t.Fatal("zero byte budget was accepted")
	}

	oversizedCount := append([]byte(nil), encoded...)
	binary.LittleEndian.PutUint32(oversizedCount[12:16], ^uint32(0))
	recomputeCodecChecksum(oversizedCount)
	if _, err := ReadAvailabilityGraph(bytes.NewReader(oversizedCount), budget); err == nil {
		t.Fatal("unbounded string count was accepted")
	}
}

func TestAvailabilityGraphCodecRejectsOversizedStringClaimBeforeAllocation(t *testing.T) {
	graph := buildCodecTestGraph(t)
	encoded := append([]byte(nil), encodeCodecGraph(t, graph)...)
	firstLengthOffset := availabilityGraphCodecHeaderBytes
	binary.LittleEndian.PutUint32(encoded[firstLengthOffset:firstLengthOffset+4], ^uint32(0))
	recomputeCodecChecksum(encoded)
	if _, err := ReadAvailabilityGraph(bytes.NewReader(encoded), codecDecodeBudget(graph, encoded)); err == nil {
		t.Fatal("oversized aggregate string claim was accepted")
	}
}

func TestAvailabilityGraphCodecRejectsChecksummedInvalidOffsets(t *testing.T) {
	graph := buildCodecTestGraph(t)
	encoded := append([]byte(nil), encodeCodecGraph(t, graph)...)
	stringCount := uint64(binary.LittleEndian.Uint32(encoded[12:16]))
	vertexCount := uint64(binary.LittleEndian.Uint32(encoded[16:20]))
	arcCount := uint64(binary.LittleEndian.Uint32(encoded[20:24]))
	stringDataBytes := uint64(0)
	lengthsStart := uint64(availabilityGraphCodecHeaderBytes)
	for index := uint64(0); index < stringCount; index++ {
		lengthOffset := lengthsStart + index*4
		stringDataBytes += uint64(binary.LittleEndian.Uint32(encoded[lengthOffset : lengthOffset+4]))
	}
	verticesStart := uint64(availabilityGraphCodecHeaderBytes) + stringCount*4 + stringDataBytes
	offsetsStart := verticesStart + vertexCount*20
	terminalOffset := offsetsStart + vertexCount*4
	if arcCount == 0 {
		t.Fatal("codec fixture unexpectedly has no navigation arcs")
	}
	binary.LittleEndian.PutUint32(encoded[terminalOffset:terminalOffset+4], ^uint32(0))
	recomputeCodecChecksum(encoded)
	if _, err := ReadAvailabilityGraph(bytes.NewReader(encoded), codecDecodeBudget(graph, encoded)); err == nil {
		t.Fatal("checksummed non-monotonic terminal offset was accepted")
	}
}

func TestAvailabilityGraphCodecRejectsChecksummedInvalidOrdinal(t *testing.T) {
	graph := buildCodecTestGraph(t)
	encoded := append([]byte(nil), encodeCodecGraph(t, graph)...)
	stringCount := uint64(binary.LittleEndian.Uint32(encoded[12:16]))
	vertexCount := uint64(binary.LittleEndian.Uint32(encoded[16:20]))
	arcCount := uint64(binary.LittleEndian.Uint32(encoded[20:24]))
	if arcCount == 0 {
		t.Fatal("codec fixture unexpectedly has no navigation arcs")
	}
	stringDataBytes := uint64(0)
	for index := uint64(0); index < stringCount; index++ {
		lengthOffset := uint64(availabilityGraphCodecHeaderBytes) + index*4
		stringDataBytes += uint64(binary.LittleEndian.Uint32(encoded[lengthOffset : lengthOffset+4]))
	}
	verticesStart := uint64(availabilityGraphCodecHeaderBytes) + stringCount*4 + stringDataBytes
	offsetsStart := verticesStart + vertexCount*20
	arcsStart := offsetsStart + (vertexCount+1)*4
	binary.LittleEndian.PutUint32(encoded[arcsStart:arcsStart+4], ^uint32(0))
	recomputeCodecChecksum(encoded)
	if _, err := ReadAvailabilityGraph(bytes.NewReader(encoded), codecDecodeBudget(graph, encoded)); err == nil {
		t.Fatal("checksummed out-of-range arc target was accepted")
	}
}

func TestAvailabilityGraphCodecRoundTripsEmptyGraph(t *testing.T) {
	graph := mustFinish(t, NewAvailabilityGraphBuilder(0, 0))
	encoded := encodeCodecGraph(t, graph)
	decoded, err := ReadAvailabilityGraph(bytes.NewReader(encoded), codecDecodeBudget(graph, encoded))
	if err != nil {
		t.Fatalf("ReadAvailabilityGraph(empty): %v", err)
	}
	if got, want := decoded.Stats(), graph.Stats(); got.Vertices != want.Vertices || got.Edges != want.Edges || got.Features != want.Features || got.Associations != want.Associations {
		t.Fatalf("empty round-trip stats = %+v, want %+v", got, want)
	}
}
