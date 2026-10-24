package resourcepolicy

import (
	"math"
	"testing"
)

func TestArtifactPolicyValidation(t *testing.T) {
	for name, change := range map[string]func(*Artifact){
		"download zero":           func(p *Artifact) { p.DownloadBytes = 0 },
		"extracted negative":      func(p *Artifact) { p.ExtractedBytes = -1 },
		"files overflow":          func(p *Artifact) { p.Files = math.MaxInt64 },
		"nodes insufficient":      func(p *Artifact) { p.StagingNodes = p.Files - 1 },
		"metadata overflow":       func(p *Artifact) { p.MetadataBytes = math.MaxInt64 },
		"fetch duration overflow": func(p *Artifact) { p.FetchTimeoutSeconds = math.MaxInt64 },
	} {
		t.Run(name, func(t *testing.T) {
			policy := Defaults().Artifact
			change(&policy)
			if err := policy.Validate(); err == nil {
				t.Fatal("invalid artifact policy accepted")
			}
		})
	}
	if err := Defaults().Artifact.Validate(); err != nil {
		t.Fatal(err)
	}
}
