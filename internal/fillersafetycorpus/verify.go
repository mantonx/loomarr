package fillersafetycorpus

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

type verifiedVCTKMember struct {
	transcriptRaw []byte
}

func verifyVCTKInputs(ctx context.Context, loaded loadedVCTK, config PrepareVCTKConfig) (map[string]verifiedVCTKMember, int64, error) {
	result := make(map[string]verifiedVCTKMember, len(loaded.authority.Members))
	total := int64(len(loaded.authorityRaw) + len(loaded.seed))
	if total > config.MaximumInputBytes {
		return nil, 0, fmt.Errorf("VCTK inputs exceed byte ceiling")
	}
	type verifiedFile struct {
		authority FileAuthority
		path      string
	}
	verifiedFiles := make(map[string]verifiedFile)
	verify := func(authority FileAuthority, maximum int64) (string, error) {
		if authority.Bytes > maximum {
			return "", fmt.Errorf("VCTK input exceeds per-file ceiling")
		}
		if previous, ok := verifiedFiles[authority.Path]; ok {
			if previous.authority != authority {
				return "", fmt.Errorf("VCTK input path has conflicting authorities")
			}
			return previous.path, nil
		}
		if authority.Bytes > config.MaximumInputBytes-total {
			return "", fmt.Errorf("VCTK inputs exceed byte ceiling")
		}
		path, err := verifiedMemberPath(loaded.root, authority, maximum)
		if err != nil {
			return "", err
		}
		total += authority.Bytes
		verifiedFiles[authority.Path] = verifiedFile{authority: authority, path: path}
		return path, nil
	}
	for _, evidence := range []FileAuthority{loaded.authority.License, loaded.authority.Readme, loaded.authority.RightsReviewEvidence} {
		if _, err := verify(evidence, maximumReleaseAuthorityBytes); err != nil {
			return nil, 0, fmt.Errorf("VCTK release evidence bytes are invalid: %w", err)
		}
	}
	for index, member := range loaded.authority.Members {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if _, err := verify(member.Audio, config.MaximumInputBytes); err != nil {
			return nil, 0, fmt.Errorf("VCTK member %d audio bytes are invalid: %w", index+1, err)
		}
		if _, err := verify(member.Transcript, maximumTranscriptBytes); err != nil {
			return nil, 0, fmt.Errorf("VCTK member %d transcript bytes are invalid: %w", index+1, err)
		}
		transcript, err := readVerifiedMember(loaded.root, member.Transcript, maximumTranscriptBytes)
		if err != nil {
			return nil, 0, fmt.Errorf("VCTK member %d transcript bytes are invalid: %w", index+1, err)
		}
		if _, err := verify(member.ScreeningEvidence, maximumReleaseAuthorityBytes); err != nil {
			return nil, 0, fmt.Errorf("VCTK member %d screening evidence is invalid: %w", index+1, err)
		}
		if !utf8.Valid(transcript) || len(strings.TrimSpace(string(transcript))) == 0 || strings.ContainsRune(string(transcript), 0) {
			return nil, 0, fmt.Errorf("VCTK member %d transcript is not bounded non-empty UTF-8", index+1)
		}
		result[vctkMemberKey(member)] = verifiedVCTKMember{transcriptRaw: transcript}
	}
	return result, total, nil
}

func vctkMemberKey(member VCTKMember) string {
	return member.SpeakerID + "\x00" + member.UtteranceID + "\x00" + member.Microphone
}

func verifyWrappedOutput(path string, expected wrappedMedia) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.Bytes {
		return fmt.Errorf("wrapped output is not the expected regular file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	digest, bytes, err := hashRegularFile(path, expected.Bytes)
	if err != nil || digest != expected.SHA256 || bytes != expected.Bytes {
		return fmt.Errorf("wrapped output bytes do not match wrapper result")
	}
	return nil
}

func snapshotVerifiedMember(root string, authority FileAuthority, output string, maximum int64) error {
	_, input, err := openVerifiedMember(root, authority, maximum)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(output)
		}
	}()
	hash := sha256.New()
	bytes, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input, maximum+1))
	if err != nil || bytes != authority.Bytes || fmt.Sprintf("%x", hash.Sum(nil)) != authority.SHA256 {
		return fmt.Errorf("member bytes do not match authority")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	written = true
	return nil
}
