package executor

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Interpreter is one of the fixed executables accepted by the agent protocol.
type Interpreter = agentproto.Interpreter

const (
	InterpreterBash    = agentproto.InterpreterBash
	InterpreterPwsh    = agentproto.InterpreterPwsh
	InterpreterPython3 = agentproto.InterpreterPython3
)

// ErrInterpreterUnavailable means the selected fixed executable was not found.
var ErrInterpreterUnavailable = errors.New("interpreter is unavailable")

var interpreters = []Interpreter{
	InterpreterBash,
	InterpreterPwsh,
	InterpreterPython3,
}

// SupportedInterpreters reports fixed interpreter executables visible inside
// the agent's execution boundary.
func SupportedInterpreters() []Interpreter {
	return discoverInterpreters(exec.LookPath)
}

func discoverInterpreters(lookPath func(string) (string, error)) []Interpreter {
	supported := make([]Interpreter, 0, len(interpreters))
	for _, interpreter := range interpreters {
		if _, err := lookPath(string(interpreter)); err == nil {
			supported = append(supported, interpreter)
		}
	}
	return supported
}

func normalizeInterpreter(interpreter Interpreter) Interpreter {
	if interpreter == "" {
		return InterpreterBash
	}
	return interpreter
}

func resolveInterpreter(interpreter Interpreter) (string, error) {
	interpreter = normalizeInterpreter(interpreter)
	if _, err := agentproto.ParseInterpreter(string(interpreter)); err != nil {
		return "", err
	}
	executable, err := exec.LookPath(string(interpreter))
	if err != nil {
		return "", fmt.Errorf(
			"%w: %s executable not found",
			ErrInterpreterUnavailable,
			interpreter,
		)
	}
	return executable, nil
}

func scriptExtension(interpreter Interpreter) string {
	switch normalizeInterpreter(interpreter) {
	case InterpreterPwsh:
		return ".ps1"
	case InterpreterPython3:
		return ".py"
	default:
		return ".sh"
	}
}
