package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// UpdateResult says what UpdateFromTemplate changed.
type UpdateResult struct {
	Added                  []string // added fields, as dotted paths
	OldVersion, NewVersion int      // equal when the version didn't change
}

// UpdateFromTemplate adds the template's fields that are missing from the
// config file at path, recursing into objects. Existing fields and values
// are never changed or removed, including ones the template doesn't have.
// The one exception is "version", which is raised to the template's. When
// nothing changes, the file is left untouched.
func UpdateFromTemplate(path string, template []byte) (UpdateResult, error) {
	var cur, tmpl map[string]any
	if err := readJSON(path, &cur); err != nil {
		return UpdateResult{}, err
	}
	if err := json.Unmarshal(template, &tmpl); err != nil {
		return UpdateResult{}, fmt.Errorf("parse template: %w", err)
	}
	res := UpdateResult{OldVersion: versionOf(cur)}
	res.Added = mergeMissing(cur, tmpl, "")
	if tv := versionOf(tmpl); tv > versionOf(cur) {
		cur["version"] = tv
	}
	res.NewVersion = versionOf(cur)
	if len(res.Added) == 0 && res.NewVersion == res.OldVersion {
		return res, nil
	}
	b, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return res, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return res, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), fi.Mode().Perm()); err != nil {
		return res, err
	}
	return res, os.Rename(tmp, path)
}

// FileVersion reads the "version" field of the config file at path (0 when
// it has none).
func FileVersion(path string) (int, error) {
	var cur map[string]any
	if err := readJSON(path, &cur); err != nil {
		return 0, err
	}
	return versionOf(cur), nil
}

func versionOf(m map[string]any) int {
	switch v := m["version"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func readJSON(path string, v *map[string]any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if *v == nil {
		*v = map[string]any{}
	}
	return nil
}

// mergeMissing copies keys from src that dst lacks; where both have an
// object it recurses. Arrays and other values already in dst are kept as is.
func mergeMissing(dst, src map[string]any, prefix string) []string {
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var added []string
	for _, k := range keys {
		cur, ok := dst[k]
		if !ok {
			dst[k] = src[k]
			added = append(added, prefix+k)
			continue
		}
		a, aok := cur.(map[string]any)
		b, bok := src[k].(map[string]any)
		if aok && bok {
			added = append(added, mergeMissing(a, b, prefix+k+".")...)
		}
	}
	return added
}
