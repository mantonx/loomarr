package filler

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Intake — the ONE route every clip takes into the catalog (§10 V38c).
//
// Watch folder → hash → move into the clip folder as `<hash>.<ext>` → sidecar → catalogued.
// YouTube, Internet Archive and a hand-dropped file all follow it; there are deliberately no
// divergent paths, because a second way in is a second place for the lifecycle, the dedupe and
// the sidecar to be forgotten.
//
// ⚠ **The original filename is captured BEFORE the rename**, and that is load-bearing rather than
// sentimental. §10's grounding rule accepts an era only when the year appears literally in the
// clip's text signals, and the filename is one of them (`Frosted Flakes 1993.mp4`). Renaming to a
// hash without recording the name would permanently destroy that signal — every clip whose era
// came from its filename would become ungrounded. The tagger reads `originalName` from the
// sidecar instead.

// WatchDirName is the watch folder's name when it is derived from the clip folder (§10 V38c) —
// `<filler.dir>/_watch`.
//
// ⚠ Exported because TWO packages must agree on it and neither owns the other: intake creates it,
// and the scan skips it by name so a file still waiting to be filed is never catalogued from its
// arrival path. A literal in both places is a rename away from a catalog that lists clips which
// vanish on the next sync.
//
// ⚠ Underscore-prefixed so it sorts to the top of an operator's file listing and reads as
// Loomarr's, not theirs.
const WatchDirName = "_watch"

// WatchDir resolves the watch folder from the two settings (§10 V38c).
//
// ⚠ The DERIVED default is the point. `filler.watch_dir` defaults to empty rather than to a
// literal `/data/filler/_watch`, because a literal silently stops tracking the moment an operator
// points `filler.dir` at a library on another disk: arrivals keep landing under `/data` while the
// catalog looks elsewhere, and the drop-folder appears broken with both settings looking right.
//
// An explicit watch_dir wins — an operator who mounts a real inbox somewhere else means it.
func WatchDir(clipDir, watchDir string) string {
	if watchDir != "" {
		return watchDir
	}
	if clipDir == "" {
		return "" // nothing configured at all; intake no-ops rather than inventing a path
	}
	return filepath.Join(clipDir, WatchDirName)
}

// IntakeResult reports what one intake pass did.
type IntakeResult struct {
	// Taken counts files moved into the clip folder.
	Taken int
	// Duplicates counts arrivals whose hash was already in the clip folder. The arriving copy is
	// discarded — not catalogued twice, and not left in the watch folder to be re-examined on
	// every pass.
	Duplicates int
	// Skipped counts files that could not be hashed or moved. They stay in the watch folder, so
	// a transient failure retries rather than losing the file.
	Skipped int
}

// TakeIn drains the watch folder into the clip folder.
//
// `fetched` marks the whole pass as Loomarr's own download (the auto-fetch and ingest paths) so
// the sidecar records `fetchedBy` — the held/filed fork's signal. An operator dropping files in
// runs the same code with `fetched=false`.
func TakeIn(watchDir, clipDir string, fetched bool, log func(string, ...any)) (IntakeResult, error) {
	return TakeInFrom(watchDir, clipDir, fetched, "", log)
}

// TakeInWithAcquisitionBinding files watch-folder media while allowing durable acquisition
// authority to verify a claimed arrival and bind its content-addressed destination before any
// move or duplicate deletion. The callback receives both filesystem paths for exact-byte checks
// and their durable relative names; it is never invoked for an unfiled operator clip found
// directly in clipDir.
func TakeInWithAcquisitionBinding(watchDir, clipDir string, fetched bool, log func(string, ...any), bind func(sourcePath, destinationPath, previousPath, filedPath, clipHash string) error) (IntakeResult, error) {
	return takeInFrom(watchDir, clipDir, fetched, "", log, bind)
}

// TakeInFrom preserves the registered source responsible for an unattended arrival. Registered
// folder/library scans set fetched=true so the clip waits for the same grounded admission gate as
// a remote download; a direct hand-copy still uses TakeIn(..., false, ...) as an operator decision.
func TakeInFrom(watchDir, clipDir string, fetched bool, sourceID string, log func(string, ...any)) (IntakeResult, error) {
	return takeInFrom(watchDir, clipDir, fetched, sourceID, log, nil)
}

func takeInFrom(watchDir, clipDir string, fetched bool, sourceID string, log func(string, ...any), bind func(sourcePath, destinationPath, previousPath, filedPath, clipHash string) error) (IntakeResult, error) {
	var res IntakeResult
	if watchDir == "" || clipDir == "" {
		return res, nil
	}
	if err := os.MkdirAll(clipDir, 0o755); err != nil {
		return res, fmt.Errorf("create clip folder: %w", err)
	}

	entries, err := collectMedia(watchDir)
	if err != nil {
		return res, err
	}
	// ⚠ **The CLIP FOLDER is drained too, not only the watch folder** — and this is the case that
	// matters most, because it is what every existing install does. `FILLER_DIR` has always been
	// documented as *the* drop-folder: an operator copies `Frosted Flakes 1993.mp4` straight into
	// it, and did so for every release before V38c.
	//
	// Without this, such a file is scanned, catalogued at its arrival path, and then PRUNED in the
	// same pass — because V38c's `ClipPath` allow-list only accepts `<hash>.<ext>` or
	// `xx/yy/<hash>.<ext>`, so its raw name is not a valid clip id. The catalog reports
	// "1 added, 1 pruned" and holds nothing. Filler silently never works, with a green sync.
	//
	// Found by running the real binary against a real folder; every unit test passed throughout,
	// because they all dropped their fixtures into the WATCH folder.
	if watchDir != clipDir {
		unfiled, err := collectMedia(clipDir, watchDir)
		if err != nil {
			return res, err
		}
		for _, p := range unfiled {
			// Anything already filed under its hash is left ALONE — re-filing it would rewrite
			// the whole catalog on every pass.
			if rel, relErr := filepath.Rel(clipDir, p); relErr == nil && validShardPath(filepath.ToSlash(rel)) {
				continue
			}
			entries = append(entries, p)
		}
	}
	for _, src := range entries {
		id, err := ClipID(src)
		if err != nil {
			// Left where it is: a file being written as we look at it is the common case, and
			// the next pass will find it complete.
			res.Skipped++
			continue
		}

		dst, err := ClipPath(clipDir, id, filepath.Ext(src))
		if err != nil {
			res.Skipped++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			res.Skipped++
			continue
		}
		if bind != nil {
			previousPath, watched := pathWithin(watchDir, src)
			filedPath, filed := pathWithin(clipDir, dst)
			if watched && !filed {
				res.Skipped++
				continue
			}
			if watched {
				if err := bind(src, dst, previousPath, filedPath, id); err != nil {
					if log != nil {
						log("filler intake: could not bind an acquisition manifest before filing a clip", "file", src, "err", err)
					}
					// Leaving claimed bytes in the watch folder is safer than moving or deleting
					// them without an exact durable binding.
					res.Skipped++
					continue
				}
			}
		}
		if _, err := os.Stat(dst); err == nil {
			// A path alias can make src and dst the SAME filesystem object even after every
			// layout-level containment check (notably a bind mount changed between validation
			// and intake). Duplicate cleanup must never unlink the canonical catalog file.
			srcInfo, srcErr := os.Stat(src)
			dstInfo, dstErr := os.Stat(dst)
			if srcErr == nil && dstErr == nil && os.SameFile(srcInfo, dstInfo) {
				continue
			}
			// ⚠ A duplicate. The arriving copy is REMOVED rather than left in place: leaving it
			// would mean re-hashing the same file on every pass forever, and the operator would
			// watch a folder that never drains. The clip itself is not lost — the copy already
			// in the clip folder IS this file, byte for byte, which is what the hash asserts.
			_ = os.Remove(src)
			_ = os.Remove(sidecarPathFor(src))
			res.Duplicates++
			continue
		}

		// ⚠ The name is read BEFORE the move, and written to the sidecar AFTER it. Capturing it
		// afterwards is impossible — by then the only name is the hash.
		original := filepath.Base(src)
		if err := movePath(src, dst); err != nil {
			if log != nil {
				log("filler intake: could not move a clip into the clip folder",
					"file", src, "err", err)
			}
			res.Skipped++
			continue
		}
		// A sidecar the downloader wrote travels with the clip, so its title/description survive
		// to reach the tagger.
		_ = moveIfPresent(sidecarPathFor(src), sidecarPathFor(dst))

		if err := WriteSidecarTags(dst, SidecarTags{OriginalName: original, SourceID: sourceID}, fetched); err != nil && log != nil {
			// Not fatal: the clip is in place and catalogueable. A missing sidecar costs the
			// filename signal, not the file.
			log("filler intake: could not write a sidecar", "clip", dst, "err", err)
		}
		res.Taken++
	}
	return res, nil
}

func pathWithin(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// collectMedia lists media files in the watch folder, recursively.
//
// ⚠ Sidecars are NOT returned as entries — they travel with their clip. Returning them would
// make each one an "unhashable file" and inflate Skipped on every pass.
func collectMedia(dir string, excludedDirs ...string) ([]string, error) {
	excluded := make(map[string]struct{}, len(excludedDirs))
	for _, path := range excludedDirs {
		if path != "" {
			excluded[filepath.Clean(path)] = struct{}{}
		}
	}
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			if _, skip := excluded[filepath.Clean(path)]; skip {
				return fs.SkipDir
			}
			// ⚠ Skip Loomarr's own subdirectories. Draining the CLIP folder walks the same tree
			// the watch folder lives in, so without this a file waiting in `_watch` is collected
			// TWICE in one pass — once as an arrival and once as an unfiled clip — and the second
			// look finds a file the first has already moved. Dot-directories are ours too (the
			// thumbnail cache), and nothing in them is a clip.
			if path != dir && (d.Name() == WatchDirName || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".info.json") || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		if !clipExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("scan watch folder %s: %w", dir, err)
	}
	return out, nil
}

// movePath renames, falling back to copy+delete.
//
// ⚠ The fallback is not optional: `os.Rename` fails with EXDEV when the watch folder and the clip
// folder are on different filesystems, which is the NORMAL container setup (two bind mounts). A
// rename-only implementation would work on the developer's laptop and fail on every real deploy.
//
// ⚠ A MISSING SOURCE IS AN ERROR HERE. It reads like a no-op worth tolerating, and for the
// optional sidecar it is — but the media move runs through this same function, and a vanished
// clip reported as success would be counted as Taken and catalogued as a row pointing at nothing.
// Callers that genuinely do not care use moveIfPresent, which names that choice.
func movePath(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	// Cross-device (EXDEV) is the case the fallback exists for, but any rename failure is worth
	// one copy attempt — the copy either succeeds or reports the real reason.
	if copyErr := copyFile(src, dst); copyErr != nil {
		return copyErr
	}
	return os.Remove(src)
}

// moveIfPresent moves a file that may legitimately not exist — the sidecar, which most
// hand-dropped clips arrive without. Absence is success; anything else is the caller's problem.
func moveIfPresent(src, dst string) error {
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return movePath(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	// ⚠ Written to a temp name and renamed into place, so a crash mid-copy cannot leave a
	// truncated file sitting at the hash's name — where the duplicate check would then treat it
	// as a complete clip and discard every future arrival of the real one.
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
