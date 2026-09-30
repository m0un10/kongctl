// Package patch applies decK patch files (the `deck file patch` format) to a
// JSON-shaped configuration map, using Kong's go-apiops implementation so the
// semantics are identical: each key under `values` replaces the matched key
// wholesale (maps and arrays alike, no deep merge), `remove` deletes keys, and
// later patches win.
package patch

import (
	"fmt"

	"github.com/kong/go-apiops/deckformat"
	"github.com/kong/go-apiops/jsonbasics"
	apiopspatch "github.com/kong/go-apiops/patch"

	"github.com/m0un10/kongctl/internal/yamlx"
)

// Source is one patch file.
type Source struct {
	Name string
	Data []byte
}

// Parse reads a patch file into its entries. Entries without `values` or
// `remove` are ignored, as decK does.
func Parse(src Source) ([]apiopspatch.DeckPatch, error) {
	data, err := yamlx.Deserialize(src.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name, err)
	}
	if data[deckformat.VersionKey] != nil {
		if _, _, err := deckformat.ParseFormatVersion(data); err != nil {
			return nil, fmt.Errorf("%s: invalid %s: %w", src.Name, deckformat.VersionKey, err)
		}
	}
	entries, err := jsonbasics.GetObjectArrayField(data, "patches")
	if err != nil {
		return nil, fmt.Errorf("%s: field 'patches' is not an array: %w", src.Name, err)
	}
	var out []apiopspatch.DeckPatch
	for i, entry := range entries {
		if entry["values"] == nil && entry["remove"] == nil {
			continue
		}
		var p apiopspatch.DeckPatch
		if err := p.Parse(entry, fmt.Sprintf("%s: patches[%d]", src.Name, i)); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Apply applies every patch in every source, in order, and returns the new map.
func Apply(data map[string]any, sources []Source) (map[string]any, error) {
	if len(sources) == 0 {
		return data, nil
	}
	node := jsonbasics.ConvertToYamlNode(data)
	for _, src := range sources {
		patches, err := Parse(src)
		if err != nil {
			return nil, err
		}
		for i, p := range patches {
			if err := p.ApplyToNodes(node); err != nil {
				return nil, fmt.Errorf("%s: failed to apply patch %d: %w", src.Name, i, err)
			}
		}
	}
	return jsonbasics.ConvertToJSONobject(node), nil
}
