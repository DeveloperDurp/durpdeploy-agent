package configuration

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const FileEnv = "DURPDEPLOY_AGENT_CONFIG_FILE"

type Lookup func(string) (string, bool)

// Load resolves environment, optional complete YAML, then built-in defaults.
func Load() (Config, error) { return LoadWithLookup(os.LookupEnv) }

// LoadWithLookup preserves explicitly empty overrides. An empty file selector
// disables file loading. Relative paths use the process working directory.
func LoadWithLookup(lookup Lookup) (Config, error) {
	var reader io.Reader
	if path, _ := lookup(FileEnv); path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("cannot read %s", FileEnv)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	return resolve(reader, lookup)
}

func resolve(reader io.Reader, lookup Lookup) (Config, error) {
	cfg := Defaults()
	value := reflect.ValueOf(&cfg).Elem()
	if reader != nil {
		decoder := yaml.NewDecoder(reader)
		var document yaml.Node
		if err := decoder.Decode(&document); err != nil {
			return Config{}, errors.New("invalid YAML configuration")
		}
		if len(document.Content) != 1 {
			return Config{}, errors.New("expected one YAML mapping")
		}
		if err := applyYAML(document.Content[0], value); err != nil {
			return Config{}, err
		}
		var extra yaml.Node
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return Config{}, errors.New("expected one YAML document")
		}
	}
	if err := applyEnvironment(value, "", lookup); err != nil {
		return Config{}, err
	}
	if err := cfg.Limits.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Errors omit input values because startup fields can contain sensitive text.
func applyYAML(node *yaml.Node, value reflect.Value) error {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" || node.Anchor != "" {
		return errors.New("configuration sections must be YAML mappings")
	}
	seen := make(map[string]bool)
	for i := 0; i < len(node.Content); i += 2 {
		key, child := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" ||
			key.Anchor != "" || seen[key.Value] {
			return errors.New("configuration keys must be unique strings")
		}
		seen[key.Value] = true
		index := fieldIndex(value.Type(), key.Value)
		if index < 0 {
			return errors.New("unknown YAML configuration key")
		}
		if err := applyField(child, value.Field(index), key.Value); err != nil {
			return err
		}
	}
	return nil
}

func applyField(node *yaml.Node, value reflect.Value, name string) error {
	if value.Kind() == reflect.Struct {
		return applyYAML(node, value)
	}
	if err := applyScalar(node, value); err != nil {
		return fmt.Errorf("invalid scalar type for %s", name)
	}
	return nil
}

func applyScalar(node *yaml.Node, value reflect.Value) error {
	if node.Kind != yaml.ScalarNode || node.Anchor != "" {
		return errors.New("expected a scalar")
	}
	if value.Kind() == reflect.String {
		if node.Tag != "!!str" && node.Tag != "!!bool" && node.Tag != "!!int" {
			return errors.New("expected text, a Boolean, or an integer")
		}
		value.SetString(node.Value)
		return nil
	}
	integer := node.Tag == "!!int"
	floating := value.Kind() == reflect.Float64 && node.Tag == "!!float"
	if !integer && !floating {
		return errors.New("expected a number")
	}
	return node.Decode(value.Addr().Interface())
}

func fieldIndex(t reflect.Type, name string) int {
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Tag.Get("yaml") == name {
			return i
		}
	}
	return -1
}

func applyEnvironment(value reflect.Value, path string, lookup Lookup) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		name := field.Tag.Get("yaml")
		if path != "" {
			name = path + "." + name
		}
		v := value.Field(i)
		if v.Kind() == reflect.Struct {
			if err := applyEnvironment(v, name, lookup); err != nil {
				return err
			}
			continue
		}
		env := field.Tag.Get("env")
		if env == "" {
			env = "DURPDEPLOY_AGENT_" + strings.ToUpper(strings.ReplaceAll(name, ".", "_"))
		}
		if raw, set := lookup(env); set {
			if err := setEnvironment(v, raw); err != nil {
				return fmt.Errorf("invalid value for %s", env)
			}
		}
	}
	return nil
}

func setEnvironment(value reflect.Value, raw string) error {
	switch value.Kind() {
	case reflect.String:
		value.SetString(raw)
	case reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		value.SetInt(n)
	case reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		value.SetFloat(n)
	default:
		return errors.New("unsupported configuration type")
	}
	return nil
}
