package imports

import "encoding/json"

// The job row stores rows and errors as JSON documents; these helpers convert typed values to and
// from the map shapes Ent stores.

func toMaps[T any](v []T) []map[string]any {
	out := []map[string]any{}
	b, err := json.Marshal(v)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func decodeRows(raw any) ([]Row, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var rows []Row
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
