package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opendatahub-io/model-metadata-collection/internal/releasecandidate"
)

const fixtureCatalogRevision = "cccccccccccccccccccccccccccccccccccccccc"

func main() {
	var cfg releasecandidate.Config
	var fixtureOnly bool
	flag.StringVar(&cfg.CatalogDir, "catalog-dir", "data", "directory containing generated catalogs and indexes")
	flag.StringVar(&cfg.ProducerInputDir, "producer-input-dir", "input", "directory containing producer-only catalog inputs")
	flag.StringVar(&cfg.BenchmarkDir, "benchmark-dir", "", "qualified benchmark payload directory (required)")
	flag.StringVar(&cfg.QualificationReport, "benchmark-report", "", "external benchmark qualification report (required)")
	flag.StringVar(&cfg.ExpectedCatalogRevision, "expected-catalog-revision", "", "full catalog commit used by benchmark qualification (required)")
	flag.StringVar(&cfg.RuntimeFileList, "runtime-files", "release/runtime-catalog-files.txt", "runtime catalog allowlist")
	flag.StringVar(&cfg.OutputDir, "output-dir", "build/release-candidate", "validated image build context")
	flag.StringVar(&cfg.LoaderCheckDir, "loader-check-dir", "", "directory containing one executable check per supported Model Catalog version")
	flag.StringVar(&cfg.SupportedVersionList, "supported-version-list", "release/supported-model-catalog-versions.txt", "reviewed supported Model Catalog version list")
	flag.BoolVar(&fixtureOnly, "fixture-only", false, "use only the committed non-product report, payload, version list, and loader")
	flag.Parse()

	if cfg.BenchmarkDir == "" {
		fmt.Fprintln(os.Stderr, "release candidate validation failed: --benchmark-dir is required")
		os.Exit(2)
	}
	if cfg.QualificationReport == "" {
		fmt.Fprintln(os.Stderr, "release candidate validation failed: --benchmark-report is required")
		os.Exit(2)
	}
	if cfg.ExpectedCatalogRevision == "" {
		fmt.Fprintln(os.Stderr, "release candidate validation failed: --expected-catalog-revision is required")
		os.Exit(2)
	}
	if cfg.LoaderCheckDir == "" {
		fmt.Fprintln(os.Stderr, "release candidate validation failed: --loader-check-dir is required")
		os.Exit(2)
	}
	if fixtureOnly {
		if err := configureFixtureOnly(&cfg); err != nil {
			fmt.Fprintf(os.Stderr, "release candidate validation failed: %v\n", err)
			os.Exit(2)
		}
	}

	report, err := releasecandidate.ValidateAndStage(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "release candidate validation failed:\n%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("release candidate validated: %d catalog files, %d benchmark files, %d compatible benchmark models\n", report.CatalogFiles, report.BenchmarkFiles, report.BenchmarkModels)
	fmt.Printf("staged image payload: %s/app/data and %s/app/benchmarks\n", cfg.OutputDir, cfg.OutputDir)
}

func configureFixtureOnly(cfg *releasecandidate.Config) error {
	paths := []struct {
		label    string
		actual   string
		expected string
	}{
		{"--benchmark-dir", cfg.BenchmarkDir, "internal/releasecandidate/testdata/benchmarks"},
		{"--benchmark-report", cfg.QualificationReport, "internal/releasecandidate/testdata/benchmark-qualification-report.json"},
		{"--supported-version-list", cfg.SupportedVersionList, "internal/releasecandidate/testdata/supported-model-catalog-versions.txt"},
		{"--loader-check-dir", cfg.LoaderCheckDir, "internal/releasecandidate/testdata/loader-checks"},
	}
	for _, item := range paths {
		expected, err := resolvedAbsolute(item.expected)
		if err != nil {
			return fmt.Errorf("resolve committed fixture %s: %w", item.expected, err)
		}
		actual, err := resolvedAbsolute(item.actual)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", item.label, err)
		}
		if actual != expected {
			return fmt.Errorf("--fixture-only requires %s to be the committed fixture at %s", item.label, expected)
		}
	}
	if !strings.Contains(filepath.Base(filepath.Clean(cfg.OutputDir)), "fixture") {
		return fmt.Errorf("--fixture-only output directory name must contain %q", "fixture")
	}
	if cfg.ExpectedCatalogRevision != fixtureCatalogRevision {
		return fmt.Errorf("--fixture-only requires synthetic expected catalog revision %s", fixtureCatalogRevision)
	}
	return nil
}

func resolvedAbsolute(value string) (string, error) {
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}
