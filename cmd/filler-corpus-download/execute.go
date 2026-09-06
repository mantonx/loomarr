package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

const downloadRepresentationAttempts = 3

type fileHashes struct{ sha256, sha1, md5 string }

type verifiedRepresentation struct {
	mediaType     string
	width, height int
}

type representationIdentityMismatchError struct{ reason string }

func (e *representationIdentityMismatchError) Error() string { return e.reason }

func executeDownloads(ctx context.Context, client *http.Client, plan []plannedDownload, opts options) (downloadLedger, error) {
	if err := ensurePrivateDirectory(opts.outputDir); err != nil {
		return downloadLedger{}, err
	}
	ledger := downloadLedger{
		SchemaVersion: fillercorpus.MaterializationLedgerSchemaVersion,
		Profile:       opts.profile, ProcessorID: opts.processorID, ProcessorTermsSHA256: opts.processorTermsSHA256,
		InventorySHA256: opts.inventorySHA256, GeneratedAt: opts.generatedAt.UTC(),
		MaxRequests: opts.maxRequests, MaxItems: opts.maxItems, MaxBytes: opts.maxBytes, MaxImagePixels: opts.maxImagePixels,
	}
	lastRequestAt := time.Time{}
	seenContent := map[string]string{}
	for _, item := range plan {
		verifiedAt := opts.generatedAt.UTC()
		hashes, size, err := hashPrivateFile(item.path)
		var representation verifiedRepresentation
		if errors.Is(err, os.ErrNotExist) {
			for attempt := 0; attempt < downloadRepresentationAttempts; attempt++ {
				if ledger.RequestsUsed >= opts.maxRequests {
					return downloadLedger{}, fmt.Errorf("request ceiling exhausted before %s", item.candidate.CaseID)
				}
				if err := waitForDownload(ctx, lastRequestAt, opts.delay); err != nil {
					return downloadLedger{}, err
				}
				hashes, size, representation, err = download(ctx, client, item, opts.userAgent, opts.maxImagePixels, downloadFetchIdentity(opts, item, attempt))
				ledger.RequestsUsed++
				lastRequestAt = time.Now()
				verifiedAt = lastRequestAt.UTC()
				var mismatch *representationIdentityMismatchError
				if err == nil || !errors.As(err, &mismatch) {
					break
				}
			}
		} else if err == nil {
			representation, err = verifyDownloadedRepresentation(item.path, item.candidate.Representation, opts.maxImagePixels)
		}
		if err != nil {
			return downloadLedger{}, fmt.Errorf("%s: %w", item.candidate.CaseID, err)
		}
		if size != item.candidate.Representation.Bytes || !matchesOptional(hashes.sha256, item.candidate.Representation.SHA256) || !matchesOptional(hashes.sha1, item.candidate.Representation.SHA1) || !matchesOptional(hashes.md5, item.candidate.Representation.MD5) {
			return downloadLedger{}, fmt.Errorf("%s: downloaded bytes or source checksums do not match inventory", item.candidate.CaseID)
		}
		if previous, duplicate := seenContent[hashes.sha256]; duplicate {
			return downloadLedger{}, fmt.Errorf("%s duplicates exact media bytes from %s", item.candidate.CaseID, previous)
		}
		seenContent[hashes.sha256] = item.candidate.CaseID
		ledger.Bytes += size
		ledger.Cases = append(ledger.Cases, downloadedCase{
			CaseID: item.candidate.CaseID, CaptureIDs: slices.Clone(item.candidate.CaptureIDs),
			Authority: item.candidate.Authority, ItemID: item.candidate.ItemID,
			RoleHints: slices.Clone(item.candidate.RoleHints), Creator: slices.Clone(item.candidate.Creator),
			SubjectTerms: slices.Clone(item.candidate.SubjectTerms), Campaign: item.candidate.Campaign,
			SourceFamily: item.candidate.SourceFamily,
			LicenseURL:   item.candidate.LicenseURL, ItemURL: item.candidate.ItemURL, MetadataURL: item.candidate.MetadataURL,
			MetadataRetrievedAt: item.candidate.MetadataRetrievedAt, MetadataSHA256: item.candidate.MetadataSHA256,
			Representation: item.candidate.Representation, LocalFile: filepath.Base(item.path), ContentSHA256: hashes.sha256,
			VerifiedMediaType: representation.mediaType, Width: representation.width, Height: representation.height,
			Approval: item.approval, VerifiedAt: verifiedAt,
		})
	}
	return ledger, nil
}

func waitForDownload(ctx context.Context, previous time.Time, delay time.Duration) error {
	if previous.IsZero() || time.Since(previous) >= delay {
		return nil
	}
	timer := time.NewTimer(delay - time.Since(previous))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func downloadFetchIdentity(opts options, item plannedDownload, attempt int) string {
	value := fmt.Sprintf("%s\n%s\n%s\n%d", opts.inventorySHA256, opts.generatedAt.UTC().Format(time.RFC3339Nano), item.candidate.CaseID, attempt)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func download(ctx context.Context, client *http.Client, item plannedDownload, userAgent string, maxImagePixels int64, fetchIdentity string) (fileHashes, int64, verifiedRepresentation, error) {
	requestURL := item.candidate.Representation.URL
	if item.candidate.Authority == fillercorpus.MetAuthority {
		parsed, err := url.Parse(requestURL)
		if err != nil || fetchIdentity == "" {
			return fileHashes{}, 0, verifiedRepresentation{}, fmt.Errorf("prepare Met cache identity")
		}
		query := parsed.Query()
		query.Set("loomarr-fetch", fetchIdentity)
		parsed.RawQuery = query.Encode()
		requestURL = parsed.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	requestClient := *client
	requestClient.CheckRedirect = redirectPolicy(item.candidate.AllowedMediaHosts)
	resp, err := requestClient.Do(req)
	if err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fileHashes{}, 0, verifiedRepresentation{}, fmt.Errorf("GET: %s", resp.Status)
	}
	responseType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if responseType != item.candidate.Representation.MIMEType {
		return fileHashes{}, 0, verifiedRepresentation{}, fmt.Errorf("response MIME type %q does not match inventory %q", responseType, item.candidate.Representation.MIMEType)
	}
	if resp.ContentLength > item.candidate.Representation.Bytes && resp.ContentLength >= 0 {
		return fileHashes{}, 0, verifiedRepresentation{}, &representationIdentityMismatchError{reason: "Content-Length exceeds inventory size"}
	}
	temp, err := os.CreateTemp(filepath.Dir(item.path), ".filler-corpus-download-*")
	if err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	hashes, size, err := copyAndHash(temp, io.LimitReader(resp.Body, item.candidate.Representation.Bytes+1))
	if err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	if size != item.candidate.Representation.Bytes {
		return fileHashes{}, 0, verifiedRepresentation{}, &representationIdentityMismatchError{reason: fmt.Sprintf("received %d bytes, want %d", size, item.candidate.Representation.Bytes)}
	}
	if !matchesOptional(hashes.sha256, item.candidate.Representation.SHA256) || !matchesOptional(hashes.sha1, item.candidate.Representation.SHA1) || !matchesOptional(hashes.md5, item.candidate.Representation.MD5) {
		return fileHashes{}, 0, verifiedRepresentation{}, &representationIdentityMismatchError{reason: "source checksums do not match inventory"}
	}
	if err := temp.Sync(); err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	if err := temp.Close(); err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	verified, err := verifyDownloadedRepresentation(tempName, item.candidate.Representation, maxImagePixels)
	if err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	if err := os.Link(tempName, item.path); err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, fmt.Errorf("publish without overwrite: %w", err)
	}
	if err := os.Remove(tempName); err != nil {
		return fileHashes{}, 0, verifiedRepresentation{}, err
	}
	ok = true
	return hashes, size, verified, nil
}

func verifyDownloadedRepresentation(filename string, representation fillercorpus.InventoryRepresentation, maxImagePixels int64) (verifiedRepresentation, error) {
	if representation.MIMEType == "video/mp4" {
		return verifiedRepresentation{mediaType: representation.MIMEType}, nil
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return verifiedRepresentation{}, err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width) > maxImagePixels/int64(config.Height) {
		return verifiedRepresentation{}, fmt.Errorf("image configuration is invalid or exceeds %d pixels", maxImagePixels)
	}
	wantFormat := map[string]string{"image/jpeg": "jpeg", "image/png": "png"}[representation.MIMEType]
	if wantFormat == "" || format != wantFormat {
		return verifiedRepresentation{}, fmt.Errorf("decoded image format %q does not match inventory %q", format, representation.MIMEType)
	}
	reader := bytes.NewReader(raw)
	decoded, decodedFormat, err := image.Decode(reader)
	if err != nil || decodedFormat != wantFormat || !completeEncodedImage(raw, wantFormat) || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return verifiedRepresentation{}, fmt.Errorf("image is not one complete %s representation", wantFormat)
	}
	return verifiedRepresentation{mediaType: representation.MIMEType, width: config.Width, height: config.Height}, nil
}

func completeEncodedImage(raw []byte, format string) bool {
	switch format {
	case "jpeg":
		return len(raw) >= 4 && raw[0] == 0xff && raw[1] == 0xd8 && raw[len(raw)-2] == 0xff && raw[len(raw)-1] == 0xd9
	case "png":
		const signatureBytes = 8
		if len(raw) < signatureBytes || !bytes.Equal(raw[:signatureBytes], []byte("\x89PNG\r\n\x1a\n")) {
			return false
		}
		for offset := signatureBytes; offset <= len(raw)-12; {
			length := int64(binary.BigEndian.Uint32(raw[offset : offset+4]))
			end := int64(offset) + 12 + length
			if end > int64(len(raw)) {
				return false
			}
			chunkType := string(raw[offset+4 : offset+8])
			offset = int(end)
			if chunkType == "IEND" {
				return length == 0 && offset == len(raw)
			}
		}
		return false
	default:
		return false
	}
}

func redirectPolicy(allowedHosts []string) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if req.URL == nil {
			return fmt.Errorf("refuse redirect without a destination URL")
		}
		if err := fillercorpus.ValidateMediaURL(req.URL.String(), allowedHosts); err != nil {
			return fmt.Errorf("refuse redirect: %w", err)
		}
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
}

func hashPrivateFile(path string) (fileHashes, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileHashes{}, 0, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fileHashes{}, 0, fmt.Errorf("existing media is not a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return fileHashes{}, 0, err
	}
	defer func() { _ = file.Close() }()
	return copyAndHash(io.Discard, file)
}

func copyAndHash(destination io.Writer, source io.Reader) (fileHashes, int64, error) {
	sha256Hash, sha1Hash, md5Hash := sha256.New(), sha1.New(), md5.New()
	n, err := io.Copy(io.MultiWriter(destination, sha256Hash, sha1Hash, md5Hash), source)
	if err != nil {
		return fileHashes{}, n, err
	}
	return fileHashes{sha256: sumHex(sha256Hash), sha1: sumHex(sha1Hash), md5: sumHex(md5Hash)}, n, nil
}

func sumHex(value hash.Hash) string { return hex.EncodeToString(value.Sum(nil)) }

func matchesOptional(actual, expected string) bool {
	return expected == "" || strings.EqualFold(actual, expected)
}
