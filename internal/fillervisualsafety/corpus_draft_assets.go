package fillervisualsafety

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"path/filepath"
)

func readVisualCorpusInput(root, relative string, expected VisualCorpusFileIdentity) ([]byte, error) {
	joined := filepath.Join(root, filepath.FromSlash(relative))
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	resolved, err := filepath.EvalSymlinks(joined)
	if rootErr != nil || err != nil || resolved != filepath.Join(resolvedRoot, filepath.FromSlash(relative)) {
		return nil, errors.New("visual corpus input path is invalid")
	}
	within, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || within == "." || within == ".." || filepath.IsAbs(within) ||
		len(within) >= 3 && within[:3] == ".."+string(filepath.Separator) {
		return nil, errors.New("visual corpus input escapes its source root")
	}
	raw, err := readPrivateReviewFile(resolved, expected.Bytes)
	if err != nil || int64(len(raw)) != expected.Bytes || digestBytes(raw) != expected.SHA256 {
		return nil, errors.New("visual corpus input does not reproduce its authority")
	}
	return raw, nil
}

func decodeVisualCorpusRightsEvidence(raw []byte) (VisualCorpusRightsEvidence, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var evidence VisualCorpusRightsEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return VisualCorpusRightsEvidence{}, errors.New("visual corpus rights evidence is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return VisualCorpusRightsEvidence{}, errors.New("visual corpus rights evidence has trailing content")
	}
	return evidence, nil
}

func inspectVisualCorpusImage(raw []byte) (string, int, int, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width) > MaximumVisualCorpusPixels/int64(config.Height) ||
		(format != "jpeg" && format != "png") {
		return "", 0, 0, "", errors.New("visual corpus image configuration is invalid")
	}
	reader := bytes.NewReader(raw)
	decoded, decodedFormat, err := image.Decode(reader)
	if err != nil || decodedFormat != format || !completeVisualCorpusImage(raw, format) || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return "", 0, 0, "", errors.New("visual corpus image is not a complete supported image")
	}
	mediaType := "image/" + format
	return mediaType, config.Width, config.Height, visualDifferenceHash(decoded), nil
}

func completeVisualCorpusImage(raw []byte, format string) bool {
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

func visualDifferenceHash(value image.Image) string {
	bounds := value.Bounds()
	var result uint64
	bit := uint(0)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			leftRed, leftGreen, leftBlue, leftAlpha := value.At(bounds.Min.X+x*(bounds.Dx()-1)/8, bounds.Min.Y+y*(bounds.Dy()-1)/7).RGBA()
			rightRed, rightGreen, rightBlue, rightAlpha := value.At(bounds.Min.X+(x+1)*(bounds.Dx()-1)/8, bounds.Min.Y+y*(bounds.Dy()-1)/7).RGBA()
			left := visualLuma(leftRed, leftGreen, leftBlue, leftAlpha)
			right := visualLuma(rightRed, rightGreen, rightBlue, rightAlpha)
			if left > right {
				result |= uint64(1) << bit
			}
			bit++
		}
	}
	return fmt.Sprintf("%016x", result)
}

func visualLuma(red, green, blue, _ uint32) uint64 {
	return uint64(red)*299 + uint64(green)*587 + uint64(blue)*114
}

func visualCorpusAlias(seed []byte, authoritySHA256, candidateID string) string {
	mac := hmac.New(sha256.New, seed)
	_, _ = mac.Write([]byte(authoritySHA256))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(candidateID))
	return "visual-" + hex.EncodeToString(mac.Sum(nil)[:12])
}

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func reviewAsset(relative string, raw []byte) CandidateBlindReviewAsset {
	return CandidateBlindReviewAsset{RelativePath: relative, SHA256: digestBytes(raw), Bytes: int64(len(raw))}
}
