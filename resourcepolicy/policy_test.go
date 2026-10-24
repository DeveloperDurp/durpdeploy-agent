package resourcepolicy_test

import (
	"math"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestDefaultPolicy(t *testing.T) {
	l := resourcepolicy.Defaults()
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	if l.Artifact.DownloadBytes != artifact.MaxDownload ||
		l.Artifact.ExtractedBytes != artifact.MaxExtracted ||
		l.Artifact.Files != artifact.MaxFiles ||
		l.Artifact.StagingNodes != artifact.MaxStagingNodes ||
		l.Artifact.MetadataBytes != artifact.MaxMetadata {
		t.Fatal("policy changes existing artifact defaults")
	}
	l.Execution.MaxRetries = 0
	l.Execution.MaxTimeoutSeconds = min(
		math.MaxInt64/int64(time.Second), int64(^uint(0)>>1)-1,
	)
	l.Container.HelperCPUs = 1e-9
	if err := l.Validate(); err != nil {
		t.Fatalf("valid boundary policy: %v", err)
	}
}

func TestInvalidPolicy(t *testing.T) {
	cases := map[string]func(*resourcepolicy.Limits){
		"negative retries": func(l *resourcepolicy.Limits) { l.Execution.MaxRetries = -1 },
		"zero timeout":     func(l *resourcepolicy.Limits) { l.Execution.MaxTimeoutSeconds = 0 },
		"timeout ordering": func(l *resourcepolicy.Limits) { l.Execution.MaxTimeoutSeconds = 1 },
		"duration overflow": func(l *resourcepolicy.Limits) {
			l.Execution.MaxTimeoutSeconds = math.MaxInt64/int64(time.Second) + 1
		},
		"zero PIDs":                func(l *resourcepolicy.Limits) { l.Container.PIDs = 0 },
		"negative helper PIDs":     func(l *resourcepolicy.Limits) { l.Container.HelperPIDs = -1 },
		"zero files":               func(l *resourcepolicy.Limits) { l.Artifact.Files = 0 },
		"node coverage":            func(l *resourcepolicy.Limits) { l.Artifact.StagingNodes = 1 },
		"TAR framing":              func(l *resourcepolicy.Limits) { l.Staging.ArchiveBytes = l.Staging.PayloadBytes },
		"short TAR":                func(l *resourcepolicy.Limits) { l.Staging.ArchiveBytes = 1023 },
		"integer sentinel":         func(l *resourcepolicy.Limits) { l.Container.PIDs = int64(^uint(0) >> 1) },
		"NaN CPUs":                 func(l *resourcepolicy.Limits) { l.Container.HelperCPUs = math.NaN() },
		"infinite CPUs":            func(l *resourcepolicy.Limits) { l.Container.HelperCPUs = math.Inf(1) },
		"small CPUs":               func(l *resourcepolicy.Limits) { l.Container.HelperCPUs = 1e-10 },
		"CPU nanoseconds overflow": func(l *resourcepolicy.Limits) { l.Container.HelperCPUs = math.MaxFloat64 },
		"artifact tmpfs overflow": func(l *resourcepolicy.Limits) {
			l.Artifact.DownloadBytes = int64(^uint(0)>>1) - 1
		},
		"staging capacity overflow": func(l *resourcepolicy.Limits) {
			l.Staging.Entries = int64(^uint(0)>>1) - 1
		},
		"artifact capacity overflow": func(l *resourcepolicy.Limits) {
			l.Artifact.StagingNodes = int64(^uint(0)>>1) - 1
		},
		"memory overhead overflow": func(l *resourcepolicy.Limits) {
			l.Container.HelperMemoryOverheadBytes = int64(^uint(0)>>1) - 1
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			limits := resourcepolicy.Defaults()
			change(&limits)
			if err := limits.Validate(); err == nil {
				t.Fatal("accepted invalid policy")
			}
		})
	}
}
