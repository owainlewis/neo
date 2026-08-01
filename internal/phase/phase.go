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

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

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

// ExpandInvocation expands one slash-invoked phase with optional arguments.
func ExpandInvocation(definition Definition, args string) string {
	request := strings.TrimSpace(args)
	if request == "" {
		request = "Apply this phase to the current repository and conversation context."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[named phase: %s]\n", definition.Name)
	b.WriteString("Apply this named prompt with the normal Neo tools and workflow behavior.\n")
	fmt.Fprintf(&b, "\n[phase: %s]\n%s\n", definition.Name, strings.TrimSpace(definition.Prompt))
	b.WriteString("\nUser request:\n")
	b.WriteString(request)
	return strings.TrimSpace(b.String())
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
	b.WriteString("Named phases are focused prompts for one turn. The user invokes one with `/name args` in the interactive UI. They do not replace the workflow checklist.\n")
	for _, definition := range definitions {
		fmt.Fprintf(&b, "\n- `/%s`: %s", definition.Name, definition.Description)
	}
	return b.String()
}
