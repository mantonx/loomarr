package filler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPreserveSourceMasterRetainsExactPortableBytes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "commercial.mov")
	bytes := []byte("source evidence that must survive playback preparation")
	if err := os.WriteFile(source, bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := ClipID(source)
	if err != nil {
		t.Fatal(err)
	}
	tags := SidecarTags{OriginalName: "Cereal commercial master.mov", SourceID: "archive:ads", AcquisitionID: "acq-7"}
	if err := WriteSidecarTags(source, tags, true); err != nil {
		t.Fatal(err)
	}

	asset, err := preserveSourceMaster(context.Background(), root, source, hash, tags)
	if err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(root, filepath.FromSlash(asset.Path))
	got, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bytes) || asset.Role != MediaAssetSourceMaster || asset.Bytes != int64(len(bytes)) {
		t.Fatalf("retained asset = %+v bytes %q", asset, got)
	}
	masterTags, state := ReadSidecarTagsState(master)
	if state != SidecarValid || masterTags.MediaAssets == nil || masterTags.MediaAssets.SourceMaster != asset {
		t.Fatalf("master manifest = %+v state %v", masterTags.MediaAssets, state)
	}
	if masterTags.SourceID != tags.SourceID || masterTags.AcquisitionID != tags.AcquisitionID || masterTags.OriginalName != tags.OriginalName {
		t.Fatalf("portable provenance was lost: %+v", masterTags)
	}

	second, err := preserveSourceMaster(context.Background(), root, source, hash, tags)
	if err != nil || second != asset {
		t.Fatalf("idempotent preserve = %+v, %v; want %+v", second, err, asset)
	}
}

func TestPreserveSourceMasterRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	realPath := filepath.Join(root, "real.mp4")
	if err := os.WriteFile(realPath, []byte("real bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.mp4")
	if err := os.Symlink(realPath, link); err != nil {
		t.Fatal(err)
	}
	hash, err := ClipID(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preserveSourceMaster(context.Background(), root, link, hash, SidecarTags{}); err == nil {
		t.Fatal("symlink source was retained as authority")
	}
}

func TestPreserveSourceMasterRefusesDifferentExistingBytes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "commercial.mp4")
	if err := os.WriteFile(source, []byte("source bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := ClipID(source)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := preserveSourceMaster(context.Background(), root, source, hash, SidecarTags{})
	if err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(root, filepath.FromSlash(asset.Path))
	if err := os.WriteFile(master, []byte("different bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := preserveSourceMaster(context.Background(), root, source, hash, SidecarTags{}); err == nil {
		t.Fatal("different bytes reused under the content-addressed master name")
	}
}
