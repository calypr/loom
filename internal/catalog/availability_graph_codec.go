package catalog

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"strings"
	"unsafe"
)

const (
	availabilityGraphCodecVersion       uint32 = 1
	availabilityGraphCodecHeaderBytes          = 8 + 4 + 6*4
	availabilityGraphCodecChecksumBytes        = sha256.Size
	availabilityGraphCodecChunkBytes           = 64 * 1024
)

var availabilityGraphCodecMagic = [8]byte{'L', 'O', 'O', 'M', 'A', 'V', 'G', 0}

// WriteTo writes a deterministic versioned encoding of g. The checksum covers
// the header and payload; array values are little-endian uint32s.
func (g *AvailabilityGraph) WriteTo(w io.Writer) (int64, error) {
	if g == nil {
		return 0, errors.New("availability graph is nil")
	}
	if w == nil {
		return 0, errors.New("availability graph writer is nil")
	}
	if err := validateAvailabilityGraph(g); err != nil {
		return 0, fmt.Errorf("write availability graph: %w", err)
	}
	counts, err := graphCodecCountsForWrite(g)
	if err != nil {
		return 0, err
	}
	encodedBytes, err := counts.encodedBytes(g.strings)
	if err != nil {
		return 0, err
	}
	if encodedBytes > math.MaxInt64 {
		return 0, errors.New("availability graph encoding exceeds int64 byte count")
	}

	encoder := graphCodecEncoder{writer: w, digest: sha256.New()}
	var header [availabilityGraphCodecHeaderBytes]byte
	copy(header[:8], availabilityGraphCodecMagic[:])
	binary.LittleEndian.PutUint32(header[8:12], availabilityGraphCodecVersion)
	headerCounts := [...]uint32{
		counts.strings, counts.vertices, counts.arcs,
		counts.relations, counts.features, counts.associations,
	}
	for index, value := range headerCounts {
		binary.LittleEndian.PutUint32(header[12+index*4:], value)
	}
	if err := encoder.writePayload(header[:]); err != nil {
		return encoder.written, err
	}
	if err := encoder.writeWords(len(g.strings), 1, func(row, _ int) uint32 {
		return uint32(len(g.strings[row]))
	}); err != nil {
		return encoder.written, err
	}
	if err := encoder.writeStringData(g.strings); err != nil {
		return encoder.written, err
	}
	if err := encoder.writeWords(len(g.vertices), 5, func(row, column int) uint32 {
		vertex := g.vertices[row]
		switch column {
		case 0:
			return vertex.id
		case 1:
			return vertex.resourceType
		case 2:
			return vertex.authPath
		case 3:
			return vertex.featureStart
		default:
			return vertex.featureEnd
		}
	}); err != nil {
		return encoder.written, err
	}
	if err := encoder.writeWords(len(g.edgeOffsets), 1, func(row, _ int) uint32 {
		return g.edgeOffsets[row]
	}); err != nil {
		return encoder.written, err
	}
	if err := encoder.writeWords(len(g.arcs), 3, func(row, column int) uint32 {
		arc := g.arcs[row]
		switch column {
		case 0:
			return arc.target
		case 1:
			return arc.relation
		default:
			return arc.authPath
		}
	}); err != nil {
		return encoder.written, err
	}
	if err := encoder.writeWords(len(g.relations), 4, func(row, column int) uint32 {
		relation := g.relations[row]
		switch column {
		case 0:
			return relation.fromType
		case 1:
			return relation.toType
		case 2:
			return relation.relationship
		default:
			return relation.direction
		}
	}); err != nil {
		return encoder.written, err
	}
	if err := encoder.writeWords(len(g.features), 5, func(row, column int) uint32 {
		feature := g.features[row]
		switch column {
		case 0:
			return feature.kind
		case 1:
			return feature.resourceType
		case 2:
			return feature.fieldPath
		case 3:
			return feature.conceptID
		default:
			return feature.bindingID
		}
	}); err != nil {
		return encoder.written, err
	}
	if err := encoder.writeWords(len(g.associationFeatureIDs), 1, func(row, _ int) uint32 {
		return g.associationFeatureIDs[row]
	}); err != nil {
		return encoder.written, err
	}
	if encoder.written+availabilityGraphCodecChecksumBytes != int64(encodedBytes) {
		return encoder.written, errors.New("availability graph encoder size accounting mismatch")
	}
	if err := encoder.writeTrailer(encoder.digest.Sum(nil)); err != nil {
		return encoder.written, err
	}
	return encoder.written, nil
}

// ReadAvailabilityGraph decodes one graph. maxBytes bounds both encoded input
// and the estimated graph backing plus the temporary string-length table.
// A zero budget rejects every encoding.
func ReadAvailabilityGraph(r io.Reader, maxBytes uint64) (*AvailabilityGraph, error) {
	if r == nil {
		return nil, errors.New("availability graph reader is nil")
	}
	if maxBytes < availabilityGraphCodecHeaderBytes+availabilityGraphCodecChecksumBytes {
		return nil, errors.New("availability graph byte budget is too small")
	}

	buffered := bufio.NewReaderSize(r, availabilityGraphCodecChunkBytes)
	decoder := graphCodecDecoder{
		reader:     buffered,
		maxPayload: maxBytes - availabilityGraphCodecChecksumBytes,
		digest:     sha256.New(),
		scratch:    make([]byte, availabilityGraphCodecChunkBytes),
	}
	var header [availabilityGraphCodecHeaderBytes]byte
	if err := decoder.readPayload(header[:]); err != nil {
		return nil, err
	}
	if string(header[:8]) != string(availabilityGraphCodecMagic[:]) {
		return nil, errors.New("invalid availability graph encoding magic")
	}
	version := binary.LittleEndian.Uint32(header[8:12])
	if version != availabilityGraphCodecVersion {
		return nil, fmt.Errorf("unsupported availability graph encoding version %d", version)
	}
	counts := graphCodecCounts{
		strings:      binary.LittleEndian.Uint32(header[12:16]),
		vertices:     binary.LittleEndian.Uint32(header[16:20]),
		arcs:         binary.LittleEndian.Uint32(header[20:24]),
		relations:    binary.LittleEndian.Uint32(header[24:28]),
		features:     binary.LittleEndian.Uint32(header[28:32]),
		associations: binary.LittleEndian.Uint32(header[32:36]),
	}
	if err := counts.validateForRead(); err != nil {
		return nil, err
	}
	arrayBytes, err := counts.arrayBytes()
	if err != nil {
		return nil, err
	}
	lengthBytes := uint64(counts.strings) * 4
	minimumBytes, ok := checkedAdd(uint64(availabilityGraphCodecHeaderBytes), lengthBytes)
	if !ok {
		return nil, errors.New("availability graph size overflows uint64")
	}
	minimumBytes, ok = checkedAdd(minimumBytes, arrayBytes)
	if !ok {
		return nil, errors.New("availability graph size overflows uint64")
	}
	minimumBytes, ok = checkedAdd(minimumBytes, availabilityGraphCodecChecksumBytes)
	if !ok || minimumBytes > maxBytes {
		return nil, errors.New("availability graph declared arrays exceed byte budget")
	}
	if err := counts.validateAllocations(); err != nil {
		return nil, err
	}
	retainedWithoutStrings, err := counts.retainedBytes(0)
	if err != nil {
		return nil, err
	}
	preflightBytes, ok := checkedAdd(retainedWithoutStrings, lengthBytes)
	if !ok || preflightBytes > maxBytes {
		return nil, errors.New("availability graph arrays and string-length table exceed byte budget")
	}

	stringLengths := make([]uint32, int(counts.strings))
	if err := decoder.readWords(len(stringLengths), 1, func(row int, words []uint32) {
		stringLengths[row] = words[0]
	}); err != nil {
		return nil, err
	}
	stringBytes := uint64(0)
	for _, length := range stringLengths {
		var addOK bool
		stringBytes, addOK = checkedAdd(stringBytes, uint64(length))
		if !addOK {
			return nil, errors.New("availability graph string data size overflows uint64")
		}
	}
	declaredBytes, ok := checkedAdd(uint64(availabilityGraphCodecHeaderBytes), lengthBytes)
	if !ok {
		return nil, errors.New("availability graph size overflows uint64")
	}
	declaredBytes, ok = checkedAdd(declaredBytes, stringBytes)
	if !ok {
		return nil, errors.New("availability graph size overflows uint64")
	}
	declaredBytes, ok = checkedAdd(declaredBytes, arrayBytes)
	if !ok {
		return nil, errors.New("availability graph size overflows uint64")
	}
	declaredBytes, ok = checkedAdd(declaredBytes, availabilityGraphCodecChecksumBytes)
	if !ok || declaredBytes > maxBytes {
		return nil, errors.New("availability graph declared string data exceeds byte budget")
	}
	if declaredBytes > math.MaxInt64 {
		return nil, errors.New("availability graph encoding exceeds int64 byte count")
	}
	if stringBytes > uint64(^uint(0)>>1) {
		return nil, errors.New("availability graph string pool does not fit this platform")
	}
	retainedBytes, err := counts.retainedBytes(stringBytes)
	if err != nil {
		return nil, err
	}
	decodePeakBytes, ok := checkedAdd(retainedBytes, lengthBytes)
	if !ok || decodePeakBytes > maxBytes {
		return nil, errors.New("availability graph retained arrays and decode table exceed byte budget")
	}

	var pool strings.Builder
	pool.Grow(int(stringBytes))
	remainingStringBytes := stringBytes
	for remainingStringBytes > 0 {
		chunkSize := len(decoder.scratch)
		if uint64(chunkSize) > remainingStringBytes {
			chunkSize = int(remainingStringBytes)
		}
		chunk := decoder.scratch[:chunkSize]
		if err := decoder.readPayload(chunk); err != nil {
			return nil, err
		}
		if _, err := pool.Write(chunk); err != nil {
			return nil, fmt.Errorf("read availability graph strings: %w", err)
		}
		remainingStringBytes -= uint64(chunkSize)
	}
	stringData := pool.String()
	graph := &AvailabilityGraph{
		strings:               make([]string, int(counts.strings)),
		vertices:              make([]graphVertex, int(counts.vertices)),
		edgeOffsets:           make([]uint32, int(counts.vertices)+1),
		arcs:                  make([]graphArc, int(counts.arcs)),
		relations:             make([]graphRelation, int(counts.relations)),
		features:              make([]featureIDs, int(counts.features)),
		associationFeatureIDs: make([]uint32, int(counts.associations)),
	}
	stringOffset := 0
	for index, length := range stringLengths {
		end := stringOffset + int(length)
		graph.strings[index] = stringData[stringOffset:end]
		stringOffset = end
	}
	if err := decoder.readWords(len(graph.vertices), 5, func(row int, words []uint32) {
		graph.vertices[row] = graphVertex{
			id: words[0], resourceType: words[1], authPath: words[2],
			featureStart: words[3], featureEnd: words[4],
		}
	}); err != nil {
		return nil, err
	}
	if err := decoder.readWords(len(graph.edgeOffsets), 1, func(row int, words []uint32) {
		graph.edgeOffsets[row] = words[0]
	}); err != nil {
		return nil, err
	}
	if err := decoder.readWords(len(graph.arcs), 3, func(row int, words []uint32) {
		graph.arcs[row] = graphArc{target: words[0], relation: words[1], authPath: words[2]}
	}); err != nil {
		return nil, err
	}
	if err := decoder.readWords(len(graph.relations), 4, func(row int, words []uint32) {
		graph.relations[row] = graphRelation{fromType: words[0], toType: words[1], relationship: words[2], direction: words[3]}
	}); err != nil {
		return nil, err
	}
	if err := decoder.readWords(len(graph.features), 5, func(row int, words []uint32) {
		graph.features[row] = featureIDs{
			kind: words[0], resourceType: words[1], fieldPath: words[2], conceptID: words[3], bindingID: words[4],
		}
	}); err != nil {
		return nil, err
	}
	if err := decoder.readWords(len(graph.associationFeatureIDs), 1, func(row int, words []uint32) {
		graph.associationFeatureIDs[row] = words[0]
	}); err != nil {
		return nil, err
	}
	if decoder.readBytes != uint64(availabilityGraphCodecHeaderBytes)+lengthBytes+stringBytes+arrayBytes {
		return nil, errors.New("availability graph decoder size accounting mismatch")
	}
	var expectedChecksum [availabilityGraphCodecChecksumBytes]byte
	if decoder.readBytes+availabilityGraphCodecChecksumBytes > maxBytes {
		return nil, errors.New("availability graph exceeds byte budget")
	}
	if _, err := io.ReadFull(buffered, expectedChecksum[:]); err != nil {
		return nil, fmt.Errorf("read availability graph checksum: %w", err)
	}
	if subtle.ConstantTimeCompare(decoder.digest.Sum(nil), expectedChecksum[:]) != 1 {
		return nil, errors.New("availability graph checksum mismatch")
	}
	if _, err := buffered.ReadByte(); err == nil {
		return nil, errors.New("availability graph has trailing data")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("check availability graph end: %w", err)
	}
	if err := validateAvailabilityGraph(graph); err != nil {
		return nil, fmt.Errorf("invalid availability graph encoding: %w", err)
	}
	graph.stats = AvailabilityGraphStats{
		Vertices:     len(graph.vertices),
		Edges:        len(graph.arcs) / 2,
		Features:     len(graph.features),
		Associations: len(graph.associationFeatureIDs),
	}
	graph.stats.Bytes = graph.retainedBytes()
	return graph, nil
}

type graphCodecCounts struct {
	strings, vertices, arcs, relations, features, associations uint32
}

func graphCodecCountsForWrite(graph *AvailabilityGraph) (graphCodecCounts, error) {
	lengths := []int{
		len(graph.strings), len(graph.vertices), len(graph.arcs), len(graph.relations),
		len(graph.features), len(graph.associationFeatureIDs),
	}
	for _, length := range lengths {
		if uint64(length) > uint64(maxVertexOrdinal) {
			return graphCodecCounts{}, errors.New("availability graph dimension exceeds uint32 codec limit")
		}
	}
	return graphCodecCounts{
		strings: uint32(len(graph.strings)), vertices: uint32(len(graph.vertices)),
		arcs: uint32(len(graph.arcs)), relations: uint32(len(graph.relations)),
		features: uint32(len(graph.features)), associations: uint32(len(graph.associationFeatureIDs)),
	}, nil
}

func (c graphCodecCounts) validateForRead() error {
	if c.vertices == maxVertexOrdinal {
		return errors.New("availability graph vertex count exceeds ordinal limit")
	}
	if c.arcs&1 != 0 {
		return errors.New("availability graph navigation arc count must be even")
	}
	return nil
}

func (c graphCodecCounts) arrayBytes() (uint64, error) {
	vertexBytes, ok := checkedMultiply(uint64(c.vertices), uint64(unsafe.Sizeof(graphVertex{})))
	if !ok {
		return 0, errors.New("availability graph vertex array size overflows uint64")
	}
	offsetBytes, ok := checkedMultiply(uint64(c.vertices)+1, uint64(unsafe.Sizeof(uint32(0))))
	if !ok {
		return 0, errors.New("availability graph offset array size overflows uint64")
	}
	arcBytes, ok := checkedMultiply(uint64(c.arcs), uint64(unsafe.Sizeof(graphArc{})))
	if !ok {
		return 0, errors.New("availability graph arc array size overflows uint64")
	}
	relationBytes, ok := checkedMultiply(uint64(c.relations), uint64(unsafe.Sizeof(graphRelation{})))
	if !ok {
		return 0, errors.New("availability graph relation array size overflows uint64")
	}
	featureBytes, ok := checkedMultiply(uint64(c.features), uint64(unsafe.Sizeof(featureIDs{})))
	if !ok {
		return 0, errors.New("availability graph feature array size overflows uint64")
	}
	associationBytes, ok := checkedMultiply(uint64(c.associations), uint64(unsafe.Sizeof(uint32(0))))
	if !ok {
		return 0, errors.New("availability graph association array size overflows uint64")
	}
	total := uint64(0)
	for _, size := range [...]uint64{vertexBytes, offsetBytes, arcBytes, relationBytes, featureBytes, associationBytes} {
		var addOK bool
		total, addOK = checkedAdd(total, size)
		if !addOK {
			return 0, errors.New("availability graph array size overflows uint64")
		}
	}
	return total, nil
}

func (c graphCodecCounts) validateAllocations() error {
	checks := []struct {
		count uint64
		size  uintptr
	}{
		{uint64(c.strings), unsafe.Sizeof("")},
		{uint64(c.vertices), unsafe.Sizeof(graphVertex{})},
		{uint64(c.vertices) + 1, unsafe.Sizeof(uint32(0))},
		{uint64(c.arcs), unsafe.Sizeof(graphArc{})},
		{uint64(c.relations), unsafe.Sizeof(graphRelation{})},
		{uint64(c.features), unsafe.Sizeof(featureIDs{})},
		{uint64(c.associations), unsafe.Sizeof(uint32(0))},
	}
	maxInt := uint64(^uint(0) >> 1)
	for _, check := range checks {
		if check.count > maxInt || check.count > maxInt/uint64(check.size) {
			return errors.New("availability graph array does not fit this platform")
		}
	}
	return nil
}

func (c graphCodecCounts) retainedBytes(stringBytes uint64) (uint64, error) {
	total := uint64(unsafe.Sizeof(AvailabilityGraph{}))
	parts := [...]struct {
		count uint64
		size  uintptr
	}{
		{uint64(c.strings), unsafe.Sizeof("")},
		{uint64(c.vertices), unsafe.Sizeof(graphVertex{})},
		{uint64(c.vertices) + 1, unsafe.Sizeof(uint32(0))},
		{uint64(c.arcs), unsafe.Sizeof(graphArc{})},
		{uint64(c.relations), unsafe.Sizeof(graphRelation{})},
		{uint64(c.features), unsafe.Sizeof(featureIDs{})},
		{uint64(c.associations), unsafe.Sizeof(uint32(0))},
	}
	for _, part := range parts {
		bytes, ok := checkedMultiply(part.count, uint64(part.size))
		if !ok {
			return 0, errors.New("availability graph retained allocation overflows uint64")
		}
		total, ok = checkedAdd(total, bytes)
		if !ok {
			return 0, errors.New("availability graph retained allocation overflows uint64")
		}
	}
	total, ok := checkedAdd(total, stringBytes)
	if !ok {
		return 0, errors.New("availability graph retained allocation overflows uint64")
	}
	return total, nil
}

func (c graphCodecCounts) encodedBytes(values []string) (uint64, error) {
	arrayBytes, err := c.arrayBytes()
	if err != nil {
		return 0, err
	}
	total := uint64(availabilityGraphCodecHeaderBytes + availabilityGraphCodecChecksumBytes)
	lengthBytes, ok := checkedMultiply(uint64(c.strings), 4)
	if !ok {
		return 0, errors.New("availability graph string length table overflows uint64")
	}
	for _, size := range [...]uint64{lengthBytes, arrayBytes} {
		total, ok = checkedAdd(total, size)
		if !ok {
			return 0, errors.New("availability graph encoding size overflows uint64")
		}
	}
	for _, value := range values {
		if uint64(len(value)) > uint64(maxVertexOrdinal) {
			return 0, errors.New("availability graph string exceeds uint32 codec limit")
		}
		total, ok = checkedAdd(total, uint64(len(value)))
		if !ok {
			return 0, errors.New("availability graph encoding size overflows uint64")
		}
	}
	return total, nil
}

type graphCodecEncoder struct {
	writer  io.Writer
	digest  hash.Hash
	written int64
	buffer  []byte
}

func (e *graphCodecEncoder) writePayload(data []byte) error {
	return e.writeAll(data, true)
}

func (e *graphCodecEncoder) writeTrailer(data []byte) error {
	return e.writeAll(data, false)
}

func (e *graphCodecEncoder) writeAll(data []byte, hashPayload bool) error {
	for len(data) > 0 {
		written, err := e.writer.Write(data)
		if written < 0 || written > len(data) {
			return errors.New("availability graph writer returned an invalid byte count")
		}
		if written > 0 {
			if hashPayload {
				_, _ = e.digest.Write(data[:written])
			}
			e.written += int64(written)
			data = data[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (e *graphCodecEncoder) writeWords(rows, width int, wordAt func(row, column int) uint32) error {
	if len(e.buffer) == 0 {
		e.buffer = make([]byte, availabilityGraphCodecChunkBytes)
	}
	rowsPerChunk := len(e.buffer) / (width * 4)
	for start := 0; start < rows; {
		rowCount := rows - start
		if rowCount > rowsPerChunk {
			rowCount = rowsPerChunk
		}
		byteCount := rowCount * width * 4
		for row := 0; row < rowCount; row++ {
			for column := 0; column < width; column++ {
				binary.LittleEndian.PutUint32(e.buffer[(row*width+column)*4:], wordAt(start+row, column))
			}
		}
		if err := e.writePayload(e.buffer[:byteCount]); err != nil {
			return err
		}
		start += rowCount
	}
	return nil
}

func (e *graphCodecEncoder) writeStringData(values []string) error {
	if len(e.buffer) == 0 {
		e.buffer = make([]byte, availabilityGraphCodecChunkBytes)
	}
	used := 0
	for _, value := range values {
		for len(value) > 0 {
			copied := copy(e.buffer[used:], value)
			used += copied
			value = value[copied:]
			if used == len(e.buffer) {
				if err := e.writePayload(e.buffer[:used]); err != nil {
					return err
				}
				used = 0
			}
		}
	}
	if used > 0 {
		return e.writePayload(e.buffer[:used])
	}
	return nil
}

type graphCodecDecoder struct {
	reader     io.Reader
	maxPayload uint64
	readBytes  uint64
	digest     hash.Hash
	scratch    []byte
	words      [5]uint32
}

func (d *graphCodecDecoder) readPayload(data []byte) error {
	if uint64(len(data)) > d.maxPayload-d.readBytes {
		return errors.New("availability graph payload exceeds byte budget")
	}
	n, err := io.ReadFull(d.reader, data)
	if n > 0 {
		_, _ = d.digest.Write(data[:n])
		d.readBytes += uint64(n)
	}
	if err != nil {
		return fmt.Errorf("read availability graph payload: %w", err)
	}
	return nil
}

func (d *graphCodecDecoder) readWords(rows, width int, consume func(row int, words []uint32)) error {
	rowsPerChunk := len(d.scratch) / (width * 4)
	for start := 0; start < rows; {
		rowCount := rows - start
		if rowCount > rowsPerChunk {
			rowCount = rowsPerChunk
		}
		byteCount := rowCount * width * 4
		if err := d.readPayload(d.scratch[:byteCount]); err != nil {
			return err
		}
		for row := 0; row < rowCount; row++ {
			for column := 0; column < width; column++ {
				d.words[column] = binary.LittleEndian.Uint32(d.scratch[(row*width+column)*4:])
			}
			consume(start+row, d.words[:width])
		}
		start += rowCount
	}
	return nil
}

func validateAvailabilityGraph(graph *AvailabilityGraph) error {
	if graph == nil {
		return errors.New("availability graph is nil")
	}
	if len(graph.edgeOffsets) != len(graph.vertices)+1 {
		return errors.New("availability graph offset count does not match vertices")
	}
	if len(graph.arcs)&1 != 0 {
		return errors.New("availability graph navigation arc count must be even")
	}
	for ordinal, vertex := range graph.vertices {
		if !validStringID(vertex.id, graph.strings) || !validStringID(vertex.resourceType, graph.strings) || !validStringID(vertex.authPath, graph.strings) {
			return fmt.Errorf("vertex %d has an invalid string index", ordinal)
		}
		if ordinal > 0 && graph.strings[graph.vertices[ordinal-1].id] >= graph.strings[vertex.id] {
			return fmt.Errorf("vertex IDs are not sorted and unique at ordinal %d", ordinal)
		}
	}
	for id, relation := range graph.relations {
		if !validStringID(relation.fromType, graph.strings) || !validStringID(relation.toType, graph.strings) || !validStringID(relation.relationship, graph.strings) || !validStringID(relation.direction, graph.strings) {
			return fmt.Errorf("relation %d has an invalid string index", id)
		}
		direction := graph.strings[relation.direction]
		if direction != "OUTBOUND" && direction != "INBOUND" {
			return fmt.Errorf("relation %d has invalid storage direction %q", id, direction)
		}
		if id > 0 && !relationLess(graph.relations[id-1], relation, graph.strings) {
			return fmt.Errorf("relations are not sorted and unique at index %d", id)
		}
	}
	for id, feature := range graph.features {
		if !validStringID(feature.kind, graph.strings) || !validStringID(feature.resourceType, graph.strings) || !validStringID(feature.fieldPath, graph.strings) || !validStringID(feature.conceptID, graph.strings) || !validStringID(feature.bindingID, graph.strings) {
			return fmt.Errorf("feature %d has an invalid string index", id)
		}
		if id > 0 && !featureLess(graph.features[id-1], feature, graph.strings) {
			return fmt.Errorf("features are not sorted and unique at index %d", id)
		}
	}
	if len(graph.edgeOffsets) == 0 || graph.edgeOffsets[0] != 0 {
		return errors.New("availability graph offsets do not start at zero")
	}
	for index := 1; index < len(graph.edgeOffsets); index++ {
		if graph.edgeOffsets[index] < graph.edgeOffsets[index-1] || uint64(graph.edgeOffsets[index]) > uint64(len(graph.arcs)) {
			return fmt.Errorf("availability graph offsets are invalid at index %d", index)
		}
	}
	if uint64(graph.edgeOffsets[len(graph.edgeOffsets)-1]) != uint64(len(graph.arcs)) {
		return errors.New("availability graph terminal edge offset does not match arc count")
	}
	for vertexID, vertex := range graph.vertices {
		if vertex.featureStart > vertex.featureEnd || uint64(vertex.featureEnd) > uint64(len(graph.associationFeatureIDs)) {
			return fmt.Errorf("vertex %d has invalid feature association bounds", vertexID)
		}
		if vertexID == 0 && vertex.featureStart != 0 {
			return errors.New("availability graph associations do not start at zero")
		}
		if vertexID > 0 && vertex.featureStart != graph.vertices[vertexID-1].featureEnd {
			return fmt.Errorf("vertex %d association range is not contiguous", vertexID)
		}
		for association := vertex.featureStart; association < vertex.featureEnd; association++ {
			featureID := graph.associationFeatureIDs[association]
			if uint64(featureID) >= uint64(len(graph.features)) {
				return fmt.Errorf("vertex %d references invalid feature %d", vertexID, featureID)
			}
			if graph.strings[graph.features[featureID].resourceType] != graph.strings[vertex.resourceType] {
				return fmt.Errorf("vertex %d has a feature with mismatched resource type", vertexID)
			}
			if association > vertex.featureStart && graph.associationFeatureIDs[association-1] >= featureID {
				return fmt.Errorf("vertex %d feature associations are not sorted and unique", vertexID)
			}
		}
	}
	if len(graph.vertices) == 0 && len(graph.associationFeatureIDs) != 0 {
		return errors.New("availability graph has associations without vertices")
	}
	if len(graph.vertices) > 0 && uint64(graph.vertices[len(graph.vertices)-1].featureEnd) != uint64(len(graph.associationFeatureIDs)) {
		return errors.New("availability graph terminal feature range does not match association count")
	}
	for vertexID := range graph.vertices {
		start, end := graph.edgeOffsets[vertexID], graph.edgeOffsets[vertexID+1]
		for arcID := start; arcID < end; arcID++ {
			arc := graph.arcs[arcID]
			if uint64(arc.target) >= uint64(len(graph.vertices)) || uint64(arc.relation) >= uint64(len(graph.relations)) || !validStringID(arc.authPath, graph.strings) {
				return fmt.Errorf("vertex %d has invalid navigation arc %d", vertexID, arcID)
			}
			if arcID > start && !graphArcLess(graph.arcs[arcID-1], arc, graph.strings) {
				return fmt.Errorf("vertex %d navigation arcs are not sorted and unique", vertexID)
			}
		}
	}
	return nil
}

func graphArcLess(a, b graphArc, values []string) bool {
	if a.target != b.target {
		return a.target < b.target
	}
	if a.relation != b.relation {
		return a.relation < b.relation
	}
	return values[a.authPath] < values[b.authPath]
}

func validStringID(id uint32, values []string) bool {
	return uint64(id) < uint64(len(values))
}

func checkedMultiply(a, b uint64) (uint64, bool) {
	if a != 0 && b > math.MaxUint64/a {
		return 0, false
	}
	return a * b, true
}

func checkedAdd(a, b uint64) (uint64, bool) {
	if b > math.MaxUint64-a {
		return 0, false
	}
	return a + b, true
}
