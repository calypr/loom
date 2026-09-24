package arango

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/calypr/loom/internal/catalog"
)

const availabilityGraphMaxBytes = 1536 << 20

// ConfigureAvailabilityCache is called before serving requests. The database
// identity prevents reuse across servers with matching collection revisions.
func (s *Store) ConfigureAvailabilityCache(directory, databaseIdentity string) error {
	if directory == "" || databaseIdentity == "" {
		return fmt.Errorf("column availability cache requires a directory and database identity")
	}
	identity := sha256.Sum256([]byte(databaseIdentity))
	directory = filepath.Join(directory, hex.EncodeToString(identity[:]))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create column availability cache: %w", err)
	}
	s.availabilityCachePath = filepath.Join(directory, "availability.graph")
	return nil
}

func (s *Store) readAvailabilityCache(key string) *catalog.AvailabilityGraph {
	if s.availabilityCachePath == "" {
		return nil
	}
	file, err := os.Open(s.availabilityCachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("column availability cache is unreadable", "error", err)
		}
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > availabilityGraphMaxBytes+sha256.Size || !info.Mode().IsRegular() {
		return nil
	}
	expected, err := hex.DecodeString(key)
	if err != nil || len(expected) != sha256.Size {
		return nil
	}
	var stored [sha256.Size]byte
	if _, err := io.ReadFull(file, stored[:]); err != nil || !bytes.Equal(stored[:], expected) {
		return nil
	}
	graph, err := catalog.ReadAvailabilityGraph(file, availabilityGraphMaxBytes)
	if err != nil {
		slog.Warn("column availability cache is invalid; rebuilding", "error", err)
		return nil
	}
	return graph
}

func (s *Store) writeAvailabilityCache(key string, graph *catalog.AvailabilityGraph) error {
	if s.availabilityCachePath == "" {
		return nil
	}
	identity, err := hex.DecodeString(key)
	if err != nil || len(identity) != sha256.Size {
		return fmt.Errorf("invalid column availability cache identity")
	}
	file, err := os.CreateTemp(filepath.Dir(s.availabilityCachePath), ".availability-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	if _, err := file.Write(identity); err != nil {
		return err
	}
	if _, err := graph.WriteTo(file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, s.availabilityCachePath)
}
