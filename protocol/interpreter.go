package agentproto

import "encoding/json"

// Interpreter is a fixed executable supported by the agent protocol.
type Interpreter string

const (
	InterpreterBash    Interpreter = "bash"
	InterpreterPwsh    Interpreter = "pwsh"
	InterpreterPython3 Interpreter = "python3"
)

func ParseInterpreter(raw string) (Interpreter, error) {
	interpreter := Interpreter(raw)
	switch interpreter {
	case InterpreterBash, InterpreterPwsh, InterpreterPython3:
		return interpreter, nil
	default:
		return "", protocolError(
			"interpreter",
			ReasonInvalid,
			ErrInvalidInterpreter,
		)
	}
}

func (i *Interpreter) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError("interpreter", ReasonInvalid, ErrInvalidJSON)
	}
	parsed, err := ParseInterpreter(raw)
	if err != nil {
		return err
	}
	*i = parsed
	return nil
}
