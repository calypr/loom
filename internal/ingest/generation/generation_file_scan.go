package generation

import (
	"bufio"
	"context"
	"strings"
)

func scanFileLines(fileCtx context.Context, scanner *bufio.Scanner, linesChan chan<- []fileLine, batchSize int, setPipelineErr func(error)) {
	defer close(linesChan)
	lineNumber := 0
	lineBatch := make([]fileLine, 0, batchSize)
	lineBatchBytes := 0
	flushLineBatch := func() bool {
		if len(lineBatch) == 0 {
			return true
		}
		select {
		case linesChan <- lineBatch:
			lineBatch = make([]fileLine, 0, batchSize)
			lineBatchBytes = 0
			return true
		case <-fileCtx.Done():
			return false
		}
	}
	for scanner.Scan() {
		lineNumber++
		select {
		case <-fileCtx.Done():
			return
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lineBatch = append(lineBatch, fileLine{number: lineNumber, text: line})
		lineBatchBytes += len(line)
		if (len(lineBatch) == batchSize || lineBatchBytes >= lineBatchByteLimit) && !flushLineBatch() {
			return
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		setPipelineErr(scanErr)
		return
	}
	flushLineBatch()
}
