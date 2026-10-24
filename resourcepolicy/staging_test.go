package resourcepolicy

import "testing"

func TestStagingPolicyValidation(t *testing.T) {
	for _, policy := range []Staging{
		{PayloadBytes: 1, Entries: 1, ArchiveBytes: 1025},
		Defaults().Staging,
		{PayloadBytes: 1024, Entries: 20001, ArchiveBytes: 4096},
	} {
		if err := policy.Validate(); err != nil {
			t.Fatalf("valid staging policy: %v", err)
		}
	}
	for _, policy := range []Staging{
		{},
		{PayloadBytes: 0, Entries: 1, ArchiveBytes: 1024},
		{PayloadBytes: 1, Entries: 0, ArchiveBytes: 1025},
		{PayloadBytes: 1, Entries: 1, ArchiveBytes: 1023},
		{PayloadBytes: 2, Entries: 1, ArchiveBytes: 1025},
		{PayloadBytes: 1, Entries: int64(^uint(0) >> 1), ArchiveBytes: 1025},
	} {
		if err := policy.Validate(); err == nil {
			t.Fatalf("invalid staging policy accepted: %+v", policy)
		}
	}
}
