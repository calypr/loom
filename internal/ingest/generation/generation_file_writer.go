package generation

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/semanticinventory"
	"github.com/calypr/loom/internal/ingest"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type writerResult struct {
	timings            map[string]float64
	relationshipCounts map[catalog.RelationshipKey]int64
}

type fileWriteFlow struct {
	loader           fileLoader
	verticesInserted *int64
	edgesInserted    *int64
	pipelineError    func(error)
}

func (flow fileWriteFlow) start(ctx, fileCtx context.Context, writeChan <-chan fileWriteTask) (*sync.WaitGroup, <-chan writerResult) {
	opts, client := flow.loader.options, flow.loader.client
	overwrite, insert := flow.loader.overwrite, flow.loader.insert
	setPipelineErr := flow.pipelineError
	writeStats, _ := ctx.Value(generationWriteStatsKey{}).(*generationWriteStats)
	writersWG := &sync.WaitGroup{}
	writerTimingsChan := make(chan writerResult, opts.WriterCount)
	for writer := 0; writer < opts.WriterCount; writer++ {
		writersWG.Add(1)
		go func() {
			defer writersWG.Done()
			localTimings := make(map[string]float64)
			localRelationships := make(map[catalog.RelationshipKey]int64)
			for {
				select {
				case <-fileCtx.Done():
					return
				case task, open := <-writeChan:
					if !open {
						select {
						case writerTimingsChan <- writerResult{timings: localTimings, relationshipCounts: localRelationships}:
						case <-ctx.Done():
						}
						return
					}
					var permitWait time.Duration
					if opts.fileWriterBudget != nil {
						permitStart := time.Now()
						select {
						case opts.fileWriterBudget <- struct{}{}:
							permitWait = time.Since(permitStart)
						case <-fileCtx.Done():
							return
						}
					}
					insertStart := time.Now()
					var insertErr error
					taskOverwrite := overwrite || task.replace
					insertCtx := fileCtx
					if writeStats != nil {
						class := "vertex"
						switch task.collection {
						case ingest.EdgeCollection:
							class = "edge"
						case semanticinventory.Collection, semanticinventory.EntryCollection:
							class = "semantic_inventory"
						}
						insertCtx = arangostore.WithRawInsertObserver(fileCtx, func(bytes int64, duration time.Duration) {
							writeStats.record(class, generationWriteSample{bytes: bytes, permitWait: permitWait, duration: duration})
						})
					}
					insertErr = insert(insertCtx, client, task.collection, task.docs, taskOverwrite, opts.WriteAPI)
					if opts.fileWriterBudget != nil {
						<-opts.fileWriterBudget
					}
					if insertErr != nil {
						setPipelineErr(insertErr)
						return
					}
					elapsed := time.Since(insertStart).Seconds()
					switch task.collection {
					case ingest.EdgeCollection:
						localTimings["edge_insert"] += elapsed
						atomic.AddInt64(flow.edgesInserted, int64(len(task.docs)))
						if opts.fileInsertedCounters != nil {
							atomic.AddInt64(&opts.fileInsertedCounters.edges, int64(len(task.docs)))
						}
						catalog.MergeRelationshipCounts(localRelationships, task.relationshipCounts)
					case semanticinventory.Collection, semanticinventory.EntryCollection:
						localTimings["semantic_inventory_insert"] += elapsed
						localTimings["semantic_inventory_batches"]++
					default:
						localTimings["vertex_insert"] += elapsed
						atomic.AddInt64(flow.verticesInserted, int64(len(task.docs)))
						if opts.fileInsertedCounters != nil {
							atomic.AddInt64(&opts.fileInsertedCounters.vertices, int64(len(task.docs)))
						}
					}
				}
			}
		}()
	}
	go func() {
		writersWG.Wait()
		close(writerTimingsChan)
	}()

	return writersWG, writerTimingsChan
}
