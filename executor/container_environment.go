package executor

import (
	"fmt"
	"strings"
)

func validVariableName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		if character == '_' || character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func reservedContainerVariable(name string) bool {
	for _, prefix := range []string{"DOCKER_", "PODMAN_", "CONTAINER_", "CONTAINERS_", "SSH_", "XDG_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "HOME", "PATH", "TERM", "TMPDIR", "REGISTRY_AUTH_FILE":
		return true
	default:
		return false
	}
}

func selectStepEnvironment(
	environment map[string]string,
	names []string,
	container bool,
) (map[string]string, error) {
	selected := make(map[string]string, len(environment))
	if len(names) == 0 {
		for name, value := range environment {
			if container && reservedContainerVariable(name) {
				continue
			}
			if container &&
				(!validVariableName(name) || strings.ContainsRune(value, '\x00')) {
				return nil, ErrInvalidStepExecution
			}
			selected[name] = value
		}
		return selected, nil
	}
	for _, name := range names {
		value, exists := environment[name]
		if !exists || !validVariableName(name) ||
			strings.ContainsRune(value, '\x00') ||
			container && reservedContainerVariable(name) {
			return nil, fmt.Errorf(
				"selected variable is invalid or unavailable: %w",
				ErrInvalidStepExecution,
			)
		}
		selected[name] = value
	}
	return selected, nil
}

func validateContainerImage(image string) error {
	if image == "" || strings.HasPrefix(image, "-") ||
		strings.ContainsAny(image, " \t\r\n\x00") {
		return ErrInvalidStepExecution
	}
	// Runtime references are image names, never registry credentials or URLs.
	if strings.Contains(image, "://") {
		return ErrInvalidStepExecution
	}
	return nil
}
