package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func artifactPolicyFixture(t *testing.T, names ...string) string {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(name, "/") {
			if _, err := io.WriteString(entry, "data"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "policy.zip")
	if err := os.WriteFile(filename, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestArtifactPolicyExactBudgetsAndRejectionCleanup(t *testing.T) {
	filename := artifactPolicyFixture(t, "a/one", "b/two")
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	policy := resourcepolicy.Defaults().Artifact
	policy.DownloadBytes, policy.ExtractedBytes = info.Size(), 8
	policy.Files, policy.StagingNodes = 2, 4
	if err := ValidateZIPWithPolicy(t.Context(), filename, policy); err != nil {
		t.Fatal(err)
	}
	for name, lower := range map[string]func(*resourcepolicy.Artifact){
		"download":  func(p *resourcepolicy.Artifact) { p.DownloadBytes-- },
		"extracted": func(p *resourcepolicy.Artifact) { p.ExtractedBytes-- },
		"files":     func(p *resourcepolicy.Artifact) { p.Files-- },
		"nodes":     func(p *resourcepolicy.Artifact) { p.StagingNodes-- },
	} {
		t.Run(name, func(t *testing.T) {
			limited := policy
			lower(&limited)
			assertArtifactPolicyRejection(t, filename, limited, ErrInvalid)
		})
	}
	directory, err := ExtractZIPWithPolicy(t.Context(), filename, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	data, err := os.ReadFile(filepath.Join(directory, "a/one"))
	if err != nil || string(data) != "data" {
		t.Fatalf("extraction: %q %v", data, err)
	}
}

func assertArtifactPolicyRejection(t *testing.T, filename string, policy resourcepolicy.Artifact, want error) {
	t.Helper()
	if err := ValidateZIPWithPolicy(t.Context(), filename, policy); !errors.Is(err, want) {
		t.Fatalf("validation: got %v, want %v", err, want)
	}
	if directory, err := ExtractZIPWithPolicy(t.Context(), filename, policy); directory != "" || !errors.Is(err, want) {
		t.Fatalf("extraction: directory=%q error=%v", directory, err)
	}
	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(filename) {
		t.Fatalf("partial extraction retained: %v %v", entries, err)
	}
}

func TestArtifactPolicyMetadataExactBudget(t *testing.T) {
	filename := artifactPolicyFixture(t, "one", "two")
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	reader := &metadataReader{ctx: t.Context(), reader: file, remaining: MaxMetadata}
	if _, err := zip.NewReader(reader, info.Size()); err != nil {
		t.Fatal(err)
	}
	policy := resourcepolicy.Defaults().Artifact
	policy.MetadataBytes = MaxMetadata - reader.remaining
	if err := ValidateZIPWithPolicy(t.Context(), filename, policy); err != nil {
		t.Fatal(err)
	}
	policy.MetadataBytes--
	assertArtifactPolicyRejection(t, filename, policy, ErrMetadataLimit)
}

func TestArtifactPolicyRaisedDownloadCeiling(t *testing.T) {
	filename := artifactPolicyFixture(t, "one")
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(MaxDownload+1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateZIP(t.Context(), filename); !errors.Is(err, ErrInvalid) {
		t.Fatalf("default ceiling changed: %v", err)
	}
	policy := resourcepolicy.Defaults().Artifact
	policy.DownloadBytes = MaxDownload + 1 + int64(len(data))
	if err := ValidateZIPWithPolicy(t.Context(), filename, policy); err != nil {
		t.Fatal(err)
	}
	policy.DownloadBytes--
	assertArtifactPolicyRejection(t, filename, policy, ErrInvalid)
}

func TestArtifactPolicyRaisedFileAndNodeCeilings(t *testing.T) {
	for _, count := range []int{MaxFiles, MaxFiles + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			names := make([]string, count)
			for index := range names {
				names[index] = fmt.Sprintf("%d/file", index)
			}
			filename := artifactPolicyFixture(t, names...)
			if err := ValidateZIP(t.Context(), filename); !errors.Is(err, ErrInvalid) {
				t.Fatalf("default ceiling changed: %v", err)
			}
			policy := resourcepolicy.Defaults().Artifact
			policy.Files, policy.StagingNodes = int64(count), 2*int64(count)
			if err := ValidateZIPWithPolicy(t.Context(), filename, policy); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestArtifactPolicyPreservesStrictPathsDuplicatesAndCancellation(t *testing.T) {
	for _, names := range [][]string{{"a/", "a/"}, {"a/file", "a/", "a/"}, {"a", "a/file"}, {"../escape"}} {
		filename := artifactPolicyFixture(t, names...)
		assertArtifactPolicyRejection(t, filename, resourcepolicy.Defaults().Artifact, ErrInvalid)
	}
	filename := artifactPolicyFixture(t, "valid")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ValidateZIPWithPolicy(ctx, filename, resourcepolicy.Defaults().Artifact); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestArtifactPolicyRejectsBeforeFilesystemEffects(t *testing.T) {
	policy := resourcepolicy.Defaults().Artifact
	policy.StagingNodes = policy.Files - 1
	filename := filepath.Join(t.TempDir(), "missing.zip")
	if err := ValidateZIPWithPolicy(t.Context(), filename, policy); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("policy validation did not precede file access: %v", err)
	}
	if directory, err := ExtractZIPWithPolicy(t.Context(), filename, policy); err == nil || directory != "" || errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid policy created extraction: %q %v", directory, err)
	}
	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid policy created files: %v %v", entries, err)
	}
}

func TestArtifactPolicyPinBoundaries(t *testing.T) {
	policy := resourcepolicy.Defaults().Artifact
	policy.DownloadBytes = MaxDownload + 1
	pin := Pin{SHA256: strings.Repeat("ab", 32), Size: policy.DownloadBytes}
	if err := pin.ValidateWithPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if err := pin.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("default pin ceiling changed: %v", err)
	}
	pin.Size++
	if err := pin.ValidateWithPolicy(policy); !errors.Is(err, ErrInvalid) {
		t.Fatalf("excessive pin accepted: %v", err)
	}
	policy.DownloadBytes = 0
	if err := pin.ValidateWithPolicy(policy); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid pin policy accepted: %v", err)
	}
}

func TestArtifactPolicyIndependentConcurrentCalls(t *testing.T) {
	filename := artifactPolicyFixture(t, "one", "two")
	for _, budget := range []int64{7, 8} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			t.Parallel()
			policy := resourcepolicy.Defaults().Artifact
			policy.ExtractedBytes = budget
			err := ValidateZIPWithPolicy(t.Context(), filename, policy)
			if (err == nil) != (budget == 8) {
				t.Fatalf("independent budget %d: %v", budget, err)
			}
		})
	}
}

func TestArtifactPolicyMetadataBudgetExcludesPayload(t *testing.T) {
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: "payload", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(entry, zeroReader{}, 100000); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "stored.zip")
	if err := os.WriteFile(filename, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	policy := resourcepolicy.Defaults().Artifact
	policy.MetadataBytes = 8192
	if err := ValidateZIPWithPolicy(t.Context(), filename, policy); err != nil {
		t.Fatalf("metadata budget charged payload reads: %v", err)
	}
}
