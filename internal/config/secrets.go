// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"go.yaml.in/yaml/v3"
)

// SecretsName is the file next to config.yaml that holds the channels'
// secrets, by channel name; SystemSecrets is root's, next to SystemFile.
const (
	SecretsName   = "secrets.yaml"
	SystemSecrets = "/etc/fstui/" + SecretsName
)

// Secrets are the channels' passwords and tokens, by channel name.
type Secrets map[string]string

// ErrNoSecret is returned for a channel that has none.
var ErrNoSecret = errors.New("no secret")

// SecretsFiles are the secrets files that go with the config files: root's
// in /etc/fstui, then the one next to the user's config file.
func SecretsFiles(userFile string) []string {
	var out []string
	if os.Geteuid() == rootUID {
		out = append(out, SystemSecrets)
	}
	if userFile != "" {
		out = append(out, filepath.Join(filepath.Dir(userFile), SecretsName))
	}
	return out
}

// LoadSecrets reads the secrets files, a later one overriding an earlier
// one name by name. A missing file is not a problem; one that someone
// other than the user could read or change is refused, as is a value that
// is not text.
func LoadSecrets(files ...string) (Secrets, []Problem) {
	return trust{euid: uint32(os.Geteuid()), top: "/"}.loadSecrets(files) //nolint:gosec // uids are non-negative
}

func (t trust) loadSecrets(files []string) (Secrets, []Problem) {
	out := Secrets{}
	var problems []Problem
	for _, file := range files {
		data, err := t.readWith(file, t.private)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: file, Msg: "ignored: " + err.Error()})
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			problems = append(problems, Problem{File: file, Line: yamlLine(err), Msg: "not YAML"}) // the parser's message can quote a secret
			continue
		}
		if len(doc.Content) == 0 {
			continue
		}
		problems = append(problems, secretEntries(file, doc.Content[0], out)...)
	}
	return out, problems
}

// secretEntries adds the name: secret pairs of a document to out.
func secretEntries(file string, n *yaml.Node, out Secrets) []Problem {
	if n.Kind != yaml.MappingNode {
		return []Problem{{File: file, Line: n.Line, Msg: "want channel names mapped to their secrets"}}
	}
	var problems []Problem
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if v.Kind != yaml.ScalarNode || v.Value == "" {
			problems = append(problems, Problem{File: file, Line: v.Line, Msg: k.Value + ": want the secret as text"}) // never the value itself
			continue
		}
		out[k.Value] = v.Value
	}
	return problems
}

// private requires a file only its owner, the user fstui runs as, can
// read.
func (t trust) private(path string, info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != t.euid {
		return fmt.Errorf("%s must belong to the user fstui runs as", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s can be read by group or others (mode %v); it must be 0600", path, info.Mode().Perm())
	}
	return nil
}

// Secret is ch's secret: from its secret_file if it names one, else from
// the secrets files under its name.
func (s Secrets) Secret(ch Channel) (string, error) {
	return trust{euid: uint32(os.Geteuid()), top: "/"}.secret(s, ch) //nolint:gosec // uids are non-negative
}

func (t trust) secret(s Secrets, ch Channel) (string, error) {
	if ch.SecretFile != "" {
		data, err := t.readWith(ch.SecretFile, t.private)
		if err != nil {
			return "", fmt.Errorf("secret_file: %w", err)
		}
		line, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
		if line == "" {
			return "", fmt.Errorf("secret_file %s: %w", ch.SecretFile, ErrNoSecret)
		}
		return line, nil
	}
	if v, ok := s[ch.Name]; ok {
		return v, nil
	}
	return "", ErrNoSecret
}

// yamlErrorLine finds the line number in a YAML parser error.
var yamlErrorLine = regexp.MustCompile(`line (\d+)`)

// yamlLine is the line a YAML error is on, or 0.
func yamlLine(err error) int {
	m := yamlErrorLine.FindStringSubmatch(err.Error())
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
