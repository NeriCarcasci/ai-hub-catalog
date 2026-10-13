package releasecandidate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	testBenchmarkRevision = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testCatalogRevision   = "cccccccccccccccccccccccccccccccccccccccc"
)

func TestValidateAndStage(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	report, err := ValidateAndStage(cfg)
	if err != nil {
		t.Fatalf("ValidateAndStage() error = %v", err)
	}
	if report.CatalogFiles != 2 || report.BenchmarkFiles != 4 || report.BenchmarkModels != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}

	assertMode(t, filepath.Join(cfg.OutputDir, "app", "data", "validated-models-catalog.yaml"), 0o644)
	assertMode(t, filepath.Join(cfg.OutputDir, "app", "benchmarks", "example", "model", "metadata.json"), 0o644)

	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "app", "data", "models-index.yaml")); !os.IsNotExist(err) {
		t.Fatalf("producer-only index was staged: %v", err)
	}
}

func TestValidateAndStageRejectsIncompatibleBenchmark(t *testing.T) {
	cfg := newTestCandidate(t, "different/model")
	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "is not present in the runtime model catalogs") {
		t.Fatalf("expected catalog compatibility error, got %v", err)
	}
	if _, statErr := os.Stat(cfg.OutputDir); !os.IsNotExist(statErr) {
		t.Fatalf("invalid candidate must not be staged, stat error = %v", statErr)
	}
}

func TestValidateAndStageAcceptsCapitalizationDifference(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, filepath.Join(cfg.CatalogDir, "validated-models-catalog.yaml"), "source: test\nmodels:\n  - name: Example/Model\n    artifacts:\n      - uri: oci://example.invalid/model:1\n", 0o644)
	writeQualificationReport(t, cfg, "example/model")
	if _, err := ValidateAndStage(cfg); err != nil {
		t.Fatalf("capitalization-only ID difference should be compatible: %v", err)
	}
}

func TestValidateAndStageRejectsManifestMismatch(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, filepath.Join(cfg.BenchmarkDir, "unexpected.json"), `{}`, 0o644)
	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "unexpected benchmark file") || !strings.Contains(err.Error(), "missing from manifest.json") {
		t.Fatalf("expected manifest and allowlist errors, got %v", err)
	}
}

func TestValidateAndStageRejectsDuplicateArtifactID(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	records := "{\"id\":\"same\",\"model_id\":\"example/model\"}\n{\"id\":\"same\",\"model_id\":\"example/model\"}\n"
	writeFile(t, filepath.Join(cfg.BenchmarkDir, "example", "model", "evaluations.ndjson"), records, 0o644)
	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "duplicate benchmark artifact id") {
		t.Fatalf("expected duplicate artifact error, got %v", err)
	}
}

func TestValidateAndStageRequiresLoaderChecksForProductCandidate(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	cfg.LoaderCheckDir = ""
	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "resolve Model Catalog loader check directory") {
		t.Fatalf("expected missing loader-check error, got %v", err)
	}
	if _, statErr := os.Stat(cfg.OutputDir); !os.IsNotExist(statErr) {
		t.Fatalf("candidate that skipped product loaders must not remain staged, stat error = %v", statErr)
	}
}

func TestValidateAndStageRunsEveryLoaderCheck(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, cfg.SupportedVersionList, "rhoai-one\nrhoai-two\n", 0o644)
	check := `#!/bin/sh
test -r "$CATALOG_DATA_DIR/validated-models-catalog.yaml"
test -r "$BENCHMARK_DATA_DIR/manifest.json"
test -d "$RELEASE_CANDIDATE_ROOT"
printf 'MODEL_CATALOG_LOADER_CHECK_OK=%s\n' "$MODEL_CATALOG_VERSION"
`
	if err := os.Remove(filepath.Join(cfg.LoaderCheckDir, "fixture-v1")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cfg.LoaderCheckDir, "rhoai-one"), check, 0o755)
	writeFile(t, filepath.Join(cfg.LoaderCheckDir, "rhoai-two"), check, 0o755)
	if _, err := ValidateAndStage(cfg); err != nil {
		t.Fatalf("ValidateAndStage() loader check error = %v", err)
	}
}

func TestValidateAndStageRejectsQualificationReportTampering(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*qualificationReport)
		wantError string
	}{
		{
			name:      "payload tree digest",
			mutate:    func(report *qualificationReport) { report.PayloadSHA256 = strings.Repeat("0", 64) },
			wantError: "does not match qualified payload tree digest",
		},
		{
			name:      "output manifest digest",
			mutate:    func(report *qualificationReport) { report.OutputManifestSHA256 = strings.Repeat("0", 64) },
			wantError: "does not match payload manifest digest",
		},
		{
			name: "catalog digest",
			mutate: func(report *qualificationReport) {
				report.Catalog.Files[0].SHA256 = strings.Repeat("0", 64)
			},
			wantError: "does not match validated catalog digest",
		},
		{
			name:      "payload counts",
			mutate:    func(report *qualificationReport) { report.Payload.Files++ },
			wantError: "does not match actual file count",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := newTestCandidate(t, "example/model")
			report := readQualificationReport(t, cfg.QualificationReport)
			test.mutate(&report)
			writeJSON(t, cfg.QualificationReport, report)

			_, err := ValidateAndStage(cfg)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected %q, got %v", test.wantError, err)
			}
		})
	}
}

func TestValidateAndStageRejectsQualificationRevisionMismatch(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	cfg.ExpectedCatalogRevision = strings.Repeat("d", 40)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "does not match expected catalog revision") {
		t.Fatalf("expected catalog revision mismatch, got %v", err)
	}
}

func TestValidateAndStageRequiresApprovedQualification(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	report := readQualificationReport(t, cfg.QualificationReport)
	report.Exclusions.ReviewStatus = "pending"
	report.Exclusions.ReviewReference = ""
	writeJSON(t, cfg.QualificationReport, report)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "review_status") || !strings.Contains(err.Error(), "review_reference") {
		t.Fatalf("expected approval errors, got %v", err)
	}
}

func TestValidateAndStageStrictlyParsesQualificationReport(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	data, err := os.ReadFile(cfg.QualificationReport)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "{", `{"version":"tampered",`, 1))
	if err := os.WriteFile(cfg.QualificationReport, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `duplicate object key "version"`) {
		t.Fatalf("expected strict duplicate-key error, got %v", err)
	}
}

func TestValidateAndStageRejectsMissingLoaderVersion(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, cfg.SupportedVersionList, "fixture-v1\nmissing-v2\n", 0o644)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "missing: [missing-v2]") {
		t.Fatalf("expected missing loader version error, got %v", err)
	}
}

func TestValidateAndStageRejectsPendingSupportedVersionMatrix(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, cfg.SupportedVersionList, "# pending owner review\n", 0o644)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "is empty; product release remains blocked") {
		t.Fatalf("expected pending supported-version matrix error, got %v", err)
	}
}

func TestValidateAndStageRejectsExtraLoaderVersion(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, filepath.Join(cfg.LoaderCheckDir, "unexpected-v2"), "#!/bin/sh\nexit 0\n", 0o755)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "extra: [unexpected-v2]") {
		t.Fatalf("expected extra loader version error, got %v", err)
	}
}

func TestValidateAndStageRejectsNoOpLoader(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, filepath.Join(cfg.LoaderCheckDir, "fixture-v1"), "#!/bin/sh\nexit 0\n", 0o755)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "did not attest successful loading") {
		t.Fatalf("expected loader attestation error, got %v", err)
	}
}

func TestValidateAndStageRejectsDuplicateJSONKeys(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	metadata := `{"id":"example/model","provider_id":"example","provider_id":"other"}`
	writeFile(t, filepath.Join(cfg.BenchmarkDir, "example", "model", "metadata.json"), metadata, 0o644)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `duplicate object key "provider_id"`) {
		t.Fatalf("expected duplicate JSON key error, got %v", err)
	}
}

func TestValidateAndStageRejectsDuplicateYAMLKeys(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	catalog := "source: test\nsource: duplicate\nmodels:\n  - name: example/model\n    artifacts:\n      - uri: oci://example.invalid/model:1\n"
	writeFile(t, filepath.Join(cfg.CatalogDir, "validated-models-catalog.yaml"), catalog, 0o644)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `duplicate mapping key "source"`) {
		t.Fatalf("expected duplicate YAML key error, got %v", err)
	}
}

func TestValidateAndStageRejectsProducerFileInRuntimeAllowlist(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, cfg.RuntimeFileList, "models-index.yaml\n", 0o644)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "must be a generated *-catalog.yaml basename") {
		t.Fatalf("expected runtime allowlist classification error, got %v", err)
	}
}

func TestValidateAndStageRejectsOutputResolvedIntoInput(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	link := filepath.Join(t.TempDir(), "benchmark-link")
	if err := os.Symlink(cfg.BenchmarkDir, link); err != nil {
		t.Fatal(err)
	}
	cfg.OutputDir = filepath.Join(link, "candidate")

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "overlaps benchmark directory") {
		t.Fatalf("expected resolved output containment error, got %v", err)
	}
}

func TestValidateAndStagePreservesPreviousCandidateOnValidationFailure(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	if _, err := ValidateAndStage(cfg); err != nil {
		t.Fatalf("initial ValidateAndStage() error = %v", err)
	}
	staged := filepath.Join(cfg.OutputDir, "app", "benchmarks", "example", "model", "metadata.json")
	want, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cfg.BenchmarkDir, "example", "model", "metadata.json"), `{"id":"different/model","provider_id":"example"}`, 0o644)

	if _, err := ValidateAndStage(cfg); err == nil {
		t.Fatal("expected replacement candidate validation to fail")
	}
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("previous candidate was removed: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("previous candidate changed: got %q, want %q", got, want)
	}
}

func TestValidateAndStageRejectsLoaderMutation(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	check := `#!/bin/sh
printf '\n# mutation\n' >> "$CATALOG_DATA_DIR/validated-models-catalog.yaml"
printf 'MODEL_CATALOG_LOADER_CHECK_OK=%s\n' "$MODEL_CATALOG_VERSION"
`
	writeFile(t, filepath.Join(cfg.LoaderCheckDir, "fixture-v1"), check, 0o755)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "loader checks modified the staged payload") {
		t.Fatalf("expected loader mutation error, got %v", err)
	}
	if _, statErr := os.Stat(cfg.OutputDir); !os.IsNotExist(statErr) {
		t.Fatalf("mutated candidate must not be published, stat error = %v", statErr)
	}
}

func TestValidateAndStageRejectsSymlinkInProducerInputs(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	if err := os.Symlink(filepath.Join(cfg.ProducerInputDir, "source.yaml"), filepath.Join(cfg.ProducerInputDir, "alias.yaml")); err != nil {
		t.Fatal(err)
	}

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "producer input contains symlink") {
		t.Fatalf("expected producer symlink error, got %v", err)
	}
}

func newTestCandidate(t *testing.T, benchmarkModel string) Config {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "data")
	inputDir := filepath.Join(root, "input")
	benchmarkDir := filepath.Join(root, "benchmarks")
	qualificationReportPath := filepath.Join(root, "benchmark-qualification-report.json")
	runtimeList := filepath.Join(root, "runtime-files.txt")
	outputDir := filepath.Join(root, "out")
	loaderDir := filepath.Join(root, "loaders")
	supportedVersions := filepath.Join(root, "supported-versions.txt")

	writeFile(t, filepath.Join(catalogDir, "validated-models-catalog.yaml"), "source: test\nmodels:\n  - name: example/model\n    artifacts:\n      - uri: oci://example.invalid/model:1\n", 0o644)
	writeFile(t, filepath.Join(catalogDir, "other-models-catalog.yaml"), "source: test\nmodels:\n  - name: example/other\n    artifacts:\n      - uri: oci://example.invalid/other:1\n", 0o644)
	writeFile(t, filepath.Join(catalogDir, "models-index.yaml"), "models:\n  - uri: example.invalid/model:1\n", 0o644)
	writeFile(t, filepath.Join(inputDir, "source.yaml"), "name: source\n", 0o644)
	writeFile(t, runtimeList, "validated-models-catalog.yaml\nother-models-catalog.yaml\n", 0o644)

	writeFile(t, filepath.Join(benchmarkDir, "manifest.json"), `{
  "version": "test",
  "files": {
    "example/provider.json": "provider_metadata",
    "example/model/metadata.json": "model_metadata",
    "example/model/evaluations.ndjson": "model_evaluations"
  }
}`, 0o644)
	writeFile(t, filepath.Join(benchmarkDir, "example", "provider.json"), `{"id":"example"}`, 0o644)
	writeFile(t, filepath.Join(benchmarkDir, "example", "model", "metadata.json"), `{"id":"`+benchmarkModel+`","provider_id":"example"}`, 0o644)
	writeFile(t, filepath.Join(benchmarkDir, "example", "model", "evaluations.ndjson"), `{"id":"evaluation","model_id":"example/model"}`+"\n", 0o644)
	writeFile(t, supportedVersions, "fixture-v1\n", 0o644)
	loader := `#!/bin/sh
set -eu
test -r "$CATALOG_DATA_DIR/validated-models-catalog.yaml"
test -r "$BENCHMARK_DATA_DIR/manifest.json"
test -d "$RELEASE_CANDIDATE_ROOT"
printf 'MODEL_CATALOG_LOADER_CHECK_OK=%s\n' "$MODEL_CATALOG_VERSION"
`
	writeFile(t, filepath.Join(loaderDir, "fixture-v1"), loader, 0o755)

	cfg := Config{
		CatalogDir:              catalogDir,
		ProducerInputDir:        inputDir,
		BenchmarkDir:            benchmarkDir,
		QualificationReport:     qualificationReportPath,
		ExpectedCatalogRevision: testCatalogRevision,
		RuntimeFileList:         runtimeList,
		OutputDir:               outputDir,
		LoaderCheckDir:          loaderDir,
		SupportedVersionList:    supportedVersions,
	}
	writeQualificationReport(t, cfg, benchmarkModel)
	return cfg
}

func writeQualificationReport(t *testing.T, cfg Config, benchmarkModel string) {
	t.Helper()
	payload := map[string][]byte{}
	err := filepath.WalkDir(cfg.BenchmarkDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(cfg.BenchmarkDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		payload[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	canonicalID := ""
	for _, name := range []string{"other-models-catalog.yaml", "validated-models-catalog.yaml"} {
		data, err := os.ReadFile(filepath.Join(cfg.CatalogDir, name))
		if err != nil {
			t.Fatal(err)
		}
		var document catalogDocument
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		for _, model := range document.Models {
			if strings.EqualFold(model.Name, benchmarkModel) {
				canonicalID = model.Name
			}
		}
	}
	caseOnly := []qualificationCaseOnlyMatch{}
	if canonicalID != "" && canonicalID != benchmarkModel {
		caseOnly = append(caseOnly, qualificationCaseOnlyMatch{BenchmarkID: benchmarkModel, CatalogID: canonicalID})
	}

	catalogFiles := make([]qualificationCatalogFile, 0, 2)
	for _, name := range []string{"other-models-catalog.yaml", "validated-models-catalog.yaml"} {
		data, err := os.ReadFile(filepath.Join(cfg.CatalogDir, name))
		if err != nil {
			t.Fatal(err)
		}
		catalogFiles = append(catalogFiles, qualificationCatalogFile{Name: name, SHA256: sha256Hex(data)})
	}

	report := qualificationReport{
		Version:           qualificationReportVersion,
		MatchPolicy:       qualificationMatchPolicy,
		BenchmarkRevision: testBenchmarkRevision,
		CatalogRevision:   testCatalogRevision,
		Source: qualificationSource{
			Revision:       testBenchmarkRevision,
			GeneratedAt:    "fixture-only",
			ManifestSHA256: strings.Repeat("d", 64),
			TreeSHA256:     strings.Repeat("f", 64),
		},
		Catalog: qualificationCatalog{Revision: testCatalogRevision, Files: catalogFiles},
		Exclusions: qualificationExclusions{
			Name:            "fixture-excluded-models.yaml",
			Version:         qualificationExclusionsVersion,
			MatchPolicy:     qualificationMatchPolicy,
			PolicyReference: qualificationPolicyReference,
			ReviewStatus:    "approved",
			ReviewReference: "fixture-only",
			SHA256:          strings.Repeat("e", 64),
		},
		OutputManifestSHA256: sha256Hex(payload["manifest.json"]),
		PayloadSHA256:        payloadTreeDigest(payload),
		Models: qualificationModelCounts{
			Source:          1,
			Included:        1,
			Excluded:        0,
			CaseOnlyMatches: len(caseOnly),
		},
		IncludedModelIDs:     []string{benchmarkModel},
		ExcludedModelIDs:     []string{},
		CaseOnlyModelMatches: caseOnly,
		Payload: qualificationPayloadCounts{
			Files:         len(payload),
			Models:        1,
			Providers:     1,
			VariantGroups: 0,
		},
	}
	writeJSON(t, cfg.QualificationReport, report)
}

func readQualificationReport(t *testing.T, path string) qualificationReport {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report qualificationReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
