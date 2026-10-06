package executor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Step is one immutable step from a release snapshot.
type Step struct {
	ExecutionMode  agentproto.ExecutionMode `json:"execution_mode,omitempty"`
	ContainerImage string                   `json:"container_image,omitempty"`
	VariableNames  []string                 `json:"variable_names,omitempty"`
	Name           string                   `json:"name"`
	ScriptBody     string                   `json:"script_body"`
	Interpreter    Interpreter              `json:"interpreter,omitempty"`
	SortOrder      int64                    `json:"sort_order"`
	TimeoutSeconds int64                    `json:"timeout_seconds"`
	MaxRetries     int64                    `json:"max_retries"`
}

// MarshalJSON keeps Bash compatible with strict agent/1 payload decoders.
func (s Step) MarshalJSON() ([]byte, error) {
	type wireStep Step
	copy := s
	if err := copy.ValidateExecution(); err != nil {
		return nil, err
	}
	interpreter := normalizeInterpreter(copy.Interpreter)
	if _, err := agentproto.ParseInterpreter(string(interpreter)); err != nil {
		return nil, err
	}
	if interpreter == InterpreterBash {
		copy.Interpreter = ""
	}
	return json.Marshal(wireStep(copy))
}

// MarshalForProtocol refuses lossy downgrades. Servers must select a compatible
// agent before encrypting this representation into a deployment payload.
func (s Step) MarshalForProtocol(
	version agentproto.ProtocolVersion,
) ([]byte, error) {
	if _, err := agentproto.ParseProtocolVersion(string(version)); err != nil {
		return nil, err
	}
	if err := s.ValidateExecution(); err != nil {
		return nil, err
	}
	copy := s
	if version != agentproto.AgentV3 {
		if s.ExecutionMode == agentproto.ExecutionContainer ||
			s.ContainerImage != "" ||
			len(s.VariableNames) != 0 {
			return nil, agentproto.ErrUnsupportedProtocol
		}
		copy.ExecutionMode = ""
		if version == agentproto.AgentV1 &&
			normalizeInterpreter(s.Interpreter) != InterpreterBash {
			return nil, agentproto.ErrUnsupportedProtocol
		}
	}
	return json.Marshal(copy)
}

var ErrInvalidStepExecution = errors.New("invalid step execution configuration")

func (s Step) ValidateExecution() error {
	mode := s.ExecutionMode
	if mode == "" {
		mode = agentproto.ExecutionHost
	}
	if _, err := agentproto.ParseExecutionMode(string(mode)); err != nil {
		return err
	}
	if mode == agentproto.ExecutionHost && s.ContainerImage != "" {
		return ErrInvalidStepExecution
	}
	if mode == agentproto.ExecutionContainer {
		if err := validateContainerImage(s.ContainerImage); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(s.VariableNames))
	for _, name := range s.VariableNames {
		if !validVariableName(name) || seen[name] {
			return ErrInvalidStepExecution
		}
		if mode == agentproto.ExecutionContainer &&
			reservedContainerVariable(name) {
			return ErrInvalidStepExecution
		}
		seen[name] = true
	}
	return nil
}

// ExecutionConfig supplies a deployment's immutable step snapshot and its
// environment to the sandboxed executor.
type ExecutionConfig struct {
	DeploymentID     int64
	Steps            []Step
	Environment      map[string]string
	Secrets          []string
	CallbacksForStep func(Step) Callbacks
	StepFinished     func(Step, error)
}

// ExecuteSteps runs steps in order, stopping at the first failed or cancelled
// step. Both the server and pull agents use this path so process, sandbox,
// redaction, retry, and cancellation behavior remains identical.
func (e *Executor) ExecuteSteps(
	ctx context.Context,
	config ExecutionConfig,
) error {
	for _, step := range config.Steps {
		callbacks := Callbacks{}
		if config.CallbacksForStep != nil {
			callbacks = config.CallbacksForStep(step)
		}
		err := e.Execute(ctx, NewJob(JobConfig{
			DeploymentID:   config.DeploymentID,
			Name:           step.Name,
			ScriptBody:     step.ScriptBody,
			Interpreter:    step.Interpreter,
			ExecutionMode:  step.ExecutionMode,
			ContainerImage: step.ContainerImage,
			VariableNames:  step.VariableNames,
			Timeout:        time.Duration(step.TimeoutSeconds) * time.Second,
			MaxRetries:     int(step.MaxRetries),
			Environment:    config.Environment,
			Secrets:        config.Secrets,
		}), callbacks)
		if config.StepFinished != nil {
			config.StepFinished(step, err)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
