package fillersafety

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxSherpaArtifactBytes = int64(64 << 20)

func stageSherpaArtifacts(ctx context.Context, workspace string, config sherpaProposerConfig, contract sherpaArtifactContract) (sherpaStagedArtifacts, error) {
	binDir, libDir, modelDir := filepath.Join(workspace, "bin"), filepath.Join(workspace, "lib"), filepath.Join(workspace, "model")
	for _, dir := range []string{binDir, libDir, modelDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return sherpaStagedArtifacts{}, fmt.Errorf("stage spoken-safety acoustic proposer artifacts")
		}
	}
	artifacts := sherpaStagedArtifacts{
		runtime: filepath.Join(binDir, "sherpa-onnx-keyword-spotter"), library: filepath.Join(libDir, filepath.Base(config.RuntimeLibraryPath)),
		encoder: filepath.Join(modelDir, "encoder.onnx"), decoder: filepath.Join(modelDir, "decoder.onnx"), joiner: filepath.Join(modelDir, "joiner.onnx"),
		tokens: filepath.Join(modelDir, "tokens.txt"), keywords: filepath.Join(workspace, "keywords.txt"),
	}
	files := []struct {
		source, destination, sha string
		mode                     os.FileMode
	}{
		{config.RuntimePath, artifacts.runtime, contract.runtimeSHA256, 0o700},
		{config.RuntimeLibraryPath, artifacts.library, contract.librarySHA256, 0o600},
		{config.EncoderPath, artifacts.encoder, contract.encoderSHA256, 0o600},
		{config.DecoderPath, artifacts.decoder, contract.decoderSHA256, 0o600},
		{config.JoinerPath, artifacts.joiner, contract.joinerSHA256, 0o600},
		{config.TokensPath, artifacts.tokens, contract.tokensSHA256, 0o600},
	}
	for _, file := range files {
		if err := copyVerifiedSherpaArtifact(ctx, file.source, file.destination, file.sha, file.mode); err != nil {
			return sherpaStagedArtifacts{}, err
		}
	}
	return artifacts, nil
}

func copyVerifiedSherpaArtifact(ctx context.Context, source, destination, expectedSHA string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stage spoken-safety acoustic proposer artifact")
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSherpaArtifactBytes {
		return fmt.Errorf("spoken-safety acoustic proposer artifact is invalid")
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("spoken-safety acoustic proposer artifact is invalid")
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("stage spoken-safety acoustic proposer artifact")
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, maxSherpaArtifactBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written != info.Size() || written > maxSherpaArtifactBytes || hex.EncodeToString(hash.Sum(nil)) != expectedSHA {
		return fmt.Errorf("spoken-safety acoustic proposer artifact is invalid")
	}
	return nil
}

func loadSherpaVocabulary(path string) (map[string]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("spoken-safety acoustic vocabulary is invalid")
	}
	defer func() { _ = file.Close() }()
	vocabulary := make(map[string]struct{})
	scanner := bufio.NewScanner(io.LimitReader(file, 64<<10))
	for index := 0; scanner.Scan(); index++ {
		fields := strings.Fields(scanner.Text())
		id, parseErr := strconv.Atoi(lastString(fields))
		if len(fields) != 2 || !validAcousticToken(firstString(fields)) || parseErr != nil || id != index {
			return nil, fmt.Errorf("spoken-safety acoustic vocabulary is invalid")
		}
		vocabulary[fields[0]] = struct{}{}
	}
	if scanner.Err() != nil || len(vocabulary) == 0 || len(vocabulary) > 100_000 {
		return nil, fmt.Errorf("spoken-safety acoustic vocabulary is invalid")
	}
	return vocabulary, nil
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func lastString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}
