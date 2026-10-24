package staging

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func stagingPolicyFixture(t *testing.T) (string, []byte, resourcepolicy.Staging) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested", "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "data"), []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := Pack(t.Context(), root, &buffer); err != nil {
		t.Fatal(err)
	}
	return root, buffer.Bytes(), resourcepolicy.Staging{PayloadBytes: 8, Entries: 3, ArchiveBytes: int64(buffer.Len())}
}

func TestStagingPolicyBoundaries(t *testing.T) {
	root, archive, policy := stagingPolicyFixture(t)
	for _, scenario := range []string{"exact", "payload", "entries", "archive"} {
		t.Run(scenario, func(t *testing.T) {
			checkStagingPolicyBoundary(t, root, archive, policy, scenario)
		})
	}
	assertStagingPolicyRoundTrip(t, archive, policy)
}

func checkStagingPolicyBoundary(t *testing.T, root string, archive []byte, limits resourcepolicy.Staging, scenario string) {
	t.Helper()
	switch scenario {
	case "payload":
		limits.PayloadBytes--
	case "entries":
		limits.Entries--
	case "archive":
		limits.ArchiveBytes--
	}
	var packed bytes.Buffer
	packErr := PackWithPolicy(t.Context(), root, &packed, limits)
	unpackErr := UnpackWithPolicy(t.Context(), bytes.NewReader(archive), "", limits)
	if scenario == "exact" {
		if packErr != nil || unpackErr != nil || !bytes.Equal(packed.Bytes(), archive) {
			t.Fatalf("exact budget changed archive: %v %v", packErr, unpackErr)
		}
	} else if !errors.Is(packErr, ErrInvalid) || !errors.Is(unpackErr, ErrInvalid) {
		t.Fatalf("excessive %s accepted: pack=%v unpack=%v", scenario, packErr, unpackErr)
	}
	if int64(packed.Len()) > limits.ArchiveBytes {
		t.Fatal("packing wrote beyond the archive limit")
	}
}

func assertStagingPolicyRoundTrip(t *testing.T, archive []byte, policy resourcepolicy.Staging) {
	t.Helper()
	output := t.TempDir()
	if err := UnpackWithPolicy(t.Context(), bytes.NewReader(archive), output, policy); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "nested", "data"))
	if err != nil || string(data) != "contents" {
		t.Fatalf("restored bytes: %q %v", data, err)
	}
	info, err := os.Stat(filepath.Join(output, "nested", "data"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("restored private mode: %v %v", info, err)
	}
}

func TestStagingPolicyCountsTrailingPadding(t *testing.T) {
	_, archive, policy := stagingPolicyFixture(t)
	policy.ArchiveBytes += 1024
	exact := append(bytes.Clone(archive), make([]byte, 1024)...)
	if err := UnpackWithPolicy(t.Context(), bytes.NewReader(exact), "", policy); err != nil {
		t.Fatal(err)
	}
	if err := UnpackWithPolicy(t.Context(), bytes.NewReader(append(exact, 0)), "", policy); !errors.Is(err, ErrInvalid) {
		t.Fatalf("one padding byte exceeded archive limit: %v", err)
	}
}

type policyIOProbe struct{ calls int }

func (p *policyIOProbe) Read([]byte) (int, error)  { p.calls++; return 0, io.ErrUnexpectedEOF }
func (p *policyIOProbe) Write([]byte) (int, error) { p.calls++; return 0, io.ErrClosedPipe }

func TestStagingPolicyRejectsBeforeIO(t *testing.T) {
	policy := resourcepolicy.Defaults().Staging
	policy.Entries = 0
	probe := new(policyIOProbe)
	missing := filepath.Join(t.TempDir(), "missing")
	packErr := PackWithPolicy(t.Context(), missing, probe, policy)
	unpackErr := UnpackWithPolicy(t.Context(), probe, missing, policy)
	if packErr == nil || unpackErr == nil || errors.Is(packErr, os.ErrNotExist) || errors.Is(unpackErr, os.ErrNotExist) || probe.calls != 0 {
		t.Fatalf("invalid policy reached I/O: pack=%v unpack=%v calls=%d", packErr, unpackErr, probe.calls)
	}
}

func TestStagingPolicyCopiesAndConcurrentCalls(t *testing.T) {
	root, archive, policy := stagingPolicyFixture(t)
	var output bytes.Buffer
	writer := &policyMutationWriter{target: &output, mutate: func() {
		policy.PayloadBytes = 1
		policy.Entries = 1
		policy.ArchiveBytes = 1025
		t.Setenv("DURPDEPLOY_AGENT_LIMITS_STAGING_PAYLOAD_BYTES", "1")
	}}
	if err := PackWithPolicy(t.Context(), root, writer, policy); err != nil || !bytes.Equal(output.Bytes(), archive) {
		t.Fatalf("caller or environment mutation changed snapshot: %v", err)
	}
	results := make(chan error, 2)
	for _, payloadBytes := range []int64{8, 7} {
		limits := resourcepolicy.Staging{PayloadBytes: payloadBytes, Entries: 3, ArchiveBytes: int64(len(archive))}
		go func() { results <- UnpackWithPolicy(t.Context(), bytes.NewReader(archive), "", limits) }()
	}
	accepted, rejected := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrInvalid) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("policies leaked between calls: accepted=%d rejected=%d", accepted, rejected)
	}
}

type policyMutationWriter struct {
	target io.Writer
	mutate func()
}

func (w *policyMutationWriter) Write(data []byte) (int, error) {
	if w.mutate != nil {
		w.mutate()
		w.mutate = nil
	}
	return w.target.Write(data)
}

func TestStagingPolicyRaisedEntryLimit(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for index := range MaxFiles + 1 {
		if err := writer.WriteHeader(&tar.Header{Name: fmt.Sprint(index), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Unpack(t.Context(), bytes.NewReader(archive.Bytes()), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("default entry limit did not reject: %v", err)
	}
	policy := resourcepolicy.Defaults().Staging
	policy.Entries++
	if err := UnpackWithPolicy(t.Context(), bytes.NewReader(archive.Bytes()), "", policy); err != nil {
		t.Fatalf("raised entry limit did not apply: %v", err)
	}
}

func TestStagingPolicyCountsImplicitNodes(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "parent/deep/file", Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	policy := resourcepolicy.Staging{PayloadBytes: 1, Entries: 3, ArchiveBytes: int64(archive.Len())}
	if err := UnpackWithPolicy(t.Context(), bytes.NewReader(archive.Bytes()), "", policy); err != nil {
		t.Fatal(err)
	}
	policy.Entries--
	if err := UnpackWithPolicy(t.Context(), bytes.NewReader(archive.Bytes()), "", policy); !errors.Is(err, ErrInvalid) {
		t.Fatalf("implicit directories did not consume entry budget: %v", err)
	}
}

func TestStagingPolicyRaisedPayloadLimit(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "sparse")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(file.Truncate(MaxBytes+1), file.Close()); err != nil {
		t.Fatal(err)
	}
	if err := Pack(t.Context(), root, io.Discard); !errors.Is(err, ErrInvalid) {
		t.Fatalf("default payload budget did not reject: %v", err)
	}
	policy := resourcepolicy.Defaults().Staging
	policy.PayloadBytes++
	if err := PackWithPolicy(t.Context(), root, io.Discard, policy); err != nil {
		t.Fatalf("raised payload budget did not pack: %v", err)
	}
	var header bytes.Buffer
	writer := tar.NewWriter(&header)
	if err := writer.WriteHeader(&tar.Header{Name: "sparse", Typeflag: tar.TypeReg, Size: MaxBytes + 1}); err != nil {
		t.Fatal(err)
	}
	if err := UnpackWithPolicy(t.Context(), bytes.NewReader(header.Bytes()), "", policy); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("raised unpack budget did not reach payload validation: %v", err)
	}
}

func TestStagingPolicyDefaultsMatchConstants(t *testing.T) {
	policy := resourcepolicy.Defaults().Staging
	if policy.PayloadBytes != MaxBytes || policy.Entries != MaxFiles || policy.ArchiveBytes != MaxArchive {
		t.Fatal("default policy changed staging constants")
	}
}

func TestStagingPolicyCancellationAndWriterErrors(t *testing.T) {
	root, _, policy := stagingPolicyFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := PackWithPolicy(ctx, root, io.Discard, policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("packing lost cancellation: %v", err)
	}
	probe := new(policyIOProbe)
	if err := PackWithPolicy(t.Context(), root, probe, policy); !errors.Is(err, io.ErrClosedPipe) || probe.calls == 0 {
		t.Fatalf("packing lost write error: %v calls=%d", err, probe.calls)
	}
}
