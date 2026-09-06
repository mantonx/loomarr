package fillersafety

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type sherpaProposerConfig struct {
	RuntimePath          string
	RuntimeLibraryPath   string
	EncoderPath          string
	DecoderPath          string
	JoinerPath           string
	TokensPath           string
	KeywordAuthorityPath string
	FFmpegPath           string
}

type sherpaStagedArtifacts struct {
	runtime, library, encoder, decoder, joiner, tokens, keywords string
}

type sherpaProposer struct {
	identity  proposerIdentity
	policySHA string
	ffmpeg    string
	workspace string
	artifacts sherpaStagedArtifacts
	variants  map[string][][]string
	closeOnce sync.Once
	closeErr  error
}

func newSherpaProposerWithContract(ctx context.Context, config sherpaProposerConfig, contract sherpaArtifactContract) (*sherpaProposer, error) {
	if ctx == nil || ctx.Err() != nil || !validSherpaArtifactContract(contract) {
		return nil, fmt.Errorf("spoken-safety acoustic proposer configuration is invalid")
	}
	workspace, err := os.MkdirTemp("", "loomarr-spoken-safety-proposer-")
	if err != nil {
		return nil, fmt.Errorf("create spoken-safety acoustic proposer workspace")
	}
	proposer := &sherpaProposer{workspace: workspace, ffmpeg: config.FFmpegPath}
	cleanup := true
	defer func() {
		if cleanup {
			_ = proposer.Close()
		}
	}()
	proposer.artifacts, err = stageSherpaArtifacts(ctx, workspace, config, contract)
	if err != nil {
		return nil, err
	}
	vocabulary, err := loadSherpaVocabulary(proposer.artifacts.tokens)
	if err != nil {
		return nil, err
	}
	modelIdentity := sherpaModelIdentitySHA256(contract)
	authority, err := loadAcousticKeywordAuthority(config.KeywordAuthorityPath, vocabulary, modelIdentity, contract.bpeModelSHA256)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(proposer.artifacts.keywords, authority.keywords, 0o600); err != nil {
		return nil, fmt.Errorf("stage spoken-safety acoustic keyword authority")
	}
	proposer.policySHA, proposer.variants = authority.authority.PolicySHA256, authority.variants
	configSHA, err := sherpaConfigSHA256(contract, authority)
	if err != nil {
		return nil, fmt.Errorf("identify spoken-safety acoustic proposer configuration")
	}
	proposer.identity = proposerIdentity{
		Kind: proposerExternalModel, Implementation: sherpaImplementation,
		Platform: contract.platform, RuntimeVersion: sherpaRuntimeVersion,
		RuntimeSHA256: sherpaRuntimeIdentitySHA256(contract), ModelSHA256: modelIdentity, ConfigSHA256: configSHA,
	}
	cleanup = false
	return proposer, nil
}

func (p *sherpaProposer) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() { p.closeErr = os.RemoveAll(filepath.Clean(p.workspace)) })
	return p.closeErr
}
