package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
)

func (i *Index) decodeIndexFile(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("expected object start, got %v", tok)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		field, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected object field name, got %v", tok)
		}
		switch field {
		case "entries":
			if err := i.decodeEntries(dec); err != nil {
				return err
			}
		case "superseded":
			if err := dec.Decode(&i.superseded); err != nil {
				return err
			}
		default:
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
	}
	tok, err = dec.Token()
	if err != nil {
		return err
	}
	delim, ok = tok.(json.Delim)
	if !ok || delim != '}' {
		return fmt.Errorf("expected object end, got %v", tok)
	}
	return nil
}

func (i *Index) decodeEntries(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("expected entries object start, got %v", tok)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected entry key, got %v", tok)
		}
		var meta SessionMeta
		if err := dec.Decode(&meta); err != nil {
			return err
		}
		i.entries[key] = meta
	}
	tok, err = dec.Token()
	if err != nil {
		return err
	}
	delim, ok = tok.(json.Delim)
	if !ok || delim != '}' {
		return fmt.Errorf("expected entries object end, got %v", tok)
	}
	return nil
}

// migrateLegacySearchText runs only after the complete metadata document has
// parsed successfully. Keeping persistence out of the decoder ensures a
// sidecar write failure is a cache warning, never a false JSON-corruption
// signal. Failed bodies remain inline in memory for Search and a later Save.
func (i *Index) migrateLegacySearchText() error {
	var warning error
	for key, meta := range i.entries {
		if meta.SearchText == "" {
			continue
		}
		if err := i.WriteSearchText(key, meta.SearchText); err != nil {
			warning = errors.Join(warning, fmt.Errorf("migrating transcript search sidecar %s: %w", key, err))
			continue
		}
		meta.SearchText = ""
		i.entries[key] = meta
	}
	return warning
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = dec.Token()
	return err
}
