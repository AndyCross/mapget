package mapget

import (
	"fmt"
	"strings"
)

// A segment is one slash-separated part of a pattern's path.
type segment struct {
	lit   string // literal text, as written
	name  string // wildcard name, for {name} and {name...}
	wild  bool   // {name} or {name...}
	multi bool   // {name...}, or the anonymous wildcard of a trailing slash
	end   bool   // {$}
}

// A pattern is a net/http ServeMux pattern, [METHOD ][HOST]/[PATH], taken apart.
type pattern struct {
	method string
	host   string
	segs   []segment
}

func parsePattern(s string) (pattern, error) {
	var p pattern
	rest := s
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		p.method = s[:i]
		rest = strings.TrimLeft(s[i+1:], " \t")
	}
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return p, fmt.Errorf("pattern %q has no path", s)
	}
	p.host = rest[:i]
	segs, err := parsePath(rest[i:])
	if err != nil {
		return p, fmt.Errorf("pattern %q: %w", s, err)
	}
	p.segs = segs
	return p, nil
}

func parsePath(path string) ([]segment, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("path %q does not start with a slash", path)
	}
	parts := strings.Split(path[1:], "/")
	segs := make([]segment, 0, len(parts))
	seen := map[string]bool{}
	for i, part := range parts {
		last := i == len(parts)-1
		switch {
		case part == "" && last:
			segs = append(segs, segment{multi: true})
		case part == "":
			return nil, fmt.Errorf("empty segment in %q", path)
		case part == "{$}":
			if !last {
				return nil, fmt.Errorf("{$} must end the path in %q", path)
			}
			segs = append(segs, segment{end: true})
		case strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}"):
			name := part[1 : len(part)-1]
			multi := strings.HasSuffix(name, "...")
			name = strings.TrimSuffix(name, "...")
			if name == "" {
				return nil, fmt.Errorf("wildcard with no name in %q", path)
			}
			if multi && !last {
				return nil, fmt.Errorf("{%s...} must end the path in %q", name, path)
			}
			if seen[name] {
				return nil, fmt.Errorf("wildcard {%s} appears twice in %q", name, path)
			}
			seen[name] = true
			segs = append(segs, segment{name: name, wild: true, multi: multi})
		case strings.ContainsAny(part, "{}"):
			return nil, fmt.Errorf("wildcard must be a whole segment: %q in %q", part, path)
		default:
			segs = append(segs, segment{lit: part})
		}
	}
	return segs, nil
}

// path is the pattern's path as net/http writes it.
func (p pattern) path() string {
	var b strings.Builder
	for _, s := range p.segs {
		b.WriteByte('/')
		switch {
		case s.end:
			b.WriteString("{$}")
		case s.wild && s.multi:
			b.WriteString("{" + s.name + "...}")
		case s.wild:
			b.WriteString("{" + s.name + "}")
		default:
			b.WriteString(s.lit)
		}
	}
	return b.String()
}

// openAPIPath is the path as OpenAPI writes it: no "..." and no {$}.
func (p pattern) openAPIPath() string {
	var b strings.Builder
	for _, s := range p.segs {
		b.WriteByte('/')
		switch {
		case s.wild:
			b.WriteString("{" + s.name + "}")
		default:
			b.WriteString(s.lit)
		}
	}
	return b.String()
}

func (p pattern) String() string {
	s := p.host + p.path()
	if p.method != "" {
		s = p.method + " " + s
	}
	return s
}

func (p pattern) wildcards() []segment {
	var w []segment
	for _, s := range p.segs {
		if s.wild {
			w = append(w, s)
		}
	}
	return w
}
