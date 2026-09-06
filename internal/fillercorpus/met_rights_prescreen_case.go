package fillercorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

const maximumMetMetadataBytes int64 = 1 << 20

func openPrivateMetMetadataRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("met rights pre-screen metadata root must be absolute")
	}
	clean, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve Met rights metadata root: %w", err)
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return "", fmt.Errorf("open Met rights metadata root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("met rights metadata root must be a private regular directory")
	}
	return clean, nil
}

func prescreenMetRightsCase(root string, item InventoryCase) MetRightsPrescreenCase {
	result := MetRightsPrescreenCase{CaseID: item.CaseID, MetadataSHA256: item.MetadataSHA256}
	expectedCache := sourceCacheKey(item.MetadataURL) + ".json"
	if item.MetadataURL != metAPIBase+"/objects/"+item.ItemID || item.MetadataCache != expectedCache || filepath.Base(expectedCache) != expectedCache {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_identity_mismatch")
		return finishMetRightsCase(result)
	}
	filename := filepath.Join(root, expectedCache)
	info, err := os.Lstat(filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.ReasonCodes = append(result.ReasonCodes, "metadata_cache_missing")
		} else {
			result.ReasonCodes = append(result.ReasonCodes, "metadata_cache_unreadable")
		}
		return finishMetRightsCase(result)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_cache_not_regular")
		return finishMetRightsCase(result)
	}
	if info.Mode().Perm()&0o077 != 0 {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_cache_not_private")
	}
	if info.Size() <= 0 || info.Size() > maximumMetMetadataBytes {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_cache_size_invalid")
		return finishMetRightsCase(result)
	}
	file, err := os.Open(filename)
	if err != nil {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_cache_unreadable")
		return finishMetRightsCase(result)
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_cache_changed_while_opening")
		return finishMetRightsCase(result)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumMetMetadataBytes+1))
	if err != nil || int64(len(raw)) != openedInfo.Size() || int64(len(raw)) > maximumMetMetadataBytes {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_cache_unreadable")
		return finishMetRightsCase(result)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != item.MetadataSHA256 {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_sha256_mismatch")
		return finishMetRightsCase(result)
	}
	var object metObject
	if err := json.Unmarshal(raw, &object); err != nil {
		result.ReasonCodes = append(result.ReasonCodes, "metadata_malformed")
		return finishMetRightsCase(result)
	}
	expectedID, err := strconv.ParseInt(item.ItemID, 10, 64)
	if err != nil || object.ObjectID != expectedID {
		result.ReasonCodes = append(result.ReasonCodes, "object_id_mismatch")
	}
	if !object.IsPublicDomain {
		result.ReasonCodes = append(result.ReasonCodes, "public_domain_not_asserted")
	}
	if object.RightsAndReproduction == nil {
		result.ReasonCodes = append(result.ReasonCodes, "rights_and_reproduction_missing")
	} else if strings.TrimSpace(*object.RightsAndReproduction) != "" {
		result.ReasonCodes = append(result.ReasonCodes, "rights_and_reproduction_nonempty")
	}
	if !metObjectProjectsToInventory(object, raw, item) {
		result.ReasonCodes = append(result.ReasonCodes, "inventory_projection_mismatch")
	}
	return finishMetRightsCase(result)
}

func metObjectProjectsToInventory(object metObject, raw []byte, item InventoryCase) bool {
	if len(item.CaptureIDs) != 1 || len(item.RoleHints) != 1 || len(item.Collection) == 0 || item.Collection[0] != "Metropolitan Museum of Art" {
		return false
	}
	terms := make([]string, 0, len(item.Collection)-1)
	for _, collection := range item.Collection[1:] {
		term, ok := strings.CutPrefix(collection, "search-term:")
		if !ok || term == "" {
			return false
		}
		terms = append(terms, term)
	}
	projected := metInventoryCase(object, terms, raw, item.MetadataURL, item.MetadataRetrievedAt, item.CaptureIDs[0], item.RoleHints[0], item.Representation.MIMEType, item.Representation.Bytes)
	return reflect.DeepEqual(projected, item)
}

func finishMetRightsCase(result MetRightsPrescreenCase) MetRightsPrescreenCase {
	slices.Sort(result.ReasonCodes)
	result.ReasonCodes = slices.Compact(result.ReasonCodes)
	if len(result.ReasonCodes) == 0 {
		result.Status = metRightsPrescreenPass
	} else {
		result.Status = metRightsPrescreenHold
	}
	return result
}
