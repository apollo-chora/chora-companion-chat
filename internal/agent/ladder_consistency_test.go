package agent

import (
	"testing"

	"github.com/apollo-chora/chora-adk-common/growthstageplugin"
)

// agentRegistryKeys mirrors the availableTools map in cmd/familiar/main.go
// (package main is unimportable; main.go boot-asserts the same invariant at
// process start, so a drift between this mirror and the binary fails there).
var agentRegistryKeys = map[string]bool{
	"atom.cite": true,
}

// ADR-249 A1a agree-invariant: every ladder name in the growth table either
// resolves (via LadderToRegistryKey) to a registry key this build ships, or
// is explicitly declared unshipped. Nothing may vanish into the
// narrowing-safe WARN silently.
func TestGrowthLadderNamesResolveOrAreDeclaredUnshipped(t *testing.T) {
	table, err := growthstageplugin.Default()
	if err != nil {
		t.Fatalf("growthstageplugin.Default: %v", err)
	}
	unshipped := map[string]bool{}
	for _, name := range table.Unshipped() {
		unshipped[name] = true
	}
	for stage := 0; stage <= 6; stage++ {
		caps, err := table.Resolve(stage)
		if err != nil {
			t.Fatalf("stage %d: %v", stage, err)
		}
		for _, ladder := range caps.AllowedTools {
			if unshipped[ladder] {
				continue
			}
			key, ok := growthstageplugin.LadderToRegistryKey[ladder]
			if !ok {
				t.Errorf("stage %d ladder name %q neither maps to a registry key nor is declared unshipped", stage, ladder)
				continue
			}
			if !agentRegistryKeys[key] {
				t.Errorf("stage %d ladder name %q maps to %q, which this build does not ship", stage, ladder, key)
			}
		}
	}
}

// The runtime allowlist (what growthstageplugin actually writes to
// growth.allowed_tools) must never advertise an unshipped name.
func TestShippedStageToolsCarryOnlyBackedNames(t *testing.T) {
	for stage := 0; stage <= 6; stage++ {
		shipped, err := growthstageplugin.ShippedStageTools(stage)
		if err != nil {
			t.Fatalf("stage %d: %v", stage, err)
		}
		for _, name := range shipped {
			key, ok := growthstageplugin.LadderToRegistryKey[name]
			if !ok || !agentRegistryKeys[key] {
				t.Errorf("stage %d shipped list advertises %q, which has no backing tool", stage, name)
			}
		}
	}
}
