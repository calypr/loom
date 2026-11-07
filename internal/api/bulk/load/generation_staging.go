package load

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// generationFormMaxMemory is the budget the handler historically passed to
	// multipart.Reader.ReadForm. readForm adds a fixed 10 MiB reserve on top of
	// it for non-file parts, and both limits decide which parts stay in memory.
	generationFormMaxMemory = int64(32 << 20)
	formMemoryReserve       = int64(10 << 20)

	// Budget accounting mirrored from mime/multipart's readForm so the
	// streaming stager makes the same in-memory-vs-streaming, message-too-large,
	// part-count and header-count decisions as the ReadForm call it replaces.
	formMapEntryOverhead       = 200
	formFileHeaderSize         = 100
	formMIMEHeaderBaseSize     = 400
	formMIMEHeaderMapOverhead  = 200
	formDefaultMaxParts        = 1000
	formDefaultMaxHeaderValues = 10000
	formCopyBufferSize         = 32 * 1024

	// generationFileField is the multipart field the loader bundle is read
	// from; file parts under any other field name are not part of the bundle.
	generationFileField = "file"
)

// formLimits are the parse limits mime/multipart's readForm applies, including
// the ones an operator can retune through GODEBUG. Reading them here keeps the
// stager and the parser it replaces in lockstep when a deployment sets
// multipartmaxparts or multipartmaxheaders.
type formLimits struct {
	maxFileMemoryBytes int64
	maxMemoryBytes     int64
	maxParts           int
	maxHeaders         int64
}

func generationFormLimits() formLimits {
	limits := formLimits{
		maxFileMemoryBytes: generationFormMaxMemory,
		maxMemoryBytes:     generationFormMaxMemory + formMemoryReserve,
		maxParts:           formDefaultMaxParts,
		maxHeaders:         formDefaultMaxHeaderValues,
	}
	// Same rule as readForm: an override only applies when it parses, so a
	// malformed value keeps the default instead of tightening the limit to zero.
	if value, err := strconv.Atoi(godebugValue("multipartmaxparts")); err == nil && value >= 0 {
		limits.maxParts = value
	}
	if value, err := strconv.ParseInt(godebugValue("multipartmaxheaders"), 10, 64); err == nil && value >= 0 {
		limits.maxHeaders = value
	}
	return limits
}

// godebugValue returns the last value assigned to name in $GODEBUG, mirroring
// how internal/godebug (not importable) resolves a setting: comma-separated
// pairs, later ones winning, with any "#pattern" bisect suffix stripped.
func godebugValue(name string) string {
	value, found := "", false
	for _, setting := range strings.Split(os.Getenv("GODEBUG"), ",") {
		key, arg, ok := strings.Cut(setting, "=")
		if !ok || key != name {
			continue
		}
		value, _, _ = strings.Cut(arg, "#")
		found = true
	}
	if !found {
		return ""
	}
	return value
}

// multipartParseError marks a failure raised while parsing the multipart
// stream. mime/multipart returns unexported errors for these, and the handler
// must classify them as invalid input rather than as a staging fault.
type multipartParseError struct{ err error }

func (e *multipartParseError) Error() string { return e.err.Error() }
func (e *multipartParseError) Unwrap() error { return e.err }

// generationBundle is a parsed generation upload: the scalar form values in
// ReadForm's first-value-wins shape, plus the bundle already staged in the
// directory the loader consumes.
//
// stagingErr carries the first file-part fault — an invalid name, a duplicate
// name, or a failed staged write — and is reported only by ParseStagingError,
// which the handler calls after scalar validation and authorization. That is
// where the replaced path raised it: ReadForm parsed the entire body before
// stageGenerationFiles ever inspected a name, so a denied or otherwise invalid
// request reported its scalar-level or transport-level fault instead of the
// file fault.
type generationBundle struct {
	Values    map[string][]string
	StagedDir string

	fileParts  int // bundle-field file parts, in multipart order, valid or not
	pending    []pendingGenerationFile
	stagingErr error
}

// ParseStagingError returns the deferred file-part fault, or nil.
func (b *generationBundle) ParseStagingError() error {
	if b == nil {
		return nil
	}
	return b.stagingErr
}

type pendingGenerationFile struct {
	name    string
	content []byte
}

// Cleanup removes every staged file. It is safe to call more than once and on
// a nil bundle, and it is the only disposal an upload needs: no intermediate
// multipart temporary copy exists to remove.
func (b *generationBundle) Cleanup() {
	if b == nil {
		return
	}
	b.pending = nil
	if b.StagedDir != "" {
		_ = os.RemoveAll(b.StagedDir)
		b.StagedDir = ""
	}
}

// FileCount is the number of file parts under the bundle field, in multipart
// order. It counts parts the stager later rejects by name, as
// len(form.File["file"]) did.
func (b *generationBundle) FileCount() int {
	if b == nil {
		return 0
	}
	return b.fileParts
}

// noteFault records the first file-part fault. Later faults are already
// shadowed by it, as they were when stageGenerationFiles returned at the first
// offending header.
func (b *generationBundle) noteFault(err error) {
	if b.stagingErr == nil {
		b.stagingErr = err
	}
}

// stageGenerationBundle parses a multipart generation upload in one pass and
// writes every file part straight into the directory the loader consumes, so
// an uploaded byte reaches temporary storage once. It mirrors mime/multipart
// readForm's memory budgeting, part and header limits, value accumulation and
// error values; the deliberate difference is that a part past the budget
// readForm spooled to disk is streamed into the staged file instead of into a
// multipart-* temporary file that a second copy then read back.
//
// A part that stays within the budget is buffered and written when the next
// scalar field arrives or at the end of the stream, which is no later than the
// point where the replaced path handed the same bytes to the stager. File-part
// faults are deferred to ParseStagingError so the handler keeps the shipped
// precedence: parse, scalar validation, authorization, then the file fault.
// Whatever the failure, Cleanup leaves nothing behind. A nil reader yields a
// nil bundle and a nil error, which is how the handler saw an absent body.
func stageGenerationBundle(body *multipart.Reader) (*generationBundle, error) {
	if body == nil {
		return nil, nil
	}
	bundle := &generationBundle{Values: make(map[string][]string)}
	limits := generationFormLimits()
	maxFileMemoryBytes := limits.maxFileMemoryBytes
	maxMemoryBytes := limits.maxMemoryBytes
	maxParts := limits.maxParts
	maxHeaders := limits.maxHeaders
	seen := make(map[string]struct{})
	var copyBuf []byte

	// A parse failure disposes of what has been staged so far. A file-part
	// fault does not: it is reported after authorization, and the handler's
	// cleanup covers the bundle either way.
	failParse := func(err error) (*generationBundle, error) {
		bundle.Cleanup()
		return nil, &multipartParseError{err}
	}
	// discard drains a part that will not be staged, keeping readForm's budget
	// accounting for it intact. readForm returned a failed drain of an
	// over-budget part as a parse error, so this does too.
	discard := func(part *multipart.Part) error {
		_, err := io.Copy(io.Discard, part)
		part.Close()
		return err
	}

	for {
		part, err := body.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return failParse(err)
		}
		if maxParts <= 0 {
			part.Close()
			return failParse(errors.New("multipart: message too large"))
		}
		maxParts--

		name := part.FormName()
		if name == "" {
			part.Close()
			continue
		}
		// Several values for one key share a map entry, but a uniform
		// per-value overhead keeps the accounting identical to readForm.
		maxMemoryBytes -= int64(len(name)) + formMapEntryOverhead
		if maxMemoryBytes < 0 {
			part.Close()
			return failParse(errors.New("multipart: message too large"))
		}

		filename := part.FileName()
		if filename == "" {
			var value bytes.Buffer
			n, err := io.CopyN(&value, part, maxMemoryBytes+1)
			part.Close()
			if err != nil && err != io.EOF {
				return failParse(err)
			}
			maxMemoryBytes -= n
			if maxMemoryBytes < 0 {
				return failParse(errors.New("multipart: message too large"))
			}
			bundle.Values[name] = append(bundle.Values[name], value.String())
			// A scalar field means every earlier file is fully visible to the
			// handler, so writing them now is no later than the replaced path.
			if err := bundle.flush(); err != nil {
				bundle.noteFault(err)
			}
			continue
		}

		maxMemoryBytes -= mimeHeaderSize(part.Header) + formMapEntryOverhead + formFileHeaderSize
		if maxMemoryBytes < 0 {
			part.Close()
			return failParse(errors.New("multipart: message too large"))
		}
		for _, values := range part.Header {
			maxHeaders -= int64(len(values))
		}
		if maxHeaders < 0 {
			part.Close()
			return failParse(errors.New("multipart: message too large"))
		}

		// Only the "file" field is the bundle; file parts under any other name
		// are charged against the budget, as ReadForm charged them, then
		// discarded.
		bundled := name == generationFileField
		if bundled {
			bundle.fileParts++
		}

		// readForm read and budgeted every part's content before any file name
		// was inspected, so the charge applies to parts the bundle rejects:
		// their size still decides whether a later part trips the limit.
		var content bytes.Buffer
		n, err := io.CopyN(&content, part, maxFileMemoryBytes+1)
		if err != nil && err != io.EOF {
			part.Close()
			return failParse(err)
		}
		oversized := n > maxFileMemoryBytes
		if !oversized {
			maxFileMemoryBytes -= n
			maxMemoryBytes -= n
		}

		stagedName := ""
		stage := bundled && bundle.stagingErr == nil
		if bundled {
			var err error
			stagedName, err = stagedFileName(filename)
			if err != nil {
				bundle.noteFault(err)
				stage = false
			} else if _, ok := seen[stagedName]; ok {
				bundle.noteFault(os.ErrExist)
				stage = false
			} else {
				seen[stagedName] = struct{}{}
			}
		}
		if !stage {
			// readForm still read this part's content, so a failing read is a
			// parse failure here too rather than something to swallow.
			if err := discard(part); err != nil {
				return failParse(err)
			}
			continue
		}
		if !oversized {
			part.Close()
			bundle.pending = append(bundle.pending, pendingGenerationFile{name: stagedName, content: content.Bytes()})
			continue
		}
		// Past the budget ReadForm kept on disk: stream the rest into the
		// staged file, the only copy the loader will read. The part stays
		// open until the remainder has been copied.
		dst, err := bundle.create(stagedName)
		if err != nil {
			bundle.noteFault(err)
			part.Close()
			continue
		}
		if _, err := dst.Write(content.Bytes()); err != nil {
			_ = dst.Close()
			part.Close()
			bundle.noteFault(err)
			continue
		}
		if copyBuf == nil {
			copyBuf = make([]byte, formCopyBufferSize)
		}
		// os.File.ReadFrom would allocate its own buffer; writerOnly keeps
		// io.Copy on ours, as readForm does.
		type writerOnly struct{ io.Writer }
		_, copyErr := io.CopyBuffer(writerOnly{dst}, part, copyBuf)
		part.Close()
		closeErr := dst.Close()
		if copyErr != nil {
			bundle.noteFault(copyErr)
			continue
		}
		if closeErr != nil {
			bundle.noteFault(closeErr)
			continue
		}
	}
	if err := bundle.flush(); err != nil {
		bundle.noteFault(err)
	}
	return bundle, nil
}

// create opens a staged file for a part too large to buffer.
func (b *generationBundle) create(name string) (*os.File, error) {
	if err := b.ensureDir(); err != nil {
		return nil, err
	}
	return os.Create(filepath.Join(b.StagedDir, name))
}

// flush writes the buffered in-memory parts to the staged directory. Once a
// file-part fault is recorded the bundle cannot load, so nothing more is
// written; Cleanup disposes of whatever reached disk beforehand.
func (b *generationBundle) flush() error {
	if len(b.pending) == 0 || b.stagingErr != nil {
		return nil
	}
	if err := b.ensureDir(); err != nil {
		return err
	}
	for _, file := range b.pending {
		if err := writeStagedFile(filepath.Join(b.StagedDir, file.name), file.content); err != nil {
			return err
		}
	}
	b.pending = nil
	return nil
}

func (b *generationBundle) ensureDir() error {
	if b.StagedDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "loom-generation-")
	b.StagedDir = dir
	return err
}

func writeStagedFile(path string, content []byte) error {
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := dst.Write(content); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// stagedFileName applies the shipped bundle contract: a bare .ndjson name with
// no path components, unique within one request.
func stagedFileName(filename string) (string, error) {
	name := filepath.Base(filename)
	if name == "." || name == "" || name != filename || filepath.Ext(name) != ".ndjson" {
		return "", os.ErrInvalid
	}
	return name, nil
}

// mimeHeaderSize mirrors the per-part metadata charge mime/multipart's
// readForm applies to its memory budget.
func mimeHeaderSize(header textproto.MIMEHeader) int64 {
	size := int64(formMIMEHeaderBaseSize)
	for key, values := range header {
		size += int64(len(key)) + formMIMEHeaderMapOverhead
		for _, value := range values {
			size += int64(len(value))
		}
	}
	return size
}
