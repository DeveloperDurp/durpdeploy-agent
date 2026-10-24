package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

type deploymentPayload struct {
	DeploymentID int64              `json:"deployment_id"`
	Release      releasePayload     `json:"release"`
	Environment  environmentPayload `json:"environment"`
	Variables    []variablePayload  `json:"variables"`
}

type releasePayload struct {
	Artifact  *artifact.Pin `json:"artifact,omitempty"`
	Staging   bool          `json:"staging,omitempty"`
	ID        int64         `json:"id"`
	ProjectID int64         `json:"project_id"`
	Version   string        `json:"version"`
	Steps     []runner.Step `json:"steps"`
}

type environmentPayload struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type stepPayload = runner.Step

type variablePayload struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

func decodePayload(raw []byte, deploymentID int64) (deploymentPayload, error) {
	return decodePayloadWithResourcePolicy(raw, deploymentID, resourcepolicy.Defaults())
}

func decodePayloadWithResourcePolicy(raw []byte, deploymentID int64, policy resourcepolicy.Limits) (deploymentPayload, error) {
	if err := policy.Validate(); err != nil {
		return deploymentPayload{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload deploymentPayload
	if err := decoder.Decode(&payload); err != nil {
		return deploymentPayload{}, errors.New("invalid deployment payload")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return deploymentPayload{}, errors.New("invalid deployment payload")
	}
	if payload.DeploymentID != deploymentID {
		return deploymentPayload{}, errors.New("deployment payload ID mismatch")
	}
	if payload.Release.Staging && len(payload.Release.Steps) != 1 {
		return deploymentPayload{}, errors.New(
			"staging requires one claimed step",
		)
	}
	if err := payload.Release.validateArtifact(policy.Artifact); err != nil {
		return deploymentPayload{}, err
	}
	for index, step := range payload.Release.Steps {
		if err := validatePayloadStep(step, index, policy.Execution); err != nil {
			return deploymentPayload{}, err
		}
		if step.Interpreter == "" {
			payload.Release.Steps[index].Interpreter = runner.InterpreterBash
		}
	}
	return payload, nil
}

func (release releasePayload) validateArtifact(policy resourcepolicy.Artifact) error {
	if release.Artifact == nil {
		return nil
	}
	if !release.Staging {
		return artifact.ErrInvalid
	}
	return release.Artifact.ValidateWithPolicy(policy)
}

func validatePayloadStep(step runner.Step, index int, policy resourcepolicy.Execution) error {
	if err := step.ValidateExecution(); err != nil {
		return err
	}
	if err := step.ValidateLimits(policy); err != nil {
		return err
	}
	if step.Name == "" || step.ScriptBody == "" || step.SortOrder != int64(index+1) {
		return errors.New("invalid deployment payload")
	}
	return nil
}

func (payload deploymentPayload) validateExecution(
	supports func(runner.Step) bool,
) error {
	for _, step := range payload.Release.Steps {
		if !supports(step) {
			return errors.New(
				"deployment requires unavailable execution capabilities",
			)
		}
	}
	return nil
}

func (payload deploymentPayload) validateInterpreters(
	supports func(runner.Interpreter) bool,
) error {
	for _, step := range payload.Release.Steps {
		if !supports(step.Interpreter) {
			return fmt.Errorf(
				"step %q requires unavailable interpreter %q",
				step.Name,
				step.Interpreter,
			)
		}
	}
	return nil
}

func (payload deploymentPayload) environment() (map[string]string, []string, error) {
	environment := make(map[string]string, len(payload.Variables))
	secrets := make([]string, 0, len(payload.Variables))
	for _, variable := range payload.Variables {
		if payload.Release.Artifact != nil && variable.Name == artifact.PathVariable {
			return nil, nil, artifact.ErrInvalid
		}
		if payload.Release.Staging && variable.Name == "DURPDEPLOY_STAGE_DIR" {
			return nil, nil, errors.New("reserved deployment variable")
		}
		if variable.Name == "" {
			return nil, nil, errors.New("invalid deployment variable")
		}
		if _, exists := environment[variable.Name]; exists {
			return nil, nil, fmt.Errorf(
				"duplicate deployment variable %q",
				variable.Name,
			)
		}
		environment[variable.Name] = variable.Value
		if variable.Secret && variable.Value != "" {
			secrets = append(secrets, variable.Value)
		}
	}
	return environment, secrets, nil
}
