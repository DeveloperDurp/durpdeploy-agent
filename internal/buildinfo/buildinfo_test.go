package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	build := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "source"},
		{Key: "vcs.modified", Value: "true"},
	}}
	for _, scenario := range []struct {
		name, version, commit, number, modified string
		build                                   *debug.BuildInfo
		want                                    Info
		output                                  string
	}{
		{"unstamped", "dev", "unknown", "unknown", "", build,
			Info{"dev", "source", "unknown", true}, "dev (build unknown, commit source, modified)"},
		{"archive", "dev", "unknown", "unknown", "", nil,
			Info{"dev", "unknown", "unknown", false}, "dev (build unknown, commit unknown)"},
		{"tagged", "v1.2.3+build.4", "release", "742", "false", build,
			Info{"v1.2.3+build.4", "release", "742", false}, "v1.2.3+build.4 (build 742, commit release)"},
		{"development", "v0.0.0-dev.743+gabc.dirty", "abc", "743", "true", nil,
			Info{"v0.0.0-dev.743+gabc.dirty", "abc", "743", true},
			"v0.0.0-dev.743+gabc.dirty (build 743, commit abc, modified)"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := resolve(scenario.version, scenario.commit,
				scenario.number, scenario.modified, scenario.build)
			if got != scenario.want || got.String() != scenario.output {
				t.Fatalf("build = %+v (%q), want %+v (%q)",
					got, got.String(), scenario.want, scenario.output)
			}
		})
	}
}
