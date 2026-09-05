package inventory

import (
	"context"
	"sort"
	"strings"
	"time"
)

type service struct{ repository Repository }

func New(repository Repository) Service { return &service{repository: repository} }

func (s *service) ApplySnapshot(ctx context.Context, snapshot Snapshot) (ItemID, error) {
	clean, err := ValidateSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	return s.repository.ApplyInventorySnapshot(ctx, clean)
}

func (s *service) Item(ctx context.Context, ref ItemRef) (Item, bool, error) {
	if err := ValidateItemRef(ref); err != nil {
		return Item{}, false, err
	}
	return s.repository.InventoryItem(ctx, ref)
}

func (s *service) ResolveSource(ctx context.Context, request SourceRequest) (ResolvedSource, bool, error) {
	if err := ValidateItemRef(request.Item); err != nil {
		return ResolvedSource{}, false, err
	}
	item, ok, err := s.repository.InventoryItem(ctx, request.Item)
	if err != nil || !ok {
		return ResolvedSource{}, false, err
	}
	wanted := make(map[SourceKind]bool, len(request.Kinds))
	for _, kind := range request.Kinds {
		wanted[kind] = true
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}
	for _, source := range item.Sources {
		if len(wanted) > 0 && !wanted[source.Kind] {
			continue
		}
		origins := append([]SourceOrigin(nil), source.Origins...)
		sort.SliceStable(origins, func(i, j int) bool {
			return origins[i].Observation.ObservedAt.After(origins[j].Observation.ObservedAt)
		})
		for _, origin := range origins {
			if !origin.MissingAt.IsZero() || strings.TrimSpace(source.Revision) == "" {
				continue
			}
			observation := origin.Observation
			measurement := source.Measurement
			if measurement != nil && measurement.Revision == source.Revision &&
				observationCovers(measurement.Observation.Coverage, request.RequiredCoverage) &&
				(!observationCovers(observation.Coverage, request.RequiredCoverage) ||
					measurement.Observation.ObservedAt.After(observation.ObservedAt)) {
				observation = measurement.Observation
			}
			if request.MaxAge > 0 && now.Sub(observation.ObservedAt) > request.MaxAge {
				continue
			}
			if !observationCovers(observation.Coverage, request.RequiredCoverage) {
				continue
			}
			return ResolvedSource{
				ID: source.ID, ItemID: item.ID, Kind: source.Kind, Revision: source.Revision,
				Locator: origin.Locator, Observation: observation, Measurement: measurement,
			}, true, nil
		}
	}
	return ResolvedSource{}, false, nil
}

func observationCovers(coverage map[string]Coverage, required []string) bool {
	for _, key := range required {
		if coverage[key] != CoveragePresent && coverage[key] != CoverageEmpty {
			return false
		}
	}
	return true
}

func (s *service) RecordMeasurement(ctx context.Context, measurement Measurement) error {
	clean, err := ValidateMeasurement(measurement)
	if err != nil {
		return err
	}
	return s.repository.RecordInventoryMeasurement(ctx, clean)
}

func (s *service) MarkUnseen(ctx context.Context, authority AuthorityID, at time.Time, seen []OriginKey) error {
	authority = AuthorityID(strings.TrimSpace(string(authority)))
	if authority == "" || at.IsZero() {
		return ErrInvalid
	}
	clean := make([]OriginKey, 0, len(seen))
	for _, key := range seen {
		key.Authority = AuthorityID(strings.TrimSpace(string(key.Authority)))
		key.ExternalItemID = strings.TrimSpace(key.ExternalItemID)
		if key.Authority != authority || key.ExternalItemID == "" {
			return ErrInvalid
		}
		clean = append(clean, key)
	}
	return s.repository.MarkInventoryUnseen(ctx, authority, at.UTC(), clean)
}
