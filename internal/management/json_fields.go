package management

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
)

var jsonUnmarshaler = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
var jsonFields sync.Map

// encoding/json matches struct fields case-insensitively. That is too permissive
// for authorization policy: Accept must not alias accept or defeat duplicate checks.
func exactFields(b []byte, destination any) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if e := decoder.Decode(&value); e != nil {
		return e
	}
	var check func(any, reflect.Type) error
	check = func(value any, t reflect.Type) error {
		for t != nil && t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t == nil {
			return nil
		}
		if reflect.PointerTo(t).Implements(jsonUnmarshaler) {
			return nil
		}
		switch t.Kind() {
		case reflect.Struct:
			object, ok := value.(map[string]any)
			if !ok {
				return nil
			}
			cached, ok := jsonFields.Load(t)
			if !ok {
				fields := map[string]reflect.Type{}
				for i := 0; i < t.NumField(); i++ {
					field := t.Field(i)
					if !field.IsExported() {
						continue
					}
					name := strings.Split(field.Tag.Get("json"), ",")[0]
					if name == "-" {
						continue
					}
					if name == "" {
						name = field.Name
					}
					fields[name] = field.Type
				}
				cached, _ = jsonFields.LoadOrStore(t, fields)
			}
			fields := cached.(map[string]reflect.Type)
			for key, v := range object {
				field, ok := fields[key]
				if !ok {
					return errors.New("management_unknown_field")
				}
				if e := check(v, field); e != nil {
					return e
				}
			}
		case reflect.Slice, reflect.Array:
			if values, ok := value.([]any); ok {
				for _, v := range values {
					if e := check(v, t.Elem()); e != nil {
						return e
					}
				}
			}
		case reflect.Map:
			if values, ok := value.(map[string]any); ok {
				for _, v := range values {
					if e := check(v, t.Elem()); e != nil {
						return e
					}
				}
			}
		}
		return nil
	}
	return check(value, reflect.TypeOf(destination))
}
