package placeholder

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// ParseEnvFile reads NAME=value lines. Blank lines and # comments are
// skipped, a leading "export " is tolerated, and matching single or double
// quotes around the value are stripped. Nothing is evaluated: this is a
// dotenv reader, not a shell.
func ParseEnvFile(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		eq := strings.IndexByte(text, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("line %d: expected NAME=value", line)
		}
		name := strings.TrimSpace(text[:eq])
		val := strings.TrimSpace(text[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		out[name] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ChainLookup returns a Lookup that consults each source in order.
func ChainLookup(sources ...Lookup) Lookup {
	return func(name string) (string, bool) {
		for _, s := range sources {
			if s == nil {
				continue
			}
			if v, ok := s(name); ok && v != "" {
				return v, true
			}
		}
		return "", false
	}
}

// MapLookup adapts a map.
func MapLookup(m map[string]string) Lookup {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}
