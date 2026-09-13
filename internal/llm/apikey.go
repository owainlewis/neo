package llm

import (
	"fmt"
	"os"
	"strings"
)

// APIKeyFromEnv returns the API key configured in environmentVariable. When it
// is absent, it provides the common next steps for configuring a provider.
func APIKeyFromEnv(environmentVariable, keyURL string) (string, error) {
	key := strings.TrimSpace(os.Getenv(environmentVariable))
	if key != "" {
		return key, nil
	}

	return "", fmt.Errorf(
		"%s is not set. Create an API key at %s, then export it with `export %s=\"your-api-key\"`. Run `neo doctor` to check your setup",
		environmentVariable,
		keyURL,
		environmentVariable,
	)
}
