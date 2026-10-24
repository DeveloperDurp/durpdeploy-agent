package resourcepolicy_test

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestPolicyAdvertisementV1WireVector(t *testing.T) {
	const expected = "resource-policy/1;sha256=9e698965e836b7cb3220f7899a6e99cd5e418d5c8340262eb462fe41bbe818d2"
	if actual := mustAdvertise(t, resourcepolicy.Defaults()); actual != expected {
		t.Fatalf("version 1 wire representation changed: %s", actual)
	}
}

func TestPolicyCompatibility(t *testing.T) {
	defaults := resourcepolicy.Defaults()
	for _, multiplier := range []int64{1, 2, 3} {
		policy := defaults
		policy.Execution.MaxRetries = multiplier - 1
		policy.Execution.MaxTimeoutSeconds = 600 * multiplier
		policy.Execution.DefaultTimeoutSeconds = 60 * multiplier
		policy.Artifact.DownloadBytes = multiplier << 20
		policy.Artifact.ExtractedBytes = multiplier << 20
		policy.Artifact.Files = 10 * multiplier
		policy.Artifact.StagingNodes = 20 * multiplier
		policy.Artifact.MetadataBytes = multiplier << 16
		policy.Staging.PayloadBytes = multiplier << 20
		policy.Staging.Entries = 30 * multiplier
		policy.Staging.ArchiveBytes = 2 * multiplier << 20
		policy.Container.PIDs = 8 * multiplier
		policy.Container.HelperPIDs = 4 * multiplier
		policy.Container.HelperCPUs = float64(multiplier) / 2
		policy.Container.TmpfsBytes = multiplier << 20
		policy.Container.HelperMemoryOverheadBytes = multiplier << 20
		advertisement, err := policy.Advertisement()
		if err != nil {
			t.Fatal(err)
		}
		if err := policy.CheckCompatibility(advertisement); err != nil {
			t.Fatal(err)
		}
		for _, peer := range []string{"", mustAdvertise(t, defaults)} {
			if err := policy.CheckCompatibility(peer); !errors.Is(err, resourcepolicy.ErrIncompatible) {
				t.Fatalf("custom policy accepted incompatible peer: %v", err)
			}
		}
		if err := defaults.CheckCompatibility(advertisement); !errors.Is(err, resourcepolicy.ErrIncompatible) {
			t.Fatalf("default policy accepted custom peer: %v", err)
		}
	}
	if err := defaults.CheckCompatibility(""); err != nil {
		t.Fatalf("legacy default peer: %v", err)
	}
	if err := defaults.CheckCompatibility(mustAdvertise(t, defaults)); err != nil {
		t.Fatalf("policy-aware default peer: %v", err)
	}
}

func TestPolicyAdvertisementCoversEveryField(t *testing.T) {
	defaults := resourcepolicy.Defaults()
	value := reflect.ValueOf(defaults)
	for group := 0; group < value.NumField(); group++ {
		fields := value.Field(group)
		for field := 0; field < fields.NumField(); field++ {
			name := value.Type().Field(group).Name + "/" + fields.Type().Field(field).Name
			t.Run(name, func(t *testing.T) {
				checkPolicyField(t, group, field)
			})
		}
	}
}

func checkPolicyField(t *testing.T, group, field int) {
	t.Helper()
	defaults := resourcepolicy.Defaults()
	policy := defaults
	changed := reflect.ValueOf(&policy).Elem().Field(group).Field(field)
	if changed.Kind() == reflect.Int64 {
		changed.SetInt(changed.Int() + 1)
	} else {
		changed.SetFloat(changed.Float() + 0.5)
	}
	advertisement := mustAdvertise(t, policy)
	if advertisement == mustAdvertise(t, defaults) {
		t.Fatal("policy field omitted from advertisement")
	}
	if err := defaults.CheckCompatibility(advertisement); !errors.Is(err, resourcepolicy.ErrIncompatible) {
		t.Fatalf("changed policy accepted: %v", err)
	}
	if err := policy.CheckCompatibility(""); !errors.Is(err, resourcepolicy.ErrIncompatible) {
		t.Fatalf("legacy peer accepted for changed field: %v", err)
	}
	if err := policy.CheckCompatibility(advertisement); err != nil {
		t.Fatalf("matching changed policy rejected: %v", err)
	}
}

func TestPolicyAdvertisementRejectsInvalidPolicy(t *testing.T) {
	for _, change := range []func(*resourcepolicy.Limits){
		func(p *resourcepolicy.Limits) { p.Container.PIDs = 0 },
		func(p *resourcepolicy.Limits) { p.Container.HelperCPUs = math.NaN() },
		func(p *resourcepolicy.Limits) { p.Staging.Entries = math.MaxInt64 },
		func(p *resourcepolicy.Limits) { p.Execution.DefaultTimeoutSeconds = math.MaxInt64 },
	} {
		policy := resourcepolicy.Defaults()
		change(&policy)
		if advertisement, err := policy.Advertisement(); err == nil || advertisement != "" {
			t.Fatal("advertised invalid local policy")
		}
		for _, peer := range []string{"", mustAdvertise(t, resourcepolicy.Defaults())} {
			if err := policy.CheckCompatibility(peer); err == nil {
				t.Fatal("checked compatibility with invalid local policy")
			}
		}
	}
}

func TestPolicyAdvertisementRejectsMalformedPeer(t *testing.T) {
	policy := resourcepolicy.Defaults()
	valid := mustAdvertise(t, policy)
	diagnostic := policy.CheckCompatibility("operator-secret").Error()
	for _, peer := range []string{
		" ", "resource-policy/2", strings.ToUpper(valid),
		strings.Replace(valid, "resource-policy/1", "resource-policy/2", 1),
		valid[:len(valid)-1], valid + "0", valid + "," + valid,
		valid + "\r\n", "operator-secret", strings.Repeat("x", 1<<20),
	} {
		err := policy.CheckCompatibility(peer)
		if !errors.Is(err, resourcepolicy.ErrIncompatible) {
			t.Fatalf("accepted malformed peer: %v", err)
		}
		if err.Error() != diagnostic {
			t.Fatal("diagnostic retained untrusted advertisement")
		}
	}
}

func mustAdvertise(t *testing.T, policy resourcepolicy.Limits) string {
	t.Helper()
	value, err := policy.Advertisement()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
