package prepared

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

const (
	readinessMetadata = ".readiness.json"
	readinessVersion  = 2
)

// BindingKey identifies one Channel's selection of one library item. Publications remain shared
// and channel-free; only this control-plane source selection is channel-aware because audio policy
// may differ by Channel.
type BindingKey struct {
	ChannelID     string `json:"channelId"`
	LibraryItemID string `json:"libraryItemId"`
}

// Binding is the resolved Inventory source for one active policy. Policy includes global tier,
// language, and path-map inputs; ChannelPolicy independently invalidates per-Channel audio changes.
type Binding struct {
	Policy        string  `json:"policy"`
	ChannelPolicy string  `json:"channelPolicy,omitempty"`
	Request       Request `json:"request"`
}

type readinessDocument struct {
	Version  int             `json:"version"`
	Bindings []bindingRecord `json:"bindings"`
}

type bindingRecord struct {
	Key     BindingKey `json:"key"`
	Binding Binding    `json:"binding"`
}

// Readiness is the durable, regenerable source index. Reads never touch disk after OpenReadiness;
// control-plane writes snapshot memory and atomically replace the versioned file.
type Readiness struct {
	root string

	mu        sync.RWMutex
	bindings  map[BindingKey]Binding
	persistMu sync.Mutex
}

// OpenReadiness loads the prepared root's source index. A malformed index returns a usable empty
// value plus an error so composition can warn and retain immediate live fallback; the next
// successful Remember call atomically replaces the bad bytes.
func OpenReadiness(library *Library) (*Readiness, error) {
	index := &Readiness{bindings: make(map[BindingKey]Binding)}
	if library == nil || library.root == "" {
		return index, fmt.Errorf("prepared: open readiness index: %w", ErrInvalidSpecification)
	}
	index.root = library.root
	body, err := os.ReadFile(filepath.Join(index.root, readinessMetadata))
	if errors.Is(err, os.ErrNotExist) {
		return index, nil
	}
	if err != nil {
		return index, fmt.Errorf("prepared: read readiness index: %w", err)
	}
	var document readinessDocument
	if err := json.Unmarshal(body, &document); err != nil || document.Version != readinessVersion {
		if err == nil {
			err = fmt.Errorf("unsupported version %d", document.Version)
		}
		return index, fmt.Errorf("prepared: decode readiness index: %w", err)
	}
	if err := index.load(document); err != nil {
		index.bindings = make(map[BindingKey]Binding)
		return index, err
	}
	return index, nil
}

// Binding returns a source only when every current policy input matches the persisted selection.
func (r *Readiness) Binding(key BindingKey, policy, channelPolicy string) (Request, bool) {
	if r == nil {
		return Request{}, false
	}
	r.mu.RLock()
	binding, ok := r.bindings[key]
	r.mu.RUnlock()
	if !ok || binding.Policy != policy || binding.ChannelPolicy != channelPolicy {
		return Request{}, false
	}
	return binding.Request, true
}

// RememberBinding replaces the prior policy for this Channel/item pair and durably snapshots the
// whole small control file. It is called only by the bounded readiness scheduler, never tune.
func (r *Readiness) RememberBinding(key BindingKey, binding Binding) error {
	return r.RememberBindings(map[BindingKey]Binding{key: binding})
}

// RememberBindings commits one planner pass with one atomic control-file replacement. Memory does
// not expose the new bindings until the durable rename succeeds.
func (r *Readiness) RememberBindings(updates map[BindingKey]Binding) error {
	return r.ReconcileBindings(updates, nil)
}

// ReconcileBindings atomically removes stale selections and installs their replacements. A key
// present in both collections is replaced because removals are applied before validated updates.
// The planner uses this to ensure a failed source re-resolution cannot leave old bytes tuneable.
func (r *Readiness) ReconcileBindings(updates map[BindingKey]Binding, removals []BindingKey) error {
	if r == nil {
		return ErrInvalidSpecification
	}
	normalized := make(map[BindingKey]Binding, len(updates))
	for key, binding := range updates {
		if err := validateBinding(key, &binding); err != nil {
			return err
		}
		normalized[key] = binding
	}
	for _, key := range removals {
		if strings.TrimSpace(key.ChannelID) == "" || strings.TrimSpace(key.LibraryItemID) == "" {
			return ErrInvalidSource
		}
	}
	if len(normalized) == 0 && len(removals) == 0 {
		return nil
	}
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	bindings := r.clone()
	for _, key := range removals {
		delete(bindings, key)
	}
	for key, binding := range normalized {
		bindings[key] = binding
	}
	if err := r.persist(documentFrom(bindings)); err != nil {
		return err
	}
	r.mu.Lock()
	r.bindings = bindings
	r.mu.Unlock()
	return nil
}

func (r *Readiness) load(document readinessDocument) error {
	for _, record := range document.Bindings {
		binding := record.Binding
		if err := validateBinding(record.Key, &binding); err != nil {
			return fmt.Errorf("prepared: invalid readiness binding: %w", err)
		}
		if _, duplicate := r.bindings[record.Key]; duplicate {
			return fmt.Errorf("prepared: duplicate readiness binding %+v", record.Key)
		}
		r.bindings[record.Key] = binding
	}
	return nil
}

func (r *Readiness) persist(document readinessDocument) error {
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("prepared: encode readiness index: %w", err)
	}
	temporary, err := os.CreateTemp(r.root, ".readiness-")
	if err != nil {
		return fmt.Errorf("prepared: create readiness workspace: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("prepared: write readiness index: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("prepared: sync readiness index: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("prepared: close readiness index: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(r.root, readinessMetadata)); err != nil {
		return fmt.Errorf("prepared: publish readiness index: %w", err)
	}
	removeTemporary = false
	return syncDir(r.root)
}

func (r *Readiness) clone() map[BindingKey]Binding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bindings := make(map[BindingKey]Binding, len(r.bindings))
	for key, binding := range r.bindings {
		bindings[key] = binding
	}
	return bindings
}

func documentFrom(bindings map[BindingKey]Binding) readinessDocument {
	document := readinessDocument{Version: readinessVersion}
	for key, binding := range bindings {
		document.Bindings = append(document.Bindings, bindingRecord{Key: key, Binding: binding})
	}
	slices.SortFunc(document.Bindings, func(a, b bindingRecord) int {
		if byChannel := strings.Compare(a.Key.ChannelID, b.Key.ChannelID); byChannel != 0 {
			return byChannel
		}
		return strings.Compare(a.Key.LibraryItemID, b.Key.LibraryItemID)
	})
	return document
}

func validateBinding(key BindingKey, binding *Binding) error {
	if strings.TrimSpace(key.ChannelID) == "" || strings.TrimSpace(key.LibraryItemID) == "" ||
		strings.TrimSpace(binding.Policy) == "" || binding.Request.Source.AudioTrack < 0 ||
		strings.TrimSpace(binding.Request.Source.ItemID) == "" ||
		strings.TrimSpace(binding.Request.Source.SourceID) == "" ||
		strings.TrimSpace(binding.Request.Source.Revision) == "" {
		return ErrInvalidSource
	}
	_, err := keyFor(Specification{SourceFingerprint: "readiness", Rendition: binding.Request.Rendition})
	return err
}
