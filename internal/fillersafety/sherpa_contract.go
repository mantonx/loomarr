package fillersafety

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
)

const (
	sherpaImplementation           = "sherpa-onnx-kws-proposer-v1"
	sherpaRuntimeVersion           = "sherpa-onnx-v1.13.7"
	sherpaModelArchiveSHA256       = "f170013b4716e41b62b9bfd809687c207cef798ef9bc6534d524e17af9b6561a"
	sherpaBPEModelSHA256           = "c8a2a0129c4ab8e463164c142f82d25649661b122c8cd0b7aab5c9e80b90ad24"
	sherpaEncoderSHA256            = "1e721676515bcd42a186979733981213c66c80db680e1cc582dfedf3be76e678"
	sherpaDecoderSHA256            = "f61ebd3eed3773a44d088d53dfae92dbb6aec4839f4dcaee2d402414741663a3"
	sherpaJoinerSHA256             = "eae9da0c7e1e6c6a3f4cc42d167899c388f6c6701b94cb96320e4f55df79624c"
	sherpaTokensSHA256             = "fd2ded4050a55d2b1578870ba8697d02371980217806b7558bd0a5cc60f3ba53"
	sherpaKeywordScore             = 4
	sherpaKeywordThreshold         = "0.05"
	sherpaResultFrameMS      int64 = 40
)

type sherpaArtifactContract struct {
	platform             string
	runtimeArchiveSHA256 string
	runtimeSHA256        string
	librarySHA256        string
	modelArchiveSHA256   string
	encoderSHA256        string
	decoderSHA256        string
	joinerSHA256         string
	tokensSHA256         string
	bpeModelSHA256       string
}

func knownSherpaArtifactContract() (sherpaArtifactContract, error) {
	contract := sherpaArtifactContract{
		platform: runtime.GOOS + "/" + runtime.GOARCH, modelArchiveSHA256: sherpaModelArchiveSHA256,
		encoderSHA256: sherpaEncoderSHA256, decoderSHA256: sherpaDecoderSHA256,
		joinerSHA256: sherpaJoinerSHA256, tokensSHA256: sherpaTokensSHA256, bpeModelSHA256: sherpaBPEModelSHA256,
	}
	switch contract.platform {
	case "darwin/arm64":
		contract.runtimeArchiveSHA256 = "6a78081a617727ebb91a6449aaa9d98fa556272f8f7600a7c2308c9f100e2953"
		contract.runtimeSHA256 = "f0f04f054b72d130b2f1991d369471e3989db3b82e2b3b238728adb223e3514b"
		contract.librarySHA256 = "59665a56e6a95118606bebe583efa8a3528362fc1078f69fc27f36def905bb2c"
	case "linux/amd64":
		contract.runtimeArchiveSHA256 = "e5abe50fae5e25ad6b70bc74b51984ccea77df2571f211833b572fcc0d1c3bef"
		contract.runtimeSHA256 = "282f4f878c67c59ea9305f9f54b77380269b87e24516da9f07d1a98efe5e931d"
		contract.librarySHA256 = "c85f471e1bd5059a4556038f7f5288fa41141647613688452ae7de4879150903"
	case "linux/arm64":
		contract.runtimeArchiveSHA256 = "7ab34c29ad9927e772f32be43efddd5e971987dc59e5f9aa3c09513348e4505b"
		contract.runtimeSHA256 = "b20bfb95be222864ea87330f0b14a9c8182c7dc5aa56ba59546999dafdd02724"
		contract.librarySHA256 = "d860f5968f5a1ed63533e9ed198aa747ca9fe289028129877f428556089f6874"
	default:
		return sherpaArtifactContract{}, fmt.Errorf("spoken-safety acoustic proposer platform is unsupported")
	}
	return contract, nil
}

func validSherpaArtifactContract(contract sherpaArtifactContract) bool {
	return boundedAuthorityID(contract.platform) && validSHA256(contract.runtimeArchiveSHA256) && validSHA256(contract.runtimeSHA256) && validSHA256(contract.librarySHA256) && validSHA256(contract.modelArchiveSHA256) && validSHA256(contract.encoderSHA256) && validSHA256(contract.decoderSHA256) && validSHA256(contract.joinerSHA256) && validSHA256(contract.tokensSHA256) && validSHA256(contract.bpeModelSHA256)
}

func sherpaConfigSHA256(contract sherpaArtifactContract, authority loadedAcousticAuthority) (string, error) {
	raw, err := json.Marshal(struct {
		ContractVersion, PolicySHA256, KeywordAuthoritySHA256, ModelSHA256, BPEModelSHA256 string
		KeywordScore                                                                       int
		KeywordThreshold                                                                   string
		ResultFrameMS                                                                      int64
	}{acousticKeywordAuthorityContractVersion, authority.authority.PolicySHA256, authority.sha256, sherpaModelIdentitySHA256(contract), contract.bpeModelSHA256, sherpaKeywordScore, sherpaKeywordThreshold, sherpaResultFrameMS})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func sherpaRuntimeIdentitySHA256(contract sherpaArtifactContract) string {
	return canonicalSherpaManifestSHA256(struct {
		Contract, Version, Platform, ArchiveSHA256, ExecutableSHA256, LibrarySHA256 string
	}{"sherpa-runtime-manifest-v1", sherpaRuntimeVersion, contract.platform, contract.runtimeArchiveSHA256, contract.runtimeSHA256, contract.librarySHA256})
}

func sherpaModelIdentitySHA256(contract sherpaArtifactContract) string {
	return canonicalSherpaManifestSHA256(struct {
		Contract, ArchiveSHA256, EncoderSHA256, DecoderSHA256, JoinerSHA256, TokensSHA256 string
	}{"sherpa-model-manifest-v1", contract.modelArchiveSHA256, contract.encoderSHA256, contract.decoderSHA256, contract.joinerSHA256, contract.tokensSHA256})
}

func canonicalSherpaManifestSHA256(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
