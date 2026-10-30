// Package importers defines the shared contract for turning exported monitoring
// payloads into files inside an incident bundle, plus the source registry used by
// the kubectl-triage import command.
//
// Importers are pure and deterministic: the same Input always yields the same
// Output. They never read or write the filesystem; the CLI owns I/O so that
// --from, --force and exit codes stay in one place.
package importers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Input is one raw exporter payload, read from stdin or a --from file.
type Input struct {
	// Name is the --name flag value. Importers that do not take a name ignore
	// it; use DefaultName to declare whether the flag applies to a source.
	Name string
	// Data is the raw payload (JSON or YAML, depending on the source).
	Data []byte
}

// File is a single file to write, addressed relative to the incident bundle
// root with slash separators (for example "k8s/pods.yaml").
type File struct {
	Path string
	Data []byte
}

// Output is the deterministic, ordered set of files produced by one import.
type Output struct {
	Files []File
}

// Paths returns the bundle-relative paths in output order.
func (o *Output) Paths() []string {
	paths := make([]string, 0, len(o.Files))
	for _, f := range o.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

// Single wraps one generated file in an Output.
func Single(path string, data []byte) *Output {
	return &Output{Files: []File{{Path: path, Data: data}}}
}

// Importer converts one exported monitoring payload into incident bundle files.
type Importer interface {
	// Name is the stable registry key: kubectl-triage import NAME DIR.
	Name() string
	// DefaultName is the default value of --name. An empty string means the
	// source does not use --name and the CLI rejects the flag for it.
	DefaultName() string
	// Import parses Data and returns deterministic bundle files. Any error is a
	// user input error (malformed payload); it never means "retry later".
	Import(in Input) (*Output, error)
}

var registry = map[string]Importer{}

// Register adds an importer to the global registry. Importers register from
// their package init functions and the CLI blank-imports them. Register panics
// on programming mistakes (empty or duplicate name) because those are build
// errors, not user errors.
func Register(imp Importer) {
	if imp == nil {
		panic("importers: Register(nil)")
	}
	name := imp.Name()
	if strings.TrimSpace(name) == "" {
		panic("importers: importer with empty name")
	}
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("importers: duplicate importer %q", name))
	}
	registry[name] = imp
}

// Lookup returns the registered importer for a source name.
func Lookup(name string) (Importer, bool) {
	imp, ok := registry[name]
	return imp, ok
}

// Names returns the registered source names in lexical order so help text and
// error messages are stable.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MarshalJSON renders v as two-space indented JSON with a trailing newline.
// encoding/json sorts map keys, so the output is deterministic.
func MarshalJSON(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, 10), nil
}

// MarshalYAML renders v with two-space indentation. yaml.v3 emits struct fields
// in declaration order and sorts map keys, so the output is deterministic.
func MarshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ResolveName applies the --name default and validation shared by importers
// whose output file is named by the user (prometheus, loki). The output
// extension is stripped first so --name memory.json yields metrics/memory.json
// rather than metrics/memory.json.json.
func ResolveName(in Input, defaultName, extension string) (string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = defaultName
	}
	if extension != "" {
		name = strings.TrimSuffix(name, extension)
	}
	switch {
	case name == "":
		return "", fmt.Errorf("--name must not be empty")
	case name == "." || name == "..":
		return "", fmt.Errorf("invalid --name %q", name)
	case strings.ContainsAny(name, "/\\"):
		return "", fmt.Errorf("invalid --name %q: must not contain path separators", name)
	case strings.HasPrefix(name, "."):
		return "", fmt.Errorf("invalid --name %q: must not start with a dot", name)
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f {
			return "", fmt.Errorf("invalid --name %q: must not contain spaces or control characters", name)
		}
	}
	return name, nil
}
