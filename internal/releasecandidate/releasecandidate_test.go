package releasecandidate

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
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

	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "app", "data", "validated-models-index.yaml")); !os.IsNotExist(err) {
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
	refreshQualificationReport(t, cfg)
	if _, err := ValidateAndStage(cfg); err != nil {
		t.Fatalf("capitalization-only ID difference should be compatible: %v", err)
	}
}

func TestValidateAndStageRejectsQualificationReportPayloadMismatch(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	report, err := os.ReadFile(cfg.BenchmarkQualificationReport)
	if err != nil {
		t.Fatal(err)
	}
	report = []byte(strings.Replace(string(report), `"payload_sha256": "`, `"payload_sha256": "ffffffff`, 1))
	writeFile(t, cfg.BenchmarkQualificationReport, string(report), 0o644)

	_, err = ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), "payload_sha256 does not match") {
		t.Fatalf("expected qualification payload digest error, got %v", err)
	}
}

func TestValidateAndStageRejectsUnapprovedQualificationReport(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	report, err := os.ReadFile(cfg.BenchmarkQualificationReport)
	if err != nil {
		t.Fatal(err)
	}
	report = []byte(strings.Replace(string(report), `"review_status": "approved"`, `"review_status": "pending"`, 1))
	writeFile(t, cfg.BenchmarkQualificationReport, string(report), 0o644)

	_, err = ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `exclusion review_status "pending" must be "approved"`) {
		t.Fatalf("expected unapproved qualification report error, got %v", err)
	}
}

func TestValidateAndStageRejectsAgentIndexCatalogDrift(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, cfg.RuntimeFileList, "validated-models-catalog.yaml\nother-models-catalog.yaml\nredhat-agents-catalog.yaml\n", 0o644)
	writeFile(t, filepath.Join(cfg.CatalogDir, "redhat-agents-catalog.yaml"), `source: test
agents:
  - name: first-agent
    repositoryUrl: https://github.com/example/agents/tree/main/agents/first
  - name: stale-agent
    repositoryUrl: https://github.com/example/agents/tree/main/agents/stale
`, 0o644)
	writeFile(t, filepath.Join(cfg.CatalogDir, "redhat-agents-index.yaml"), `source: test
repository: example/agents
branch: main
agents:
  - path: agents/first
`, 0o644)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `generated agent repositoryUrl "https://github.com/example/agents/tree/main/agents/stale"`) {
		t.Fatalf("expected agent index/catalog drift error, got %v", err)
	}
}

func TestValidateAndStageRejectsStaleModelCatalogArtifact(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, filepath.Join(cfg.CatalogDir, "validated-models-catalog.yaml"), `source: test
models:
  - name: example/model
    artifacts:
      - uri: oci://example.invalid/model:1
      - uri: oci://example.invalid/stale:1
`, 0o644)
	refreshQualificationReport(t, cfg)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `artifact "example.invalid/stale:1" has no provenance`) {
		t.Fatalf("expected stale catalog artifact error, got %v", err)
	}
}

func TestValidateAndStageRejectsModelArtifactInWrongCatalog(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	writeFile(t, filepath.Join(cfg.CatalogDir, "validated-models-catalog.yaml"), "source: test\nmodels:\n  - name: example/model\n    artifacts:\n      - uri: oci://example.invalid/other:1\n", 0o644)
	writeFile(t, filepath.Join(cfg.CatalogDir, "other-models-catalog.yaml"), "source: test\nmodels:\n  - name: example/other\n    artifacts:\n      - uri: oci://example.invalid/model:1\n", 0o644)
	refreshQualificationReport(t, cfg)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `artifact "example.invalid/model:1" is missing from corresponding generated model catalog`) {
		t.Fatalf("expected per-index catalog placement error, got %v", err)
	}
}

func TestValidateAndStageAcceptsSupplementalModelProvenance(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	addSupplementalModelPair(t, cfg, true)

	if _, err := ValidateAndStage(cfg); err != nil {
		t.Fatalf("supplemental catalog provenance should validate: %v", err)
	}
}

func TestValidateAndStageRejectsStaleSupplementalArtifact(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	addSupplementalModelPair(t, cfg, true)
	if err := os.Remove(filepath.Join(cfg.ProducerInputDir, "supplemental-catalog.yaml")); err != nil {
		t.Fatal(err)
	}

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `artifact "example.invalid/supplemental:1" has no provenance`) {
		t.Fatalf("expected stale supplemental artifact error, got %v", err)
	}
}

func TestValidateAndStageRejectsSupplementalArtifactMissingFromOutput(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	addSupplementalModelPair(t, cfg, false)

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `supplemental catalog artifact "example.invalid/supplemental:1" is missing from generated model catalog`) {
		t.Fatalf("expected missing supplemental artifact error, got %v", err)
	}
}

func TestValidateAndStageRejectsSupplementalModelIdentityDrift(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	addSupplementalModelPair(t, cfg, true)
	path := filepath.Join(cfg.CatalogDir, "models-catalog.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "name: example/supplemental", "name: example/stale", 1))
	writeFile(t, path, string(data), 0o644)

	_, err = ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `belongs to model "example/supplemental"`) || !strings.Contains(err.Error(), `assigns it to "example/stale"`) {
		t.Fatalf("expected supplemental model identity drift error, got %v", err)
	}
}

func TestValidateAndStageAcceptsMatchingMCPServerInputName(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	addMCPServerPair(t, cfg, "com.example/test-server", "com.example/test-server")

	if _, err := ValidateAndStage(cfg); err != nil {
		t.Fatalf("matching MCP server producer name should validate: %v", err)
	}
}

func TestValidateAndStageRejectsMCPServerInputNameMismatch(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	addMCPServerPair(t, cfg, "com.example/indexed-server", "com.example/different-server")

	_, err := ValidateAndStage(cfg)
	if err == nil || !strings.Contains(err.Error(), `declares name "com.example/different-server", expected index name "com.example/indexed-server"`) {
		t.Fatalf("expected MCP server producer name mismatch, got %v", err)
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

func TestValidateAndStageLoaderDescendantCannotHoldOutputPipesOpen(t *testing.T) {
	cfg := newTestCandidate(t, "example/model")
	previousWaitDelay := loaderCheckWaitDelay
	loaderCheckWaitDelay = 50 * time.Millisecond
	t.Cleanup(func() { loaderCheckWaitDelay = previousWaitDelay })
	check := `#!/bin/sh
sleep 30 &
child=$!
printf '%s\n' "$child" > "$(dirname "$0")/child.pid"
printf 'MODEL_CATALOG_LOADER_CHECK_OK=%s\n' "$MODEL_CATALOG_VERSION"
`
	writeFile(t, filepath.Join(cfg.LoaderCheckDir, "fixture-v1"), check, 0o755)

	started := time.Now()
	_, err := ValidateAndStage(cfg)
	elapsed := time.Since(started)
	killLoaderChild(t, filepath.Join(cfg.LoaderCheckDir, "child.pid"))
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected loader output pipe wait-delay error, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("loader validation waited too long for descendant output pipes: %s", elapsed)
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
	qualificationReport := filepath.Join(root, "benchmark-qualification-report.json")
	runtimeList := filepath.Join(root, "runtime-files.txt")
	outputDir := filepath.Join(root, "out")
	loaderDir := filepath.Join(root, "loaders")
	supportedVersions := filepath.Join(root, "supported-versions.txt")

	writeFile(t, filepath.Join(catalogDir, "validated-models-catalog.yaml"), "source: test\nmodels:\n  - name: example/model\n    artifacts:\n      - uri: oci://example.invalid/model:1\n", 0o644)
	writeFile(t, filepath.Join(catalogDir, "other-models-catalog.yaml"), "source: test\nmodels:\n  - name: example/other\n    artifacts:\n      - uri: oci://example.invalid/other:1\n", 0o644)
	writeFile(t, filepath.Join(catalogDir, "validated-models-index.yaml"), "models:\n  - uri: example.invalid/model:1\n", 0o644)
	writeFile(t, filepath.Join(catalogDir, "other-models-index.yaml"), "models:\n  - uri: example.invalid/other:1\n", 0o644)
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
		CatalogDir:                   catalogDir,
		ProducerInputDir:             inputDir,
		BenchmarkDir:                 benchmarkDir,
		BenchmarkQualificationReport: qualificationReport,
		RuntimeFileList:              runtimeList,
		OutputDir:                    outputDir,
		LoaderCheckDir:               loaderDir,
		SupportedVersionList:         supportedVersions,
	}
	refreshQualificationReport(t, cfg)
	return cfg
}

func addSupplementalModelPair(t *testing.T, cfg Config, includeSupplementalOutput bool) {
	t.Helper()
	runtimeFiles, err := os.ReadFile(cfg.RuntimeFileList)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfg.RuntimeFileList, string(runtimeFiles)+"models-catalog.yaml\n", 0o644)
	writeFile(t, filepath.Join(cfg.CatalogDir, "models-index.yaml"), `models:
  - uri: example.invalid/indexed:1
`, 0o644)
	catalog := `source: test
models:
  - name: example/indexed
    artifacts:
      - uri: oci://example.invalid/indexed:1
`
	if includeSupplementalOutput {
		catalog += `  - name: example/supplemental
    artifacts:
      - uri: oci://example.invalid/supplemental:1
`
	}
	writeFile(t, filepath.Join(cfg.CatalogDir, "models-catalog.yaml"), catalog, 0o644)
	writeFile(t, filepath.Join(cfg.ProducerInputDir, "supplemental-catalog.yaml"), `source: test
models:
  - name: example/supplemental
    artifacts:
      - uri: oci://example.invalid/supplemental:1
`, 0o644)
}

func addMCPServerPair(t *testing.T, cfg Config, indexName, inputName string) {
	t.Helper()
	runtimeFiles, err := os.ReadFile(cfg.RuntimeFileList)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfg.RuntimeFileList, string(runtimeFiles)+"test-mcp-servers-catalog.yaml\n", 0o644)
	writeFile(t, filepath.Join(cfg.CatalogDir, "test-mcp-servers-catalog.yaml"), "source: test\nmcp_servers:\n  - name: "+indexName+"\n", 0o644)
	writeFile(t, filepath.Join(cfg.CatalogDir, "test-mcp-servers-index.yaml"), "mcp_servers:\n  - name: "+indexName+"\n    input_path: input/mcp_servers/test.yaml\n", 0o644)
	writeFile(t, filepath.Join(cfg.ProducerInputDir, "mcp_servers", "test.yaml"), "name: "+inputName+"\n", 0o644)
}

func killLoaderChild(t *testing.T, pidPath string) {
	t.Helper()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read loader descendant pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse loader descendant pid: %v", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find loader descendant: %v", err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("kill loader descendant: %v", err)
	}
}

func refreshQualificationReport(t *testing.T, cfg Config) {
	t.Helper()
	payload := map[string][]byte{}
	var modelIDs []string
	err := filepath.WalkDir(cfg.BenchmarkDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(cfg.BenchmarkDir, path)
		if err != nil {
			return err
		}
		payload[filepath.ToSlash(rel)] = data
		if filepath.Base(path) == "metadata.json" {
			var metadata modelMetadata
			if err := json.Unmarshal(data, &metadata); err != nil {
				return err
			}
			modelIDs = append(modelIDs, metadata.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(modelIDs)
	catalogFiles := make([]map[string]string, 0, 2)
	for _, name := range []string{"other-models-catalog.yaml", "validated-models-catalog.yaml"} {
		data, err := os.ReadFile(filepath.Join(cfg.CatalogDir, name))
		if err != nil {
			t.Fatal(err)
		}
		catalogFiles = append(catalogFiles, map[string]string{"name": name, "sha256": digestBytes(data)})
	}
	report := map[string]any{
		"version":            qualificationReportVersion,
		"match_policy":       benchmarkMatchPolicy,
		"benchmark_revision": strings.Repeat("1", 40),
		"catalog_revision":   strings.Repeat("2", 40),
		"source": map[string]any{
			"revision": strings.Repeat("1", 40),
		},
		"catalog": map[string]any{
			"revision": strings.Repeat("2", 40),
			"files":    catalogFiles,
		},
		"exclusions": map[string]any{
			"review_status":    "approved",
			"review_reference": "test-approval",
			"sha256":           strings.Repeat("3", 64),
		},
		"output_manifest_sha256": digestBytes(payload["manifest.json"]),
		"payload_sha256":         digestPayload(payload),
		"models": map[string]any{
			"included": len(modelIDs),
		},
		"included_model_ids": modelIDs,
		"payload": map[string]any{
			"files":  len(payload),
			"models": len(modelIDs),
		},
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfg.BenchmarkQualificationReport, string(append(data, '\n')), 0o644)
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
