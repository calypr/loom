package main

import (
	"fmt"
	"regexp"
	"strings"
)

type selector struct {
	kind  string
	value string
	re    *regexp.Regexp
	list  map[string]bool
}

func parseSelector(s string) (*selector, error) {
	kind, value, ok := strings.Cut(s, ":")
	if !ok {
		return nil, fmt.Errorf("selector must be file:<re>, name:<re>, prefix:<token>, or list:A,B")
	}
	sel := &selector{kind: kind, value: value}
	switch kind {
	case "file", "name":
		re, err := regexp.Compile(value)
		if err != nil {
			return nil, err
		}
		sel.re = re
	case "prefix":
		if value == "" {
			return nil, fmt.Errorf("empty prefix")
		}
	case "list":
		sel.list = map[string]bool{}
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				sel.list[item] = true
			}
		}
		if len(sel.list) == 0 {
			return nil, fmt.Errorf("empty list")
		}
	default:
		return nil, fmt.Errorf("unknown selector kind %q", kind)
	}
	return sel, nil
}

func (s *selector) matchesFile(base string) bool {
	if s == nil || s.re == nil {
		return false
	}
	stem := strings.TrimSuffix(base, ".go")
	return s.re.MatchString(base) || s.re.MatchString(stem)
}

func (s *selector) matchesName(name string) bool {
	if s == nil {
		return false
	}
	switch s.kind {
	case "name":
		return s.re.MatchString(name)
	case "list":
		if s.list[name] {
			return true
		}
		// list items may name a method as Recv.Method or just Method.
		if i := strings.LastIndex(name, "."); i >= 0 && s.list[name[i+1:]] {
			return true
		}
		return false
	}
	return false
}

func prefixToken(base string) string {
	stem := strings.TrimSuffix(base, ".go")
	parts := strings.Split(stem, "_")
	return parts[0]
}
