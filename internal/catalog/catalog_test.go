package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEmbeddedCatalogConformsToContract(t *testing.T) {
	value, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	if value.CatalogVersion != "1.0.0" {
		t.Fatalf("catalog version = %q", value.CatalogVersion)
	}
	if value.ReviewedAt != "2026-10-20" {
		t.Fatalf("reviewed_at = %q", value.ReviewedAt)
	}
	if len(value.Entries) != 5 {
		t.Fatalf("entry count = %d", len(value.Entries))
	}
	statuses := map[Status]bool{}
	for _, entry := range value.Entries {
		statuses[entry.Status] = true
		if entry.Context.AdvertisedNativeTokens < RequiredContextTokens {
			t.Errorf("%s native context = %d", entry.ID, entry.Context.AdvertisedNativeTokens)
		}
		if strings.Contains(strings.ToLower(entry.Model.OllamaName), "cloud") {
			t.Errorf("%s is cloud-only", entry.Model.OllamaName)
		}
		if entry.Source.ExpectedModelDigest == nil {
			t.Errorf("%s lacks an expected model digest", entry.ID)
		}
	}
	for _, status := range []Status{StatusRecommended, StatusFallback, StatusExperimental, StatusDeprecated} {
		if !statuses[status] {
			t.Errorf("catalog lacks %s entry", status)
		}
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(MustJSON(t, MustLoadEmbedded()), &raw); err != nil {
		t.Fatal(err)
	}
	raw["unreviewed"] = true
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Decode error = %v", err)
	}
}

func TestCatalogInvalidCases(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Catalog)
		want   string
	}{
		{
			name: "duplicate stable ID",
			mutate: func(value *Catalog) {
				value.Entries[1].ID = value.Entries[0].ID
			},
			want: "duplicates stable ID",
		},
		{
			name: "cloud tag",
			mutate: func(value *Catalog) {
				value.Entries[0].Model.OllamaName = "example:cloud"
			},
			want: "cloud-only tags are forbidden",
		},
		{
			name: "native context below minimum",
			mutate: func(value *Catalog) {
				value.Entries[0].Context.AdvertisedNativeTokens = RequiredContextTokens - 1
			},
			want: "must be at least 65536",
		},
		{
			name: "reversed byte range",
			mutate: func(value *Catalog) {
				value.Entries[0].Download.MinimumBytes = value.Entries[0].Download.MaximumBytes + 1
			},
			want: "minimum_bytes cannot exceed",
		},
		{
			name: "digest not pinned",
			mutate: func(value *Catalog) {
				value.Entries[0].Source.ExpectedModelDigest = nil
			},
			want: "must pin one sha256",
		},
		{
			name: "duplicate host tier",
			mutate: func(value *Catalog) {
				value.Entries[0].HostTiers[1].ExecutionMode = value.Entries[0].HostTiers[0].ExecutionMode
			},
			want: "duplicates",
		},
		{
			name: "separate memory invented for unified",
			mutate: func(value *Catalog) {
				memory := uint64(8 * gibibyte)
				value.Entries[0].HostTiers[1].MinimumAcceleratorMemoryBytes = &memory
			},
			want: "must be null for shared",
		},
		{
			name: "evidence after review",
			mutate: func(value *Catalog) {
				value.Entries[0].EvidenceDate = "2026-10-21"
			},
			want: "cannot be after reviewed_at",
		},
		{
			name: "invalid commercial classification",
			mutate: func(value *Catalog) {
				value.Entries[0].License.CommercialUse = "maybe"
			},
			want: "commercial_use",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := MustLoadEmbedded()
			test.mutate(&value)
			err := Validate(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRepositoryFindAndCopies(t *testing.T) {
	repository, err := NewEmbeddedRepository()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := repository.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entries[0].KnownCaveats[0] = "mutated"

	found, err := repository.Find(context.Background(), entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.KnownCaveats[0] == "mutated" {
		t.Fatal("repository returned mutable shared catalog data")
	}
	if _, err := repository.Find(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Find missing error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.List(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("List cancelled error = %v", err)
	}
}

func MustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
