package phase

// Defaults returns fresh copies of Neo's opinionated built-in phase prompts.
func Defaults() []Definition {
	return []Definition{
		{
			Name:        "design",
			Description: "Design a product change, feature, or bug fix",
			Prompt: `Design the requested change before implementation.

Read the repository instructions, relevant documentation, and current code. Ground every claim in the system that exists today. Resolve choices that change behavior, interfaces, data, failures, security, operations, tests, or compatibility. Define clear acceptance criteria and the smallest coherent scope.

For non-trivial work, create a visible workflow before inspecting. Return a concise design in chat unless the user asks for a file. Stop after the design. Do not change production code.`,
		},
		{
			Name:        "plan",
			Description: "Break accepted work into small, verifiable tasks",
			Prompt: `Plan the requested work without implementing it.

Read the repository instructions, accepted design when present, and the current code needed to make the plan real. Break the outcome into small ordered tasks. Give each task one concrete result, its dependencies, and the checks that prove it complete. Keep tasks independently understandable and avoid speculative work outside the accepted scope.

Create a visible workflow for the planning work when it is multi-step. Return the ordered plan in chat unless the user asks for a file. Stop before changing production code.`,
		},
		{
			Name:        "build",
			Description: "Implement, test, and self-review a complete change",
			Prompt: `Build the requested change completely.

Read the repository instructions, design, plan, and relevant code. For multi-step work, create a visible workflow and keep it current. Implement the smallest coherent change without placeholders. Run focused checks as you work, then review the complete diff for correctness, regressions, security, unnecessary complexity, and missing tests. Fix valid findings, simplify where useful, rerun affected checks, and update documentation when behavior changes.

Do not publish, deploy, or merge unless the user explicitly asks. Finish with concise verification evidence and anything still unverified.`,
		},
		{
			Name:        "review",
			Description: "Review and improve code, PR feedback, and CI results",
			Prompt: `Review the requested scope with fresh context and improve it where appropriate.

Read the repository instructions and determine the review target from the arguments. With no explicit target, review the current working change or branch. Inspect the full diff and surrounding code. Check behavior, correctness, regressions, security, failure handling, unnecessary complexity, and test coverage. For non-trivial changes, use a fresh read-only subagent when available to challenge the implementation. Fix valid findings within scope, simplify the code, rerun affected checks, and review the result again.

When PR feedback or CI is in scope, inspect the current feedback and checks, address actionable issues, and verify the fixes. For an explicitly repo-wide audit, report prioritized findings before making broad changes. Do not publish or merge unless the user explicitly asks.`,
		},
	}
}
