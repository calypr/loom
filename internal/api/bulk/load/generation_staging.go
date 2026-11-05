package load

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
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

// multipartParseError marks a failure raised while parsing the multipart
// stream. mime/multipart returns unexported errors for these, and the handler
// must classify them as invalid input rather than as a staging fault.
type multipartParseError struct{ err error }

func (e *multipartParseError) Error() string { return e.err.Error() }
func (e *multipartParseError) Unwrap() error { return e.err }

// generationBundle is a parsed generation upload: the scalar form values in
// ReadForm's first-value-wins shape, plus the bundle already staged in the
// directory the loader consumes.
type generationBundle struct {
	Values    map[string][]string
	StagedDir string

	files   []string // staged names in arrival order
	pending []pendingGenerationFile
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
	b.files = nil
	if b.StagedDir != "" {
		_ = os.RemoveAll(b.StagedDir)
		b.StagedDir = ""
	}
}

// FileCount is the number of file parts seen, in multipart order.
func (b *generationBundle) FileCount() int {
	if b == nil {
		return 0
	}
	return len(b.files)
}

// stageGenerationBundle parses a multipart generation upload in one pass and
// writes every file part straight into the directory the loader consumes, so
// an uploaded byte reaches temporary storage once. It mirrors mime/multipart
// readForm's memory budgeting, part and header limits, value accumulation and
// error values; the deliberate difference is that a part past the budget
// readForm spooled to disk is streamed into the staged file instead of into a
// multipart-* temporary file that a second copy then read back.
//
// Parts that stay in memory are written when the next scalar field arrives or
// at the end of the stream, so a request rejected by a later part leaves
// nothing on disk. A nil reader yields a nil bundle and a nil error, which is
// how the handler saw an absent body.
func stageGenerationBundle(body *multipart.Reader) (*generationBundle, error) {
	if body == nil {
		return nil, nil
	}
	bundle := &generationBundle{Values: make(map[string][]string)}
	maxFileMemoryBytes := generationFormMaxMemory
	maxMemoryBytes := generationFormMaxMemory + formMemoryReserve
	maxParts := formDefaultMaxParts
	maxHeaders := int64(formDefaultMaxHeaderValues)
	seen := make(map[string]struct{})
	var copyBuf []byte

	// Every failure disposes what has been staged so far, so a request
	// rejected by any part leaves no temporary files behind.
	fail := func(err error) (*generationBundle, error) {
		bundle.Cleanup()
		return nil, err
	}
	failParse := func(err error) (*generationBundle, error) {
		return fail(&multipartParseError{err})
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
			// handler, so writing them now reproduces what ReadForm staged.
			if err := bundle.flush(); err != nil {
				return fail(err)
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
		stagedName := ""
		if bundled {
			var err error
			stagedName, err = stagedFileName(filename)
			if err != nil {
				part.Close()
				return fail(err)
			}
			if _, ok := seen[stagedName]; ok {
				part.Close()
				return fail(os.ErrExist)
			}
			seen[stagedName] = struct{}{}
		}

		var content bytes.Buffer
		n, err := io.CopyN(&content, part, maxFileMemoryBytes+1)
		if err != nil && err != io.EOF {
			part.Close()
			return failParse(err)
		}
		if n > maxFileMemoryBytes {
			if !bundled {
				_, _ = io.Copy(io.Discard, part)
				part.Close()
				continue
			}
			// Past the budget ReadForm kept on disk: stream the rest into the
			// staged file, the only copy the loader will read. The part stays
			// open until the remainder has been copied.
			dst, err := bundle.create(stagedName)
			if err != nil {
				part.Close()
				return fail(err)
			}
			if _, err := dst.Write(content.Bytes()); err != nil {
				_ = dst.Close()
				part.Close()
				return fail(err)
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
				return fail(copyErr)
			}
			if closeErr != nil {
				return fail(closeErr)
			}
			continue
		}
		part.Close()
		maxFileMemoryBytes -= n
		maxMemoryBytes -= n
		if !bundled {
			continue
		}
		bundle.files = append(bundle.files, stagedName)
		bundle.pending = append(bundle.pending, pendingGenerationFile{name: stagedName, content: content.Bytes()})
	}
	if err := bundle.flush(); err != nil {
		return fail(err)
	}
	return bundle, nil
}

func (b *generationBundle) create(name string) (*os.File, error) {
	if err := b.ensureDir(); err != nil {
		return nil, err
	}
	b.files = append(b.files, name)
	return os.Create(filepath.Join(b.StagedDir, name))
}

// flush writes the buffered in-memory parts to the staged directory.
func (b *generationBundle) flush() error {
	if len(b.pending) == 0 {
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
