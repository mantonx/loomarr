package fillervisualsafety

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const (
	candidateBlindHostedInputDir      = "input"
	candidateBlindHostedCarrierName   = "carrier.mp4"
	candidateBlindFramesPerSheet      = 16
	candidateBlindSheetColumns        = 4
	candidateBlindMaximumTileWidth    = 512
	candidateBlindMaximumTileHeight   = 512
	candidateBlindMaximumCarrierBytes = int64(64 << 20)
	candidateBlindMaximumInputBytes   = int64(96 << 20)
	candidateBlindMaximumSheetBytes   = int64(16 << 20)
	candidateBlindCarrierWallTime     = 30 * time.Minute
)

type candidateBlindCarrierRecipe struct {
	Version     string `json:"version"`
	StreamIndex int    `json:"streamIndex"`
	Codec       string `json:"codec"`
	Preset      string `json:"preset"`
	CRF         int    `json:"crf"`
	PixelFormat string `json:"pixelFormat"`
	FPSMode     string `json:"fpsMode"`
	FastStart   bool   `json:"fastStart"`
	Audio       bool   `json:"audio"`
}

func buildCandidateBlindHostedInput(ctx context.Context, bundleRoot, outputRoot, ffmpegPath string, manifest CandidateBlindReviewManifest) (CandidateBlindHostedInput, error) {
	inputDir := filepath.Join(outputRoot, candidateBlindHostedInputDir)
	if err := os.Mkdir(inputDir, 0o700); err != nil {
		return CandidateBlindHostedInput{}, errors.New("create candidate-blind hosted input directory")
	}
	carrier, tool, recipeSHA, err := buildCandidateBlindCarrier(ctx, bundleRoot, inputDir, ffmpegPath, manifest)
	if err != nil {
		return CandidateBlindHostedInput{}, err
	}
	sheets, err := buildCandidateBlindContactSheets(bundleRoot, inputDir, manifest)
	if err != nil {
		return CandidateBlindHostedInput{}, err
	}
	total := carrier.Bytes
	for _, sheet := range sheets {
		if total > candidateBlindMaximumInputBytes-sheet.Bytes {
			return CandidateBlindHostedInput{}, errors.New("candidate-blind hosted review input exceeds its byte ceiling")
		}
		total += sheet.Bytes
	}
	input := CandidateBlindHostedInput{
		SchemaVersion: 1, ReviewPackageSHA256: manifest.SHA256,
		CoverageEvidenceSHA256: manifest.Coverage.SHA256, PolicySHA256: manifest.Policy.SHA256,
		FFmpeg: tool, CarrierRecipeSHA256: recipeSHA, Carrier: carrier, ContactSheets: sheets,
	}
	input.SHA256 = CandidateBlindHostedInputSHA256(input)
	return input, nil
}

func buildCandidateBlindCarrier(ctx context.Context, bundleRoot, inputDir, configured string, manifest CandidateBlindReviewManifest) (CandidateBlindReviewAsset, ToolIdentity, string, error) {
	resolved, tool, err := resolveVisualDecoder(ctx, configured)
	if err != nil {
		return CandidateBlindReviewAsset{}, ToolIdentity{}, "", err
	}
	recipe := candidateBlindCarrierRecipe{
		Version: "candidate-blind-hosted-h264-carrier-v1", StreamIndex: manifest.Plan.Video.Index,
		Codec: "libx264", Preset: "slow", CRF: 10, PixelFormat: "yuv420p", FPSMode: "passthrough",
		FastStart: true, Audio: false,
	}
	recipeSHA := digestJSON(recipe)
	sourcePath := filepath.Join(bundleRoot, reviewDirectoryName, manifest.Source.RelativePath)
	outputPath := filepath.Join(inputDir, candidateBlindHostedCarrierName)
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return CandidateBlindReviewAsset{}, ToolIdentity{}, "", errors.New("reserve candidate-blind hosted video carrier")
	}
	if err := file.Close(); err != nil {
		return CandidateBlindReviewAsset{}, ToolIdentity{}, "", errors.New("reserve candidate-blind hosted video carrier")
	}
	runCtx, cancel := context.WithTimeout(ctx, candidateBlindCarrierWallTime)
	defer cancel()
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-i", sourcePath,
		"-map", "0:" + strconv.Itoa(recipe.StreamIndex), "-an", "-sn", "-dn",
		"-fps_mode", recipe.FPSMode, "-c:v", recipe.Codec, "-preset", recipe.Preset,
		"-crf", strconv.Itoa(recipe.CRF), "-pix_fmt", recipe.PixelFormat,
		"-movflags", "+faststart", "-f", "mp4", "-y", outputPath,
	}
	diagnostics := &portableWorkerDiagnostics{}
	command := exec.CommandContext(runCtx, resolved, args...)
	command.Stdout, command.Stderr = diagnostics, diagnostics
	if err := command.Run(); err != nil || diagnostics.Overflowed() || runCtx.Err() != nil {
		return CandidateBlindReviewAsset{}, ToolIdentity{}, "", fmt.Errorf("build candidate-blind hosted video carrier")
	}
	_, after, err := resolveVisualDecoder(ctx, resolved)
	if err != nil || after != tool {
		return CandidateBlindReviewAsset{}, ToolIdentity{}, "", errors.New("candidate-blind hosted carrier tool identity drifted")
	}
	sha, size, err := hashReviewFile(outputPath, candidateBlindMaximumCarrierBytes)
	if err != nil || size <= 0 {
		return CandidateBlindReviewAsset{}, ToolIdentity{}, "", errors.New("candidate-blind hosted video carrier is invalid")
	}
	return CandidateBlindReviewAsset{
		RelativePath: filepath.ToSlash(filepath.Join(candidateBlindHostedInputDir, candidateBlindHostedCarrierName)),
		SHA256:       sha, Bytes: size,
	}, tool, recipeSHA, nil
}

func buildCandidateBlindContactSheets(bundleRoot, inputDir string, manifest CandidateBlindReviewManifest) ([]CandidateBlindContactSheet, error) {
	if len(manifest.Frames) == 0 {
		return nil, errors.New("candidate-blind hosted review has no frames")
	}
	tileWidth, tileHeight := candidateBlindTileSize(manifest.Plan.Video.Width, manifest.Plan.Video.Height)
	sheets := make([]CandidateBlindContactSheet, 0, (len(manifest.Frames)+candidateBlindFramesPerSheet-1)/candidateBlindFramesPerSheet)
	for first := 0; first < len(manifest.Frames); first += candidateBlindFramesPerSheet {
		last := min(first+candidateBlindFramesPerSheet, len(manifest.Frames))
		rows := (last - first + candidateBlindSheetColumns - 1) / candidateBlindSheetColumns
		canvas := image.NewNRGBA(image.Rect(0, 0, tileWidth*candidateBlindSheetColumns, tileHeight*rows))
		for index := first; index < last; index++ {
			frame := manifest.Frames[index]
			raw, err := readPrivateReviewFile(filepath.Join(bundleRoot, reviewDirectoryName, filepath.FromSlash(frame.RelativePath)), maximumReviewFrameAssetBytes)
			if err != nil {
				return nil, errors.New("read candidate-blind hosted contact frame")
			}
			decoded, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				return nil, errors.New("decode candidate-blind hosted contact frame")
			}
			tile := resizeCandidateBlindImage(decoded, tileWidth, tileHeight)
			position := index - first
			target := image.Rect(
				(position%candidateBlindSheetColumns)*tileWidth,
				(position/candidateBlindSheetColumns)*tileHeight,
				(position%candidateBlindSheetColumns+1)*tileWidth,
				(position/candidateBlindSheetColumns+1)*tileHeight,
			)
			draw.Draw(canvas, target, tile, image.Point{}, draw.Src)
		}
		filename := fmt.Sprintf("contact-%03d.jpg", len(sheets))
		path := filepath.Join(inputDir, filename)
		if err := writeCandidateBlindJPEG(path, canvas); err != nil {
			return nil, err
		}
		sha, size, err := hashReviewFile(path, candidateBlindMaximumSheetBytes)
		if err != nil {
			return nil, errors.New("candidate-blind hosted contact sheet is invalid")
		}
		sheets = append(sheets, CandidateBlindContactSheet{
			CandidateBlindReviewAsset: CandidateBlindReviewAsset{
				RelativePath: filepath.ToSlash(filepath.Join(candidateBlindHostedInputDir, filename)), SHA256: sha, Bytes: size,
			},
			FirstOrdinal: manifest.Frames[first].Ordinal, LastOrdinal: manifest.Frames[last-1].Ordinal,
			FirstObservedMS: manifest.Frames[first].ObservedMS, LastObservedMS: manifest.Frames[last-1].ObservedMS,
			Columns: candidateBlindSheetColumns, Rows: rows,
		})
	}
	return sheets, nil
}

func candidateBlindTileSize(width, height int) (int, int) {
	if width <= candidateBlindMaximumTileWidth && height <= candidateBlindMaximumTileHeight {
		return width, height
	}
	scaleWidth := float64(candidateBlindMaximumTileWidth) / float64(width)
	scaleHeight := float64(candidateBlindMaximumTileHeight) / float64(height)
	scale := min(scaleWidth, scaleHeight)
	return max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale))
}

func resizeCandidateBlindImage(source image.Image, width, height int) *image.NRGBA {
	target := image.NewNRGBA(image.Rect(0, 0, width, height))
	bounds := source.Bounds()
	for y := 0; y < height; y++ {
		sourceY := bounds.Min.Y + y*bounds.Dy()/height
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/width
			target.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return target
}

func writeCandidateBlindJPEG(path string, value image.Image) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("create candidate-blind hosted contact sheet")
	}
	writeErr := jpeg.Encode(file, value, &jpeg.Options{Quality: 100})
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return errors.New("write candidate-blind hosted contact sheet")
	}
	return nil
}
