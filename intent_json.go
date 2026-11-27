package calldataverifier

import (
	"encoding/json"
	"reflect"
	"strings"
)

func exactJSONFields(raw json.RawMessage, typ reflect.Type) json.RawMessage {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if len(raw) == 0 {
		return raw
	}
	switch {
	case raw[0] == '{' && typ.Kind() == reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return raw
		}
		exact := make(map[string]json.RawMessage)
		collectJSONFields(fields, exact, typ)
		result, _ := json.Marshal(exact)
		return result
	case raw[0] == '[' && typ.Kind() == reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return raw
		}
		for i := range values {
			values[i] = exactJSONFields(values[i], typ.Elem())
		}
		result, _ := json.Marshal(values)
		return result
	default:
		return raw
	}
}

func collectJSONFields(raw, exact map[string]json.RawMessage, typ reflect.Type) {
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !field.IsExported() {
			continue
		}
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			collectJSONFields(raw, exact, field.Type)
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if value, ok := raw[name]; name != "" && name != "-" && ok {
			exact[name] = exactJSONFields(value, field.Type)
		}
	}
}

func decodeIntent(data []byte, dst any, common *CommonIntent, nullObjects ...string) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if vault, exists := raw["vault"]; exists {
		var address string
		common.invalidVault = json.Unmarshal(vault, &address) != nil || !nonzeroAddress(address)
	}
	if string(raw["referral_fee"]) == "null" {
		raw["referral_fee"] = json.RawMessage("{}")
	}
	for _, field := range nullObjects {
		if string(raw[field]) == "null" {
			raw[field] = json.RawMessage("{}")
		}
	}
	exact := make(map[string]json.RawMessage)
	collectJSONFields(raw, exact, reflect.TypeOf(dst).Elem())
	filtered, err := json.Marshal(exact)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(filtered, dst); err != nil {
		return nil, err
	}
	return raw, nil
}
