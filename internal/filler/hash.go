package filler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// Clip identity is a SPARSE CONTENT HASH (§10 V38c) — the file's first 64 KB, its last 64 KB, and
// its size in bytes.
//
// ⚠ **Why identity moved off the path.** V38c allows many watched folders, and a path is only
// unique WITHIN its folder: two folders each holding `ads/coke.mp4` produced the same identity and
// one silently overwrote the other. Prefixing with a folder id would fix the collision; hashing
// fixes it AND answers the question a prefix cannot — *is this the same advert?* — which is what
// lets the scan skip duplicates instead of catalogueing an advert twice into one break.
//
// ⚠ **Why SPARSE rather than a full hash.** A filler catalog is thousands of files and the scan
// runs on a timer; hashing every byte would mean re-reading the whole library every pass. Head +
// tail + size is two seeks, so a 200 MB compilation costs about what a 2 MB clip does. Video
// containers put codec/metadata at the head and index/trailer data at the tail, so two genuinely
// different adverts essentially never match on all three.
//
// ⚠ **It is a HEURISTIC, not a cryptographic guarantee, and that is written down rather than
// discovered.** Two files could share a size, a head and a tail while differing in the middle — in
// practice a truncated duplicate or a deliberately constructed file. The consequence is one clip
// shadowing another in the catalog, which a re-scan does NOT fix by itself. Tolerable for filler
// (clips are a synced cache, and deleting the shadowing file resolves it); intolerable to leave
// unstated, which is the whole reason for this paragraph.
//
// ⚠ **A re-encoded or trimmed file is a DIFFERENT content identity.** A file changed outside
// Loomarr and re-entered through intake is a new clip. An ingest transform also computes a new
// identity, but atomically moves the existing clip's metadata and references to it; it must never
// rewrite bytes under the old hash.

// hashWindow is how much is read from each end. 64 KB comfortably covers container headers
// (moov/ftyp boxes, Matroska headers) without making the read itself the cost.
const hashWindow = 64 * 1024

// ClipID computes a clip's identity from its contents.
//
// The size is mixed in as well as the two windows: two files that share a head and a tail but
// differ in length are different files, and without the size a truncated copy would collide with
// its original — the single most likely real-world duplicate.
func ClipID(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash clip %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("hash clip %s: %w", path, err)
	}
	size := info.Size()
	if size <= 0 {
		// A zero-byte file has no identity worth computing, and the scan rejects it anyway
		// (no probe-able duration). Refusing here means no clip is ever keyed on the hash of
		// nothing — which every empty file would share.
		return "", fmt.Errorf("hash clip %s: empty file", path)
	}

	return ClipIDFromReaderAt(f, size)
}

// ClipIDFromReaderAt computes the same sparse identity for bytes that are not represented by an
// OS path. Download adapters use it while media is still behind their injected storage boundary.
func ClipIDFromReaderAt(r io.ReaderAt, size int64) (string, error) {
	if size <= 0 {
		return "", errors.New("hash clip: empty file")
	}
	h := sha256.New()
	// Size FIRST and fixed-width, so it cannot be confused with content bytes. A decimal string
	// would let a file whose head begins with digits shift the boundary between the two.
	var sizeBuf [8]byte
	binary.BigEndian.PutUint64(sizeBuf[:], uint64(size))
	h.Write(sizeBuf[:])

	// Head.
	if err := hashSection(h, r, 0, hashWindow, size); err != nil {
		return "", fmt.Errorf("hash clip head: %w", err)
	}
	// Tail. ⚠ Skipped when the file is smaller than one window: the head has already covered
	// every byte, and re-hashing an overlapping region would make a small file's identity depend
	// on how much of it happened to be read twice.
	if size > hashWindow {
		if err := hashSection(h, r, size-hashWindow, hashWindow, size); err != nil {
			return "", fmt.Errorf("hash clip tail: %w", err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FileSHA256 returns the full-file identity used by acquisition manifests. Catalog scans keep
// using ClipID's sparse identity; this stronger digest is paid only at acquisition boundaries.
func FileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open file for full digest %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash full file %s: %w", path, err)
	}
	if size <= 0 {
		return "", 0, fmt.Errorf("hash full file %s: empty file", path)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// hashSection reads at most n bytes from offset into the hash.
//
// ⚠ `io.EOF` is tolerated rather than returned: `ReadAt` reports it on a short read at the end of
// a file, which is normal here, and treating it as an error would fail every clip smaller than the
// window. The bytes actually read are what gets hashed, so a short read is complete information.
func hashSection(h io.Writer, r io.ReaderAt, offset, n, size int64) error {
	if offset+n > size {
		n = size - offset
	}
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	read, err := r.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	_, err = h.Write(buf[:read])
	return err
}

// exactFileBytesEqual compares two ownership-boundary artifacts without changing ClipID's sparse
// catalog contract. Both objects stay open for the complete bounded comparison, and cancellation
// is observed between every read.
func exactFileBytesEqual(ctx context.Context, leftPath, rightPath string, maxBytes int64) (bool, error) {
	left, err := os.Open(leftPath)
	if err != nil {
		return false, err
	}
	defer func() { _ = left.Close() }()
	right, err := os.Open(rightPath)
	if err != nil {
		return false, err
	}
	defer func() { _ = right.Close() }()
	leftInfo, err := left.Stat()
	if err != nil {
		return false, err
	}
	rightInfo, err := right.Stat()
	if err != nil {
		return false, err
	}
	if leftInfo.Size() != rightInfo.Size() {
		return false, nil
	}
	if leftInfo.Size() < 0 || maxBytes <= 0 || leftInfo.Size() > maxBytes {
		return false, fmt.Errorf("exact media comparison exceeds %d bytes", maxBytes)
	}
	leftBuf := make([]byte, 64*1024)
	rightBuf := make([]byte, len(leftBuf))
	var compared int64
	for compared < leftInfo.Size() {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		remaining := leftInfo.Size() - compared
		chunk := len(leftBuf)
		if remaining < int64(chunk) {
			chunk = int(remaining)
		}
		if _, err := io.ReadFull(left, leftBuf[:chunk]); err != nil {
			return false, err
		}
		if _, err := io.ReadFull(right, rightBuf[:chunk]); err != nil {
			return false, err
		}
		if !bytes.Equal(leftBuf[:chunk], rightBuf[:chunk]) {
			return false, nil
		}
		compared += int64(chunk)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}
