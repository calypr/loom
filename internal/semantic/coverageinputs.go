package semantic

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const CoverageMaximumPartitions = 10000
const maxInt64 = int64(^uint64(0) >> 1)

var ErrCoverageResourceExpectation = errors.New("semantic coverage resource source-set expectation is invalid or incomplete")

type ResourceExpectation struct {
	ResourceType    string
	SourceExists    bool
	Terminal        bool
	SourceCount     int64
	SourceSetDigest string
	Partitions      []SourcePartitionExpectation
}

type SourcePartitionExpectation struct {
	AuthResourcePath string
	SourceCount      int64
	SourceSetDigest  string
}

type SourceSetAccumulator struct {
	mu           sync.Mutex
	resourceType string
	sourceExists bool
	sourceCount  int64
	partitions   map[string]*SourceSetDigest
	failed       error
}

type SourceSetDigest struct {
	count int64
	sum   [sha256.Size]byte
}

func NewSourceSetAccumulator(resourceType string, sourceExists bool) *SourceSetAccumulator {
	return &SourceSetAccumulator{
		resourceType: resourceType,
		sourceExists: sourceExists,
		partitions:   make(map[string]*SourceSetDigest),
	}
}

func (a *SourceSetAccumulator) AddSource(sourceID, authResourcePath string) error {
	if a == nil {
		return ErrCoverageResourceExpectation
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed != nil {
		return a.failed
	}
	if strings.TrimSpace(a.resourceType) == "" || strings.TrimSpace(sourceID) == "" || !a.sourceExists {
		a.failed = ErrCoverageResourceExpectation
		return a.failed
	}
	partition := a.partitions[authResourcePath]
	if partition == nil {
		if len(a.partitions) >= CoverageMaximumPartitions {
			a.failed = fmt.Errorf("%w: authorization partition limit exceeded", ErrCoverageResourceExpectation)
			return a.failed
		}
		partition = &SourceSetDigest{}
		a.partitions[authResourcePath] = partition
	}
	if a.sourceCount == maxInt64 {
		a.failed = fmt.Errorf("%w: source count overflow", ErrCoverageResourceExpectation)
		return a.failed
	}
	if err := partition.Add(a.resourceType, sourceID, authResourcePath); err != nil {
		a.failed = err
		return err
	}
	a.sourceCount++
	return nil
}

func (a *SourceSetAccumulator) Snapshot(terminal bool) (ResourceExpectation, error) {
	if a == nil {
		return ResourceExpectation{}, ErrCoverageResourceExpectation
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed != nil {
		return ResourceExpectation{}, a.failed
	}
	if strings.TrimSpace(a.resourceType) == "" {
		return ResourceExpectation{}, ErrCoverageResourceExpectation
	}
	result := ResourceExpectation{
		ResourceType: a.resourceType, SourceExists: a.sourceExists, Terminal: terminal,
		SourceCount: a.sourceCount, Partitions: make([]SourcePartitionExpectation, 0, len(a.partitions)),
	}
	if !a.sourceExists {
		if a.sourceCount != 0 || len(a.partitions) != 0 {
			return ResourceExpectation{}, ErrCoverageResourceExpectation
		}
		return result, nil
	}
	for authPath, digest := range a.partitions {
		result.Partitions = append(result.Partitions, SourcePartitionExpectation{
			AuthResourcePath: authPath, SourceCount: digest.Count(),
			SourceSetDigest: digest.PartitionDigest(a.resourceType, authPath),
		})
	}
	sort.Slice(result.Partitions, func(i, j int) bool {
		return result.Partitions[i].AuthResourcePath < result.Partitions[j].AuthResourcePath
	})
	result.SourceSetDigest = ResourceSetDigest(result.ResourceType, result.Partitions)
	return result, nil
}

func (d *SourceSetDigest) Add(resourceType, sourceID, authResourcePath string) error {
	if d == nil || d.count == maxInt64 {
		return fmt.Errorf("%w: source count overflow", ErrCoverageResourceExpectation)
	}
	identityDigest := sha256.Sum256([]byte(CoverageDocumentKey("resource-source-identity/v1", resourceType, sourceID, authResourcePath)))
	addDigest(&d.sum, identityDigest)
	d.count++
	return nil
}

func (d *SourceSetDigest) Count() int64 {
	if d == nil {
		return 0
	}
	return d.count
}

func (d *SourceSetDigest) PartitionDigest(resourceType, authResourcePath string) string {
	if d == nil {
		return ""
	}
	return CoverageDocumentKey(
		"resource-source-partition/v1", resourceType, authResourcePath,
		fmt.Sprint(d.count), hex.EncodeToString(d.sum[:]),
	)
}

func ValidateResourceExpectation(expectation ResourceExpectation) error {
	if strings.TrimSpace(expectation.ResourceType) == "" || !expectation.SourceExists || !expectation.Terminal || expectation.SourceCount < 0 || len(expectation.Partitions) > CoverageMaximumPartitions {
		return ErrCoverageResourceExpectation
	}
	var sourceCount int64
	seen := make(map[string]struct{}, len(expectation.Partitions))
	for _, partition := range expectation.Partitions {
		if partition.SourceCount <= 0 || strings.TrimSpace(partition.SourceSetDigest) == "" {
			return ErrCoverageResourceExpectation
		}
		if _, exists := seen[partition.AuthResourcePath]; exists {
			return ErrCoverageResourceExpectation
		}
		seen[partition.AuthResourcePath] = struct{}{}
		sourceCount += partition.SourceCount
	}
	if sourceCount != expectation.SourceCount || expectation.SourceSetDigest != ResourceSetDigest(expectation.ResourceType, expectation.Partitions) {
		return ErrCoverageResourceExpectation
	}
	return nil
}

func SameResourceSourceSet(expected, actual ResourceExpectation) bool {
	if !expected.SourceExists || !expected.Terminal || !actual.SourceExists || !actual.Terminal ||
		expected.ResourceType != actual.ResourceType || expected.SourceCount != actual.SourceCount ||
		expected.SourceSetDigest != actual.SourceSetDigest || len(expected.Partitions) != len(actual.Partitions) {
		return false
	}
	for index := range expected.Partitions {
		if expected.Partitions[index] != actual.Partitions[index] {
			return false
		}
	}
	return true
}

func ResourceSetDigest(resourceType string, partitions []SourcePartitionExpectation) string {
	parts := []string{"resource-source-set/v1", resourceType}
	for _, partition := range partitions {
		parts = append(parts, partition.AuthResourcePath, fmt.Sprint(partition.SourceCount), partition.SourceSetDigest)
	}
	return CoverageDocumentKey(parts...)
}

func CoverageDocumentKey(parts ...string) string {
	hash := sha256.New()
	var length [4]byte
	for _, part := range parts {
		binary.BigEndian.PutUint32(length[:], uint32(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return "scv1_" + hex.EncodeToString(hash.Sum(nil))
}

func addDigest(target *[sha256.Size]byte, source [sha256.Size]byte) {
	carry := uint16(0)
	for index := len(target) - 1; index >= 0; index-- {
		total := uint16(target[index]) + uint16(source[index]) + carry
		target[index] = byte(total)
		carry = total >> 8
	}
}
