package handler

import (
	"strings"

	"github.com/buger/jsonparser"
)

// ExtractStringArray helps to extract string arrays from a json event buffer.
// It also trims quotes and backslashes from the extracted strings.
func ExtractStringArray(eventBuf []byte, eventType string, key string) ([]string, error) {
	var result []string
	value, _, _, err := jsonparser.Get(eventBuf, "events", eventType+"."+key)
	if err != nil {
		if err == jsonparser.KeyPathNotFoundError {
			return result, nil // Not found is not an error here
		}
		return nil, err
	}
	jsonparser.ArrayEach(value, func(v []byte, dt jsonparser.ValueType, offset int, err error) {
		s := strings.Trim(string(v), `\"`)
		result = append(result, s)
	})
	return result, nil
}
