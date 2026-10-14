package main

import (
	"os"
	"strings"
	"testing"

	"github.com/opendatahub-io/model-metadata-collection/internal/releasecandidate"
)

func TestRejectCommittedFixturesInProductMode(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	cfg := releasecandidate.Config{
		BenchmarkDir:                 "internal/releasecandidate/testdata/benchmarks",
		BenchmarkQualificationReport: "internal/releasecandidate/testdata/benchmark-qualification-report.json",
		SupportedVersionList:         "internal/releasecandidate/testdata/supported-model-catalog-versions.txt",
		LoaderCheckDir:               "internal/releasecandidate/testdata/loader-checks",
	}
	err = rejectCommittedFixtures(cfg)
	if err == nil || !strings.Contains(err.Error(), "product validation refuses committed fixture") {
		t.Fatalf("expected committed fixture rejection, got %v", err)
	}
}
