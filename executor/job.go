package executor

import (
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"slices"
	"time"
)

// JobConfig supplies the values copied into an immutable Job.
type JobConfig struct {
	ArtifactDirectory string
	ArtifactVolume    string
	StageDirectory    string
	StageVolume       string
	ExecutionMode     agentproto.ExecutionMode
	ContainerImage    string
	VariableNames     []string
	DeploymentID      int64
	Name              string
	ScriptBody        string
	Interpreter       Interpreter
	Timeout           time.Duration
	MaxRetries        int
	Environment       map[string]string
	Secrets           []string
}

// Job is an immutable step execution request.
type Job struct {
	artifactDirectory string
	artifactVolume    string
	stageDirectory    string
	stageVolume       string
	executionMode     agentproto.ExecutionMode
	containerImage    string
	variableNames     []string
	deploymentID      int64
	name              string
	scriptBody        string
	interpreter       Interpreter
	timeout           time.Duration
	maxRetries        int
	environment       map[string]string
	secrets           []string
}

func NewJob(config JobConfig) Job {
	environment := make(map[string]string, len(config.Environment))
	for key, value := range config.Environment {
		environment[key] = value
	}
	return Job{
		artifactDirectory: config.ArtifactDirectory,
		artifactVolume:    config.ArtifactVolume,
		stageDirectory:    config.StageDirectory,
		stageVolume:       config.StageVolume,
		executionMode:     config.ExecutionMode,
		containerImage:    config.ContainerImage,
		variableNames:     slices.Clone(config.VariableNames),
		deploymentID:      config.DeploymentID,
		name:              config.Name,
		scriptBody:        config.ScriptBody,
		interpreter:       normalizeInterpreter(config.Interpreter),
		timeout:           config.Timeout,
		maxRetries:        config.MaxRetries,
		environment:       environment,
		secrets:           slices.Clone(config.Secrets),
	}
}
