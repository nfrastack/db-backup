// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package objects

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type OptionSpec struct {
	Type   string
	Values []string
}

type Objects map[string]*bool

func (m *Objects) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("objects: must be a mapping of name to true|false")
	}
	out := make(Objects, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		k := value.Content[i].Value
		v := value.Content[i+1]
		if v.Kind == yaml.ScalarNode && strings.EqualFold(strings.TrimSpace(v.Value), "unset") {
			out[k] = nil
			continue
		}
		if v.Kind == yaml.ScalarNode && (v.Value == "null" || v.Value == "~" || v.Value == "") {
			out[k] = nil
			continue
		}
		var b bool
		if err := v.Decode(&b); err != nil {
			raw := ""
			if v.Kind == yaml.ScalarNode {
				raw = v.Value
			}
			return fmt.Errorf("objects.%s: must be true or false (got %q)", k, raw)
		}
		bb := b
		out[k] = &bb
	}
	*m = out
	return nil
}

func (m Objects) Get(key string) bool {
	if v, ok := m[key]; ok && v != nil {
		return *v
	}
	return true
}

func MergeObjects(profile, job Objects) Objects {
	var out Objects
	for k, v := range profile {
		if v == nil {
			continue
		}
		if out == nil {
			out = make(Objects, len(profile)+len(job))
		}
		bb := *v
		out[k] = &bb
	}
	for k, v := range job {
		if v == nil {
			if out != nil {
				delete(out, k)
			}
			continue
		}
		if out == nil {
			out = make(Objects, len(job))
		}
		bb := *v
		out[k] = &bb
	}
	return out
}

type Options map[string]string

func (o Options) Get(key string) string {
	return o[key]
}

func MergeOptions(profile, job Options) Options {
	var out Options
	for k, v := range profile {
		if strings.EqualFold(strings.TrimSpace(v), "unset") {
			continue
		}
		if out == nil {
			out = make(Options, len(profile)+len(job))
		}
		out[k] = v
	}
	for k, v := range job {
		if strings.EqualFold(strings.TrimSpace(v), "unset") {
			if out != nil {
				delete(out, k)
			}
			continue
		}
		if out == nil {
			out = make(Options, len(job))
		}
		out[k] = v
	}
	return out
}

type Spec struct {
	Name    string
	Aliases []string
	Objects []string
	Options map[string]OptionSpec
}

var specs = map[string]Spec{}

func Register(s Spec) {
	name := strings.ToLower(strings.TrimSpace(s.Name))
	if name == "" {
		panic("objects: registered with empty name")
	}
	if _, exists := specs[name]; exists {
		panic(fmt.Sprintf("objects: duplicate registration %q", name))
	}
	s.Name = name
	specs[name] = s
	for _, a := range s.Aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || a == name {
			continue
		}
		if _, exists := specs[a]; exists {
			panic(fmt.Sprintf("objects: duplicate alias %q", a))
		}
		specs[a] = s
	}
}

func Lookup(engine string) (Spec, bool) {
	s, ok := specs[strings.ToLower(strings.TrimSpace(engine))]
	return s, ok
}

func Validate(engine string, o Objects, opts Options) error {
	s, ok := Lookup(engine)
	if !ok {
		return nil
	}
	set := make(map[string]bool, len(s.Objects))
	for _, k := range s.Objects {
		set[k] = true
	}
	for k, v := range o {
		if v == nil {
			continue
		}
		if !set[k] {
			want := append([]string{}, s.Objects...)
			sort.Strings(want)
			return fmt.Errorf("objects: unknown key %q for engine %q (want %s)", k, s.Name, strings.Join(want, "|"))
		}
	}
	for k, v := range opts {
		if strings.EqualFold(strings.TrimSpace(v), "unset") {
			continue
		}
		spec, ok := s.Options[k]
		if !ok {
			want := make([]string, 0, len(s.Options))
			for sk := range s.Options {
				want = append(want, sk)
			}
			sort.Strings(want)
			if len(want) == 0 {
				return fmt.Errorf("options: unknown key %q for engine %q (engine accepts no options)", k, s.Name)
			}
			return fmt.Errorf("options: unknown key %q for engine %q (want %s)", k, s.Name, strings.Join(want, "|"))
		}
		switch spec.Type {
		case "bool":
			if _, err := strconv.ParseBool(strings.ToLower(strings.TrimSpace(v))); err != nil {
				return fmt.Errorf("options: invalid value %q for %q (want true|false)", v, k)
			}
		case "enum":
			matched := false
			for _, allowed := range spec.Values {
				if strings.EqualFold(v, allowed) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("options: invalid value %q for %q (want %s)", v, k, strings.Join(spec.Values, "|"))
			}
		}
	}
	return nil
}
