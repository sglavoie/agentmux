package transcript

import (
	"encoding/json"
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
		if field != "entries" {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
			continue
		}
		if err := i.decodeEntries(dec); err != nil {
			return err
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
		if meta.SearchText != "" {
			if err := i.WriteSearchText(key, meta.SearchText); err != nil {
				return err
			}
			meta.SearchText = ""
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
