package api

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/storage"
)

const (
	// maxDebugFilesPerAssemble bounds how many files one assemble request may
	// name; sentry-cli sends one per mapping on its command line.
	maxDebugFilesPerAssemble = 8

	// legacyUploadLimit bounds the compressed archive of a legacy upload. The
	// mappings inside are bounded again, uncompressed, by proguard.MaxDecodedBytes.
	legacyUploadLimit = 64 << 20

	legacyFilePart = "file"
)

type debugFileAssembleItem struct {
	Name   string   `json:"name"`
	Chunks []string `json:"chunks"`
}

type debugFileAssembleResult struct {
	State         string   `json:"state"`
	MissingChunks []string `json:"missingChunks"`
	Detail        *string  `json:"detail"`
}

// AssembleDebugFiles serves sentry-cli 3.x's `upload-proguard`: the request maps
// each file's SHA-1 to the name it should be stored under and the chunks it is
// made of, and the reply maps the same SHA-1 to a terminal state. Like
// AssembleBundle it answers synchronously. A per-file problem — an unusable
// mapping, a checksum mismatch, a UUID conflict — is a terminal "error" state
// in that file's own result entry, not an HTTP failure, so the CLI reports the
// reason instead of retrying; only a request-level problem (a bad checksum
// format, too many files, an oversized assembled file) fails the whole
// request with an HTTP error status. The recorded request is
// internal/sentrycli/testdata/proguard.
func (h *SentryCLIHandler) AssembleDebugFiles(w http.ResponseWriter, r *http.Request) {
	token, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !h.ownsEveryProject(w, r, token, []string{r.PathValue("project")}) {
		return
	}
	var request map[string]debugFileAssembleItem
	if err := decodeSingleJSONIgnoringUnknownFields(w, r, assembleBodyLimit, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid assemble request")
		return
	}
	if len(request) == 0 || len(request) > maxDebugFilesPerAssemble {
		writeError(w, http.StatusBadRequest, "invalid number of files in assemble request")
		return
	}

	options := sentrycli.DefaultChunkOptions(r.URL.Path)
	results := make(map[string]debugFileAssembleResult, len(request))
	for key, item := range request {
		result, ok := h.assembleDebugFile(w, r, token, key, item, options)
		if !ok {
			return
		}
		results[key] = result
	}
	writeJSON(w, http.StatusOK, results)
}

// assembleDebugFile reassembles and stores one file. It returns false only when
// it has already written an HTTP error; a file that cannot be used is a result
// with state "error", not a false return.
func (h *SentryCLIHandler) assembleDebugFile(w http.ResponseWriter, r *http.Request, token artifactauth.Token, key string, item debugFileAssembleItem, options sentrycli.ChunkOptions) (debugFileAssembleResult, bool) {
	total, err := sentrycli.ParseChecksum(key)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file checksum")
		return debugFileAssembleResult{}, false
	}
	wanted, ok := h.parseAssembleChunks(w, item.Chunks, options)
	if !ok {
		return debugFileAssembleResult{}, false
	}
	staged, err := h.chunks.Load(r.Context(), token.ProjectID, wanted)
	if err != nil {
		h.reportError(fmt.Errorf("load upload chunks: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return debugFileAssembleResult{}, false
	}
	if missing := missingChunks(wanted, staged); len(missing) > 0 {
		return debugFileAssembleResult{State: "not_found", MissingChunks: missing}, true
	}
	if sumStagedBytes(staged) > options.MaxFileSize {
		writeError(w, http.StatusRequestEntityTooLarge, "assembled file exceeds the maximum file size")
		return debugFileAssembleResult{}, false
	}
	assembled, err := sentrycli.Concat(wanted, staged, total)
	if err != nil {
		return failedDebugFile("assembled file does not match its checksum"), true
	}

	mappingUUID, ok := proguard.MappingUUIDFromName(item.Name)
	if !ok {
		return failedDebugFile("only proguard mappings named /proguard/<uuid>.txt are supported"), true
	}
	upload, err := proguard.DecodeUpload(bytes.NewReader(assembled))
	switch {
	case errors.Is(err, proguard.ErrTooLarge):
		return failedDebugFile("proguard mapping is too large"), true
	case errors.Is(err, proguard.ErrInvalidMapping):
		return failedDebugFile("not a valid proguard mapping"), true
	case err != nil:
		h.reportError(fmt.Errorf("decode proguard mapping: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return debugFileAssembleResult{}, false
	}
	if _, err := h.mappings.Put(r.Context(), token.ProjectID, mappingUUID, upload); err != nil {
		if errors.Is(err, proguard.ErrConflict) {
			return failedDebugFile("a different mapping is already stored under this uuid"), true
		}
		if errors.Is(err, storage.ErrArtifactQuotaExceeded) {
			return failedDebugFile(artifactQuotaMessage), true
		}
		h.reportError(fmt.Errorf("store proguard mapping: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return debugFileAssembleResult{}, false
	}
	if err := h.chunks.Delete(r.Context(), token.ProjectID, wanted); err != nil {
		h.reportError(fmt.Errorf("delete consumed upload chunks: %w", err))
	}
	return debugFileAssembleResult{State: "ok", MissingChunks: []string{}}, true
}

func failedDebugFile(detail string) debugFileAssembleResult {
	return debugFileAssembleResult{State: "error", MissingChunks: []string{}, Detail: &detail}
}

// debugFileResponse is one element of the legacy upload reply. sentry-cli
// 2.58.6 parses the reply as an array of these and fails on a missing
// objectName, so every field it reads is present (see the fixtures' README).
type debugFileResponse struct {
	ID          string            `json:"id"`
	DebugID     string            `json:"debugId"`
	ObjectName  string            `json:"objectName"`
	CPUName     string            `json:"cpuName"`
	SHA1        string            `json:"sha1"`
	Size        int64             `json:"size"`
	DateCreated time.Time         `json:"dateCreated"`
	Headers     map[string]string `json:"headers"`
	SymbolType  string            `json:"symbolType"`
}

// UploadLegacyDebugFile serves sentry-cli 2.x's `upload-proguard`, which sends
// one multipart request whose "file" part is a zip holding proguard/<uuid>.txt
// entries and never probes chunk upload. The recorded request is
// internal/sentrycli/testdata/proguard.
func (h *SentryCLIHandler) UploadLegacyDebugFile(w http.ResponseWriter, r *http.Request) {
	token, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !h.ownsEveryProject(w, r, token, []string{r.PathValue("project")}) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, legacyUploadLimit)
	archive, ok := readLegacyArchive(w, r)
	if !ok {
		return
	}
	mappings, err := proguard.MappingsFromZip(archive)
	switch {
	case errors.Is(err, proguard.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "proguard mapping too large")
		return
	case errors.Is(err, proguard.ErrInvalidMapping):
		writeError(w, http.StatusBadRequest, "invalid proguard upload")
		return
	case err != nil:
		h.reportError(fmt.Errorf("read legacy proguard upload: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	files := make([]debugFileResponse, 0, len(mappings))
	now := time.Now().UTC()
	for _, mapping := range mappings {
		if _, err := h.mappings.Put(r.Context(), token.ProjectID, mapping.UUID, mapping.Upload); err != nil {
			if errors.Is(err, proguard.ErrConflict) {
				writeError(w, http.StatusConflict, "a different mapping is already stored under this uuid")
				return
			}
			if errors.Is(err, storage.ErrArtifactQuotaExceeded) {
				writeError(w, http.StatusRequestEntityTooLarge, artifactQuotaMessage)
				return
			}
			h.reportError(fmt.Errorf("store proguard mapping: %w", err))
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		files = append(files, debugFileResponse{
			ID:          mapping.UUID,
			DebugID:     mapping.UUID,
			ObjectName:  "proguard-mapping",
			CPUName:     "any",
			SHA1:        hex.EncodeToString(mapping.Upload.SHA1[:]),
			Size:        mapping.Upload.UncompressedBytes,
			DateCreated: now,
			Headers:     map[string]string{"Content-Type": "text/x-proguard+plain"},
			SymbolType:  "proguard",
		})
	}
	writeJSON(w, http.StatusCreated, files)
}

// readLegacyArchive returns the bytes of the request's "file" part, or false
// after writing the HTTP error.
func readLegacyArchive(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected a multipart upload")
		return nil, false
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "upload has no file part")
			return nil, false
		}
		if err != nil {
			writeError(w, statusForBodyError(err), "invalid multipart upload")
			return nil, false
		}
		if part.FormName() != legacyFilePart {
			continue
		}
		archive, err := io.ReadAll(part)
		if err != nil {
			writeError(w, statusForBodyError(err), "could not read the uploaded file")
			return nil, false
		}
		return archive, true
	}
}

func statusForBodyError(err error) int {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
