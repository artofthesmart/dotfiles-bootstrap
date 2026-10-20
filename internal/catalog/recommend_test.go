package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestRecommendationGoldens(t *testing.T) {
	catalog := MustLoadEmbedded()
	const (
		giB32  = 32 * gibibyte
		giB64  = 64 * gibibyte
		giB256 = 256 * gibibyte
		giB24  = 24 * gibibyte
		giB8   = 8 * gibibyte
	)
	fixtures := []struct {
		name         string
		host         Host
		wantPrimary  string
		wantFallback string
	}{
		{
			name: "windows-shared-amd",
			host: Host{
				OperatingSystem:   "windows",
				Architecture:      "amd64",
				ExecutionMode:     ExecutionIntegratedShared,
				SystemMemoryBytes: pointer(giB64),
				FreeStorageBytes:  pointer(giB256),
				OllamaVersion:     "0.40.6",
			},
			wantPrimary:  "gpt-oss-20b-mxfp4",
			wantFallback: "mistral-nemo-12b-q4km",
		},
		{
			name: "apple-unified",
			host: Host{
				OperatingSystem:   "macos",
				Architecture:      "arm64",
				ExecutionMode:     ExecutionAppleUnified,
				SystemMemoryBytes: pointer(giB64),
				FreeStorageBytes:  pointer(giB256),
				OllamaVersion:     "0.40.6",
			},
			wantPrimary:  "gpt-oss-20b-mxfp4",
			wantFallback: "mistral-nemo-12b-q4km",
		},
		{
			name: "intel-mac-cpu-only",
			host: Host{
				OperatingSystem:   "macos",
				Architecture:      "amd64",
				ExecutionMode:     ExecutionCPUOnly,
				SystemMemoryBytes: pointer(giB32),
				FreeStorageBytes:  pointer(giB256),
				OllamaVersion:     "0.40.6",
			},
			wantPrimary:  "gpt-oss-20b-mxfp4",
			wantFallback: "mistral-nemo-12b-q4km",
		},
		{
			name: "linux-nvidia",
			host: Host{
				OperatingSystem:        "linux",
				Architecture:           "amd64",
				ExecutionMode:          ExecutionDedicatedGPU,
				SystemMemoryBytes:      pointer(giB64),
				AcceleratorMemoryBytes: pointer(giB24),
				FreeStorageBytes:       pointer(giB256),
				OllamaVersion:          "0.40.6",
			},
			wantPrimary:  "qwen3-coder-30b-a3b-q4km",
			wantFallback: "gpt-oss-20b-mxfp4",
		},
		{
			name: "unknown-gpu-memory",
			host: Host{
				OperatingSystem:   "linux",
				Architecture:      "amd64",
				ExecutionMode:     ExecutionDedicatedGPU,
				SystemMemoryBytes: pointer(giB64),
				FreeStorageBytes:  pointer(giB256),
				OllamaVersion:     "0.40.6",
			},
			wantPrimary:  "qwen3-coder-30b-a3b-q4km",
			wantFallback: "gpt-oss-20b-mxfp4",
		},
		{
			name: "low-disk",
			host: Host{
				OperatingSystem:        "linux",
				Architecture:           "amd64",
				ExecutionMode:          ExecutionDedicatedGPU,
				SystemMemoryBytes:      pointer(giB64),
				AcceleratorMemoryBytes: pointer(giB24),
				FreeStorageBytes:       pointer(giB8),
				OllamaVersion:          "0.40.6",
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			selection, err := Recommend(catalog, fixture.host, RecommendOptions{
				License: LicensePolicy{RequireCommercialUse: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			assertSelectionIDs(t, selection, fixture.wantPrimary, fixture.wantFallback)
			assertGolden(t, fixture.name, selection)
		})
	}
}

func TestRecommendationIsDeterministic(t *testing.T) {
	value := MustLoadEmbedded()
	host := Host{
		OperatingSystem:        "linux",
		Architecture:           "amd64",
		ExecutionMode:          ExecutionDedicatedGPU,
		SystemMemoryBytes:      pointer(uint64(64 * gibibyte)),
		AcceleratorMemoryBytes: pointer(uint64(24 * gibibyte)),
		FreeStorageBytes:       pointer(uint64(256 * gibibyte)),
		OllamaVersion:          "0.40.6",
	}
	first, err := Recommend(value, host, RecommendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expected := MustJSON(t, first)
	for i := 0; i < 100; i++ {
		actual, err := Recommend(value, host, RecommendOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(expected, MustJSON(t, actual)) {
			t.Fatalf("iteration %d produced different JSON", i)
		}
	}
}

func TestCandidateTieBreaking(t *testing.T) {
	entries := map[string]Entry{
		"alpha": {ID: "alpha", Download: ByteRange{MaximumBytes: 20}},
		"beta":  {ID: "beta", Download: ByteRange{MaximumBytes: 20}},
		"small": {ID: "small", Download: ByteRange{MaximumBytes: 10}},
	}
	candidates := []CandidateEvaluation{
		{CatalogID: "beta", Eligible: true, Score: 100, Status: StatusRecommended},
		{CatalogID: "alpha", Eligible: true, Score: 100, Status: StatusRecommended},
		{CatalogID: "small", Eligible: true, Score: 100, Status: StatusRecommended},
		{CatalogID: "blocked", Eligible: false, Score: 1000, Status: StatusRecommended},
	}
	entries["blocked"] = Entry{ID: "blocked", Download: ByteRange{MaximumBytes: 1}}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidateLess(candidates[i], candidates[j], entries)
	})
	got := []string{candidates[0].CatalogID, candidates[1].CatalogID, candidates[2].CatalogID, candidates[3].CatalogID}
	want := []string{"small", "alpha", "beta", "blocked"}
	if !bytes.Equal(MustJSON(t, got), MustJSON(t, want)) {
		t.Fatalf("tie order = %v, want %v", got, want)
	}
}

func TestFallbackIsStrictlySmaller(t *testing.T) {
	value := MustLoadEmbedded()
	selection, err := Recommend(value, Host{
		OperatingSystem:   "windows",
		Architecture:      "amd64",
		ExecutionMode:     ExecutionIntegratedShared,
		SystemMemoryBytes: pointer(uint64(64 * gibibyte)),
		FreeStorageBytes:  pointer(uint64(256 * gibibyte)),
		OllamaVersion:     "0.40.6",
	}, RecommendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Primary == nil || selection.Fallback == nil {
		t.Fatalf("selection = %+v", selection)
	}
	entries := entriesByID(value)
	if entries[selection.Fallback.CatalogID].Download.MaximumBytes >= entries[selection.Primary.CatalogID].Download.MaximumBytes {
		t.Fatalf("fallback %s is not smaller than primary %s", selection.Fallback.CatalogID, selection.Primary.CatalogID)
	}
}

func TestSharedAndUnifiedMemoryUseOnePool(t *testing.T) {
	value := MustLoadEmbedded()
	for _, mode := range []ExecutionMode{ExecutionIntegratedShared, ExecutionAppleUnified} {
		selection, err := Recommend(value, Host{
			OperatingSystem:   map[ExecutionMode]string{ExecutionIntegratedShared: "windows", ExecutionAppleUnified: "macos"}[mode],
			Architecture:      map[ExecutionMode]string{ExecutionIntegratedShared: "amd64", ExecutionAppleUnified: "arm64"}[mode],
			ExecutionMode:     mode,
			SystemMemoryBytes: pointer(uint64(64 * gibibyte)),
			FreeStorageBytes:  pointer(uint64(256 * gibibyte)),
			OllamaVersion:     "0.40.6",
		}, RecommendOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Primary == nil {
			t.Fatalf("%s has no primary", mode)
		}
		if selection.Primary.Memory.RequiredAcceleratorBytes != nil || selection.Primary.Memory.AvailableAcceleratorBytes != nil {
			t.Fatalf("%s invented a separate accelerator pool: %+v", mode, selection.Primary.Memory)
		}
	}
}

func TestUnknownDedicatedMemoryIsExplained(t *testing.T) {
	value := MustLoadEmbedded()
	selection, err := Recommend(value, Host{
		OperatingSystem:   "linux",
		Architecture:      "amd64",
		ExecutionMode:     ExecutionDedicatedGPU,
		SystemMemoryBytes: pointer(uint64(64 * gibibyte)),
		FreeStorageBytes:  pointer(uint64(256 * gibibyte)),
		OllamaVersion:     "0.40.6",
	}, RecommendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Primary == nil {
		t.Fatal("expected a conservative CPU-fallback recommendation")
	}
	if !hasExplanation(selection.Primary.Unknowns, "accelerator_memory_unknown") {
		t.Fatalf("unknowns = %+v", selection.Primary.Unknowns)
	}
	if selection.Primary.Offload.Expected != OffloadUnknown {
		t.Fatalf("offload = %+v", selection.Primary.Offload)
	}
}

func TestLicenseAndStatusFilters(t *testing.T) {
	value := MustLoadEmbedded()
	host := Host{
		OperatingSystem:   "linux",
		Architecture:      "amd64",
		ExecutionMode:     ExecutionCPUOnly,
		SystemMemoryBytes: pointer(uint64(128 * gibibyte)),
		FreeStorageBytes:  pointer(uint64(256 * gibibyte)),
		OllamaVersion:     "0.40.6",
	}
	selection, err := Recommend(value, host, RecommendOptions{
		License: LicensePolicy{
			RequireCommercialUse: true,
			AllowedIdentifiers:   []string{"BSD-3-Clause"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Primary != nil {
		t.Fatalf("unexpected primary %s", selection.Primary.CatalogID)
	}
	for _, candidate := range selection.Candidates {
		if !hasExplanation(candidate.Disqualifiers, "license_incompatible") {
			t.Errorf("%s lacks license disqualifier", candidate.CatalogID)
		}
	}

	selection, err = Recommend(value, host, RecommendOptions{IncludeExperimental: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	seenExperimental := false
	seenDeprecated := false
	for _, candidate := range selection.Candidates {
		if candidate.Status == StatusExperimental {
			seenExperimental = !hasExplanation(candidate.Disqualifiers, "experimental_status_excluded")
		}
		if candidate.Status == StatusDeprecated {
			seenDeprecated = !hasExplanation(candidate.Disqualifiers, "deprecated_status_excluded")
		}
	}
	if !seenExperimental || !seenDeprecated {
		t.Fatalf("explicit status inclusion failed: experimental=%v deprecated=%v", seenExperimental, seenDeprecated)
	}
}

func assertSelectionIDs(t *testing.T, selection Selection, primary, fallback string) {
	t.Helper()
	if primary == "" {
		if selection.Primary != nil {
			t.Fatalf("primary = %s, want nil", selection.Primary.CatalogID)
		}
	} else if selection.Primary == nil || selection.Primary.CatalogID != primary {
		t.Fatalf("primary = %+v, want %s", selection.Primary, primary)
	}
	if fallback == "" {
		if selection.Fallback != nil {
			t.Fatalf("fallback = %s, want nil", selection.Fallback.CatalogID)
		}
	} else if selection.Fallback == nil || selection.Fallback.CatalogID != fallback {
		t.Fatalf("fallback = %+v, want %s", selection.Fallback, fallback)
	}
}

func assertGolden(t *testing.T, name string, selection Selection) {
	t.Helper()
	data, err := json.MarshalIndent(selection, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join("testdata", "golden", name+".json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, data) {
		t.Fatalf("golden mismatch for %s; rerun with UPDATE_GOLDEN=1 after reviewing the behavior", name)
	}
}

func entriesByID(value Catalog) map[string]Entry {
	result := make(map[string]Entry, len(value.Entries))
	for _, entry := range value.Entries {
		result[entry.ID] = entry
	}
	return result
}

func pointer[T any](value T) *T {
	return &value
}
