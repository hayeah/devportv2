package devport

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var placeholderPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

type Environment struct {
	values map[string]string
}

func LoadEnvironment(service ServiceSpec) (Environment, error) {
	values := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}

	for _, path := range service.EnvFiles {
		expanded, err := ExpandPath(path)
		if err != nil {
			return Environment{}, fmt.Errorf("expand env file %s: %w", path, err)
		}
		loaded, err := godotenv.Read(expanded)
		if err != nil {
			return Environment{}, fmt.Errorf("read env file %s: %w", path, err)
		}
		for key, value := range loaded {
			values[key] = value
		}
	}

	if service.Port > 0 {
		portStr := strconv.Itoa(service.Port)
		if service.PortEnv != "" {
			for _, name := range strings.Split(service.PortEnv, ":") {
				values[name] = portStr
			}
		} else {
			values["PORT"] = portStr
		}
	}

	return Environment{values: values}, nil
}

func (e Environment) Lookup(key string) (string, bool) {
	value, ok := e.values[key]
	return value, ok
}

func (e Environment) ExpandString(value string) string {
	return placeholderPattern.ReplaceAllStringFunc(value, func(match string) string {
		submatches := placeholderPattern.FindStringSubmatch(match)
		if len(submatches) != 2 {
			return match
		}
		key := submatches[1]
		if value, ok := e.values[key]; ok {
			return value
		}
		return ""
	})
}

func (e Environment) ExpandSlice(values []string) []string {
	expanded := make([]string, len(values))
	for index, value := range values {
		expanded[index] = e.ExpandString(value)
	}
	return expanded
}

func (e Environment) Environ() []string {
	env := make([]string, 0, len(e.values))
	for key, value := range e.values {
		env = append(env, key+"="+value)
	}
	return env
}
