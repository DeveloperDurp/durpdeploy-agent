package main

import "testing"

func TestPayloadReservedVariablesPreserveUnstagedCompatibility(t *testing.T) {
	for _, staged := range []bool{false, true} {
		for _, name := range []string{"DURPDEPLOY_STAGE_DIR", "DURPDEPLOY_APPROVED_DIR"} {
			// Given
			payload := deploymentPayload{
				Release:   releasePayload{Staging: staged},
				Variables: []variablePayload{{Name: name, Value: "legacy"}},
			}
			// When
			environment, _, err := payload.environment()
			// Then
			if staged && name == "DURPDEPLOY_STAGE_DIR" {
				if err == nil {
					t.Fatal("staged payload replaced its owned workspace")
				}
			} else if err != nil || environment[name] != "legacy" {
				t.Errorf(
					"staging=%t name=%s lost legacy value: %v",
					staged,
					name,
					err,
				)
			}
		}
	}
}
