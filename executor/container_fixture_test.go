package executor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func containerFixture(
	t *testing.T,
	runtime agentproto.ContainerRuntime,
) (*ContainerExecutor, string) {
	t.Helper()
	directory := t.TempDir()
	socket := filepath.Join(directory, "runtime.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	script := fmt.Sprintf(`#!/bin/bash
set -eu
fixture=%q
while [[ "$1" == --* ]]; do shift; done
command=$1; shift
case "$command" in
info)
 if [[ -e "$fixture/unavailable" ]]; then exit 1; fi
 if [[ -e "$fixture/info-wait" ]]; then
   printf 'info\n' >> "$fixture/actions"
   if [[ -e "$fixture/info-wait-once" ]]; then
     /usr/bin/rm -f "$fixture/info-wait" "$fixture/info-wait-once"
   fi
   if [[ -e "$fixture/info-notify" ]]; then
     read -r host port < "$fixture/info-notify"
     exec 3<>/dev/tcp/$host/$port
     printf 'ready\n' >&3
   fi
   exec /usr/bin/sleep 30
 fi
 if [[ -e "$fixture/runtime-info" ]]; then cat "$fixture/runtime-info"; exit; fi
 printf '%%s' '{"Name":"physical-docker","OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=seccomp,profile=builtin"],"host":{"hostname":"physical-podman","os":"linux","cgroupControllers":["cpu","memory","pids"],"security":{"seccompEnabled":true,"seccompProfilePath":"/usr/share/containers/seccomp.json"}}}' ;;
ps)
 [[ ! -e "$fixture/owned" ]] || printf '%%s\n' owned
 ;;
volume) ;;
rm)
 printf 'remove\n' >> "$fixture/actions"
 [[ ! -e "$fixture/cleanup-fail" ]] || exit 1
 /usr/bin/rm -f "$fixture/owned" ;;
image)
	printf 'inspect\n' >> "$fixture/actions"
	if [[ -e "$fixture/retagged" ]]; then printf '%%s' '[{"Id":"sha256:changed","Config":{"Volumes":null}}]'; exit; fi
 if [[ -e "$fixture/missing" && ! -e "$fixture/pulled" ]]; then exit 1; fi
 if [[ -e "$fixture/volumes" ]]; then
   printf '%%s' '[{"Id":"sha256:fixed","Config":{"Volumes":{"/unsafe":{}}}}]'
 else printf '%%s' '[{"Id":"sha256:fixed","Config":{"Volumes":null}}]'; fi ;;
pull) printf 'pull\n' >> "$fixture/actions"; printf yes > "$fixture/pulled" ;;
run)
	if [[ -e "$fixture/retag" ]]; then printf yes > "$fixture/retagged"; fi
 if [[ -e "$fixture/runtime-info-after-run" ]]; then cat "$fixture/runtime-info-after-run" > "$fixture/runtime-info"; fi
 printf 'run\n' >> "$fixture/actions"
 printf yes > "$fixture/owned"
 printf '%%s\n' "$@" > "$fixture/argv"; printf '%%s\0' "$@" >> "$fixture/argv-history"; printf '\0' >> "$fixture/argv-history"
 if [[ -e "$fixture/missing-interpreter" ]]; then exit 127; fi
 if [[ -e "$fixture/run-fail" ]]; then exit 7; fi
 if [[ -e "$fixture/pwsh" ]]; then
   while [[ "$1" != sha256:fixed ]]; do shift; done
   shift
   # Emulate the container's private /tmp without writing to the host /tmp.
   args=()
   for arg in "$@"; do args+=("${arg//\/tmp\/durpdeploy-step.ps1/$fixture\/durpdeploy-step.ps1}"); done
   exec "$(cat "$fixture/pwsh")" "${args[@]}"
 fi
 printf 'ready\n'
 if [[ -e "$fixture/script-127" ]]; then printf 'script-started\n'; exit 127; fi
 if [[ -e "$fixture/excess-output" ]]; then
   while :; do printf '%%4096s' x; done
 fi
 if [[ -e "$fixture/wait" ]]; then /usr/bin/sleep 30; fi
 printf '%%s\n' "${REGION-unset}" "${DEPLOY_SECRET-unset}" "${UNRELATED-unset}"
 ;;
*) exit 2 ;;
esac
`, directory)
	binary := filepath.Join(directory, string(runtime))
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "service")
	runner, err := NewContainerExecutor(
		t.Context(),
		ContainerConfig{
			Runtime:         runtime,
			SocketURL:       "unix://" + socket,
			AgentID:         "fixture-agent",
			PairingIdentity: "fixture-server/fixture-certificate",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runner.Close(); err != nil {
			t.Error(err)
		}
	})
	return runner, directory
}
