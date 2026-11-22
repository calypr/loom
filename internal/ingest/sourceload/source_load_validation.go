package sourceload

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/ingest"
	"io"
	"os"
	"path/filepath"
)

const sourceFileIssueLimit = 100

func validateSourceLoadFile(ctx context.Context, localPath, filePath, project, authResourcePath, generation string, schema *ingest.RuntimeFHIRSchema) (digest string, rows int64, validation *SourceFileValidationError, err error) {
	resourceType, err := canonicalSourceResourceType(filePath)
	if err != nil {
		return "", 0, nil, err
	}
	if !schema.HasClass(resourceType) {
		return "", 0, nil, fmt.Errorf("resource type %s is not in the generated FHIR resource registry", resourceType)
	}
	if !ingest.SupportsGeneratedLoad(resourceType) {
		return "", 0, nil, fmt.Errorf("resource type %q has no generated loader", resourceType)
	}
	var builder ingest.RowBuilder = ingest.NewGeneratedRowBuilder(project, authResourcePath)
	builder, err = ingest.NewGenerationRowBuilder(builder, project, generation)
	if err != nil {
		return "", 0, nil, err
	}
	file, err := os.Open(localPath)
	if err != nil {
		return "", 0, nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(file, hash))
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	failures := &SourceFileValidationError{}
	timings := make(map[string]float64)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if err := ctx.Err(); err != nil {
			return "", rows, nil, err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		rows++
		var envelope struct {
			ResourceType string `json:"resourceType"`
		}
		category := ingest.RowErrorValidation
		var rowErr error
		if decodeErr := json.Unmarshal(line, &envelope); decodeErr != nil {
			rowErr = decodeErr
		} else if envelope.ResourceType != resourceType {
			rowErr = fmt.Errorf("resourceType must be %s", resourceType)
		} else {
			_, category, rowErr = builder.Build(resourceType, line, timings)
		}
		if rowErr == nil {
			continue
		}
		located := &ingest.RowError{File: filepath.Base(filePath), Row: lineNumber, ResourceType: resourceType, Category: string(category), Cause: rowErr}
		if category != ingest.RowErrorValidation {
			return "", rows, nil, located
		}
		failures.TotalErrors++
		if len(failures.Issues) < sourceFileIssueLimit {
			failures.Issues = append(failures.Issues, ingest.RowErrorSample{File: located.File, Line: lineNumber, ResourceType: resourceType, Category: string(category), Message: located.PublicMessage()})
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		if errors.Is(scanErr, bufio.ErrTooLong) {
			return "", rows, nil, &SourcePublishLimitError{Limit: "NDJSON row bytes", Maximum: 64 << 20}
		}
		return "", rows, nil, fmt.Errorf("scan source file %s: %w", filepath.Base(filePath), scanErr)
	}
	if err := ctx.Err(); err != nil {
		return "", rows, nil, err
	}
	if failures.TotalErrors > 0 {
		failures.Truncated = failures.TotalErrors > int64(len(failures.Issues))
		return "", rows, failures, nil
	}
	return hex.EncodeToString(hash.Sum(nil)), rows, nil, nil
}
