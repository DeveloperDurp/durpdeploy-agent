package resourcepolicy

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"time"
)

// Validate checks ranges and arithmetic before consumers allocate or execute.
func (l Limits) Validate() error {
	if err := validateNumbers(reflect.ValueOf(l), "limits"); err != nil {
		return err
	}
	if err := l.Execution.Validate(); err != nil {
		return err
	}
	if err := l.Artifact.Validate(); err != nil {
		return err
	}
	if err := l.Staging.Validate(); err != nil {
		return err
	}
	return l.validateCapacity()
}

func validateNumbers(value reflect.Value, path string) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		name := path + "." + value.Type().Field(i).Tag.Get("yaml")
		if field.Kind() == reflect.Struct {
			if err := validateNumbers(field, name); err != nil {
				return err
			}
			continue
		}
		if err := validateNumber(field, name); err != nil {
			return err
		}
	}
	return nil
}

func validateNumber(value reflect.Value, name string) error {
	if value.Kind() != reflect.Int64 {
		return nil
	}
	n := value.Int()
	minimum := int64(1)
	if name == "limits.execution.max_retries" {
		minimum = 0
	}
	if n < minimum || n >= int64(^uint(0)>>1) {
		return fmt.Errorf("%s is outside the supported range", name)
	}
	if strings.HasSuffix(name, "_seconds") &&
		n > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("%s exceeds the duration range", name)
	}
	return nil
}

func (l Limits) validateCapacity() error {
	maxInt := int64(^uint(0) >> 1)
	if math.IsNaN(l.Container.HelperCPUs) ||
		math.IsInf(l.Container.HelperCPUs, 0) ||
		l.Container.HelperCPUs < 1e-9 ||
		l.Container.HelperCPUs >= float64(maxInt)/1e9 {
		return errors.New("container helper CPUs are outside the supported range")
	}
	if l.Artifact.DownloadBytes > maxInt-l.Container.TmpfsBytes {
		return errors.New("artifact temporary filesystem size overflows")
	}
	page := int64(os.Getpagesize())
	for _, stage := range []struct{ bytes, entries int64 }{
		{l.Staging.PayloadBytes, l.Staging.Entries},
		{l.Artifact.ExtractedBytes, l.Artifact.StagingNodes},
	} {
		if stage.entries > (maxInt-stage.bytes)/page ||
			stage.entries > (maxInt-2)/2 {
			return errors.New("staging temporary filesystem capacity overflows")
		}
		capacity := stage.bytes + stage.entries*page
		if capacity > maxInt-l.Container.HelperMemoryOverheadBytes {
			return errors.New("staging helper memory capacity overflows")
		}
	}
	return nil
}
