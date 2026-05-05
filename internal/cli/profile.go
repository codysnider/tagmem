package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

var profiledCommands = map[string]struct{}{
	"add":    {},
	"search": {},
}

type profilePhase struct {
	name     string
	duration time.Duration
}

type profiler struct {
	enabled bool
	stderr  io.Writer
	command string
	started time.Time
	phases  []profilePhase
}

func newProfiler(stderr io.Writer) *profiler {
	if os.Getenv("TAGMEM_PROFILE") != "1" {
		return &profiler{stderr: stderr}
	}

	return &profiler{
		enabled: true,
		stderr:  stderr,
		started: time.Now(),
	}
}

func (p *profiler) setCommand(command string) {
	if !p.enabled {
		return
	}
	if _, ok := profiledCommands[command]; !ok {
		p.enabled = false
		return
	}
	p.command = command
}

func (p *profiler) startPhase(name string) func() {
	if !p.enabled {
		return func() {}
	}

	started := time.Now()
	return func() {
		p.phases = append(p.phases, profilePhase{name: name, duration: time.Since(started)})
	}
}

func (p *profiler) print(exitCode int) {
	if !p.enabled || p.command == "" {
		return
	}

	parts := make([]string, 0, len(p.phases))
	for _, phase := range p.phases {
		parts = append(parts, fmt.Sprintf("%s=%s", phase.name, phase.duration.Round(time.Microsecond)))
	}

	fmt.Fprintf(p.stderr, "[profile] command=%s exit=%d total=%s\n", p.command, exitCode, time.Since(p.started).Round(time.Microsecond))
	if len(parts) > 0 {
		fmt.Fprintf(p.stderr, "[profile] phases %s\n", strings.Join(parts, " "))
	}
}
