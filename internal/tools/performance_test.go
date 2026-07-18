package tools

import "testing"

// BenchmarkRegistrySpecs measures the core, in-memory path used to expose the
// built-in tool catalogue to providers. Filesystem and command execution are
// deliberately excluded because host I/O makes them noisy performance gates.
func BenchmarkRegistrySpecs(b *testing.B) {
	registry := NewRegistry(
		Bash{},
		ReadFile{},
		WriteFile{},
		EditFile{},
		Grep{},
		Glob{},
	)
	b.ReportAllocs()

	for b.Loop() {
		if specs := registry.Specs(); len(specs) != 6 {
			b.Fatalf("spec count = %d, want 6", len(specs))
		}
	}
}
