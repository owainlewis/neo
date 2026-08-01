// Package phase provides Neo's built-in and configured named prompts.
// A phase changes the instructions for one turn and supplies a small UI label;
// it does not own workflow state or enforce lifecycle transitions.
package phase

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Definition is one named prompt exposed as /name in the interactive UI.
type Definition struct {
	Name        string `yaml:"-"`
	Description string `yaml:"description"`
	Prompt      string `yaml:"prompt"`
}

var (
	validName  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	runPattern = regexp.MustCompile(`(?i)\brun\s+(?:the\s+)?(.{1,120}?)\s+phases?\b`)
)

var reservedNames = map[string]bool{
	"clear": true,
	"help":  true,
	"model": true,
}

// Resolve overlays configured definitions on Neo's defaults. Defaults retain
// their product order; additional configured phases are appended by name.
func Resolve(configured map[string]Definition) ([]Definition, error) {
	defaults := Defaults()
	byName := make(map[string]Definition, len(defaults)+len(configured))
	order := make([]string, 0, len(defaults)+len(configured))
	for _, definition := range defaults {
		byName[definition.Name] = definition
		order = append(order, definition.Name)
	}

	var additional []string
	for rawName, override := range configured {
		name := strings.TrimSpace(rawName)
		if name == "" || name != strings.ToLower(name) || !validName.MatchString(name) {
			return nil, fmt.Errorf("name %q must use lowercase letters, numbers, hyphens, or underscores", rawName)
		}
		if reservedNames[name] {
			return nil, fmt.Errorf("name %q is reserved for a native command", name)
		}

		definition, exists := byName[name]
		if !exists {
			definition = Definition{Name: name, Description: "Run the " + DisplayName(name) + " phase"}
			additional = append(additional, name)
		}
		if description := strings.TrimSpace(override.Description); description != "" {
			definition.Description = description
		}
		if prompt := strings.TrimSpace(override.Prompt); prompt != "" {
			definition.Prompt = prompt
		}
		if definition.Prompt == "" {
			return nil, fmt.Errorf("phase %q needs a prompt", name)
		}
		byName[name] = definition
	}

	sort.Strings(additional)
	order = append(order, additional...)
	out := make([]Definition, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out, nil
}

// Find returns one definition by its case-insensitive invocation name.
func Find(definitions []Definition, name string) (Definition, bool) {
	name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "/")))
	for _, definition := range definitions {
		if definition.Name == name {
			return definition, true
		}
	}
	return Definition{}, false
}

// MatchRun finds explicit natural-language requests such as "run the review
// phase" or "run design, plan and build phases". Every meaningful token in
// the matched segment must name a configured phase, which avoids treating a
// casual mention of a phase as an invocation.
func MatchRun(input string, definitions []Definition) []Definition {
	known := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		known[definition.Name] = definition
	}

	var selected []Definition
	seen := map[string]bool{}
	for _, match := range runPattern.FindAllStringSubmatch(input, -1) {
		if len(match) != 2 {
			continue
		}
		segment := strings.NewReplacer(",", " ", ";", " ").Replace(strings.ToLower(match[1]))
		var candidate []Definition
		valid := true
		for _, token := range strings.Fields(segment) {
			switch token {
			case "and", "then", "the":
				continue
			}
			definition, ok := known[token]
			if !ok {
				valid = false
				break
			}
			candidate = append(candidate, definition)
		}
		if !valid || len(candidate) == 0 {
			continue
		}
		for _, definition := range candidate {
			if !seen[definition.Name] {
				seen[definition.Name] = true
				selected = append(selected, definition)
			}
		}
	}
	return selected
}

// Expand prepends the selected prompt bodies to the user's request. The
// original request remains visible at the end so task-specific scope wins.
func Expand(input string, selected []Definition) string {
	if len(selected) == 0 {
		return input
	}
	var b strings.Builder
	names := make([]string, len(selected))
	for i, definition := range selected {
		names[i] = definition.Name
	}
	fmt.Fprintf(&b, "[named phases: %s]\n", strings.Join(names, ", "))
	b.WriteString("Apply these named prompts in order. Keep the normal Neo tools and workflow behavior.\n")
	for _, definition := range selected {
		fmt.Fprintf(&b, "\n[phase: %s]\n%s\n", definition.Name, strings.TrimSpace(definition.Prompt))
	}
	if strings.TrimSpace(input) != "" {
		b.WriteString("\nUser request:\n")
		b.WriteString(input)
	}
	return strings.TrimSpace(b.String())
}

// ExpandInvocation expands one slash-invoked phase with optional arguments.
func ExpandInvocation(definition Definition, args string) string {
	request := strings.TrimSpace(args)
	if request == "" {
		request = "Apply this phase to the current repository and conversation context."
	}
	return Expand(request, []Definition{definition})
}

// Label returns a compact display label for one or more active phases.
func Label(definitions []Definition) string {
	labels := make([]string, len(definitions))
	for i, definition := range definitions {
		labels[i] = DisplayName(definition.Name)
	}
	return strings.Join(labels, " → ")
}

// DisplayName turns an invocation name into a compact UI label.
func DisplayName(name string) string {
	name = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), "-", " "), "_", " ")
	if name == "" {
		return "Phase"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// Augment advertises only phase names and descriptions in the stable system
// prompt. Full prompt bodies are injected only when a phase is invoked.
func Augment(base string, definitions []Definition) string {
	if len(definitions) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n# Named phases\n\n")
	b.WriteString("Named phases are focused prompts for one turn. The user invokes one with `/name args` or asks to run named phases. They do not replace the workflow checklist.\n")
	for _, definition := range definitions {
		fmt.Fprintf(&b, "\n- `/%s`: %s", definition.Name, definition.Description)
	}
	return b.String()
}
