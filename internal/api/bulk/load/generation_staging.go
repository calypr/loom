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
	generationFormMaxMemory = int64(32 << 20)
	formMemoryReserve       = int64(10 << 20)

	formMapEntryOverhead       = 200
	formFileHeaderSize         = 100
	formMIMEHeaderBaseSize     = 400
	formMIMEHeaderMapOverhead  = 200
	formDefaultMaxParts        = 1000
	formDefaultMaxHeaderValues = 10000
	formCopyBufferSize         = 32 * 1024

	generationFileField = "file"
)

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
	if value, err := strconv.Atoi(godebugValue("multipartmaxparts")); err == nil && value >= 0 {
		limits.maxParts = value
	}
	if value, err := strconv.ParseInt(godebugValue("multipartmaxheaders"), 10, 64); err == nil && value >= 0 {
		limits.maxHeaders = value
	}
	return limits
}

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

type multipartParseError struct{ err error }

func (e *multipartParseError) Error() string { return e.err.Error() }
func (e *multipartParseError) Unwrap() error { return e.err }

type generationBundle struct {
	Values    map[string][]string
	StagedDir string

	fileParts  int
	pending    []pendingGenerationFile
	stagingErr error
}

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

func (b *generationBundle) FileCount() int {
	if b == nil {
		return 0
	}
	return b.fileParts
}

func (b *generationBundle) noteFault(err error) {
	if b.stagingErr == nil {
		b.stagingErr = err
	}
}

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

	failParse := func(err error) (*generationBundle, error) {
		bundle.Cleanup()
		return nil, &multipartParseError{err}
	}
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

		bundled := name == generationFileField
		if bundled {
			bundle.fileParts++
		}

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

func (b *generationBundle) create(name string) (*os.File, error) {
	if err := b.ensureDir(); err != nil {
		return nil, err
	}
	return os.Create(filepath.Join(b.StagedDir, name))
}

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

func stagedFileName(filename string) (string, error) {
	name := filepath.Base(filename)
	if name == "." || name == "" || name != filename || filepath.Ext(name) != ".ndjson" {
		return "", os.ErrInvalid
	}
	return name, nil
}

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
