package executor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Step is one immutable step from a release snapshot.
type Step struct {
	Name           string      `json:"name"`
	ScriptBody     string      `json:"script_body"`
	Interpreter    Interpreter `json:"interpreter,omitempty"`
	SortOrder      int64       `json:"sort_order"`
	TimeoutSeconds int64       `json:"timeout_seconds"`
	MaxRetries     int64       `json:"max_retries"`
}

// MarshalJSON keeps Bash compatible with strict agent/1 payload decoders.
func (s Step) MarshalJSON() ([]byte, error) {
	type wireStep Step
	copy := s
	interpreter := normalizeInterpreter(copy.Interpreter)
	if _, err := agentproto.ParseInterpreter(string(interpreter)); err != nil {
		return nil, err
	}
	if interpreter == InterpreterBash {
		copy.Interpreter = ""
	}
	return json.Marshal(wireStep(copy))
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
			DeploymentID: config.DeploymentID,
			Name:         step.Name,
			ScriptBody:   step.ScriptBody,
			Interpreter:  step.Interpreter,
			Timeout:      time.Duration(step.TimeoutSeconds) * time.Second,
			MaxRetries:   int(step.MaxRetries),
			Environment:  config.Environment,
			Secrets:      config.Secrets,
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
