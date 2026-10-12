package artifact

import (
	"errors"
	"strings"
	"testing"
)

func TestPinValidation(t *testing.T) {
	for _, test := range []struct {
		name  string
		pin   Pin
		valid bool
	}{
		{"valid", Pin{strings.Repeat("a", 64), 1}, true},
		{"missing digest", Pin{"", 1}, false},
		{"short digest", Pin{"aa", 1}, false},
		{"invalid digest", Pin{strings.Repeat("g", 64), 1}, false},
		{"empty", Pin{strings.Repeat("a", 64), 0}, false},
		{"oversized", Pin{strings.Repeat("a", 64), MaxDownload + 1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.pin.Validate()
			if (err == nil) != test.valid ||
				(err != nil && !errors.Is(err, ErrInvalid)) {
				t.Fatalf("pin=%+v error=%v", test.pin, err)
			}
		})
	}
}
