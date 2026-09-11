#!/bin/sh
set -eu

# Docker starts with only identity-switching capabilities as root. setpriv
# transfers them to the agent, which clears them before executing Bash.
exec /usr/bin/setpriv \
	--reuid=durpdeploy-agent --regid=durpdeploy-agent --clear-groups \
	--inh-caps=+setuid,+setgid,+setpcap \
	--ambient-caps=+setuid,+setgid,+setpcap \
	-- "$@"
