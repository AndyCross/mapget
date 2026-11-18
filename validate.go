package mapget

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// rules are the validation tags on one field. The tag names are the JSON
// Schema keywords, so the same tags describe the field in the OpenAPI
// document:
//
//	minimum, maximum      numbers
//	minLength, maxLength  strings, counted in characters
//	pattern               strings, a regular expression
//	enum                  strings and integers, comma separated
//	minItems, maxItems    slices
//
// On a slice field the first four apply to each element.
type rules struct {
	hasMin, hasMax bool
	min, max       float64
	minText        string // the bounds as written, for messages
	maxText        string
	minLen, maxLen int // -1 when unset
	pattern        *regexp.Regexp
	enum           []string
	minItems       int // -1 when unset
	maxItems       int
	numeric        bool
}

var ruleTags = []string{"minimum", "maximum", "minLength", "maxLength", "pattern", "enum", "minItems", "maxItems"}

func isNumeric(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func isInteger(k reflect.Kind) bool {
	return isNumeric(k) && k != reflect.Float32 && k != reflect.Float64
}

// parseRules reads the validation tags of a field whose values have type t,
// or whose elements do when slice is true. It returns nil when there are
// none, and an error when a tag does not suit the type or does not parse.
func parseRules(tag reflect.StructTag, t reflect.Type, slice bool) (*rules, error) {
	r := &rules{minLen: -1, maxLen: -1, minItems: -1, maxItems: -1}
	found := false
	for _, name := range ruleTags {
		val, ok := tag.Lookup(name)
		if !ok {
			continue
		}
		found = true
		kind := t.Kind()
		number := isNumeric(kind) && t != durationType
		count := func() (int, error) {
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return 0, fmt.Errorf(`%s:"%s" is not a whole number`, name, val)
			}
			return n, nil
		}
		var err error
		switch name {
		case "minimum", "maximum":
			if !number {
				return nil, fmt.Errorf("%s applies to numbers, not %s", name, t)
			}
			f, perr := strconv.ParseFloat(val, 64)
			if perr != nil {
				return nil, fmt.Errorf(`%s:"%s" is not a number`, name, val)
			}
			if name == "minimum" {
				r.hasMin, r.min, r.minText = true, f, val
			} else {
				r.hasMax, r.max, r.maxText = true, f, val
			}
		case "minLength", "maxLength":
			if kind != reflect.String {
				return nil, fmt.Errorf("%s applies to strings, not %s", name, t)
			}
			if name == "minLength" {
				r.minLen, err = count()
			} else {
				r.maxLen, err = count()
			}
		case "pattern":
			if kind != reflect.String {
				return nil, fmt.Errorf("pattern applies to strings, not %s", t)
			}
			r.pattern, err = regexp.Compile(val)
			if err != nil {
				return nil, fmt.Errorf(`pattern:"%s" is not a regular expression: %v`, val, err)
			}
		case "enum":
			if kind != reflect.String && !(isInteger(kind) && t != durationType) {
				return nil, fmt.Errorf("enum applies to strings and integers, not %s", t)
			}
			for _, e := range strings.Split(val, ",") {
				e = strings.TrimSpace(e)
				if isInteger(kind) {
					if _, perr := strconv.ParseInt(e, 10, 64); perr != nil {
						return nil, fmt.Errorf(`enum value "%s" is not an integer`, e)
					}
				}
				r.enum = append(r.enum, e)
			}
		case "minItems", "maxItems":
			if !slice {
				return nil, fmt.Errorf("%s applies to slices, not %s", name, t)
			}
			if name == "minItems" {
				r.minItems, err = count()
			} else {
				r.maxItems, err = count()
			}
		}
		if err != nil {
			return nil, err
		}
		r.numeric = number
	}
	if !found {
		return nil, nil
	}
	r.numeric = isNumeric(t.Kind()) && t != durationType
	return r, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// check returns what is wrong with one value, or "".
func (r *rules) check(v reflect.Value) string {
	if r.numeric {
		var f float64
		var text string
		switch {
		case v.CanInt():
			f, text = float64(v.Int()), strconv.FormatInt(v.Int(), 10)
		case v.CanUint():
			f, text = float64(v.Uint()), strconv.FormatUint(v.Uint(), 10)
		default:
			f = v.Float()
		}
		switch {
		case r.hasMin && f < r.min:
			return "must be at least " + r.minText
		case r.hasMax && f > r.max:
			return "must be at most " + r.maxText
		}
		return r.checkEnum(text)
	}
	s := v.String()
	if r.minLen >= 0 || r.maxLen >= 0 {
		n := utf8.RuneCountInString(s)
		switch {
		case r.minLen >= 0 && n < r.minLen:
			return "must be at least " + plural(r.minLen, "character")
		case r.maxLen >= 0 && n > r.maxLen:
			return "must be at most " + plural(r.maxLen, "character")
		}
	}
	if r.pattern != nil && !r.pattern.MatchString(s) {
		return "must match the pattern " + r.pattern.String()
	}
	return r.checkEnum(s)
}

func (r *rules) checkEnum(s string) string {
	if r.enum == nil {
		return ""
	}
	for _, e := range r.enum {
		if e == s {
			return ""
		}
	}
	return "must be one of " + strings.Join(r.enum, ", ")
}

// checkItems returns what is wrong with a slice's length, or "".
func (r *rules) checkItems(n int) string {
	switch {
	case r.minItems >= 0 && n < r.minItems:
		return "must have at least " + plural(r.minItems, "item")
	case r.maxItems >= 0 && n > r.maxItems:
		return "must have at most " + plural(r.maxItems, "item")
	}
	return ""
}

func (r *rules) hasScalar() bool {
	return r.hasMin || r.hasMax || r.minLen >= 0 || r.maxLen >= 0 || r.pattern != nil || r.enum != nil
}

// describe writes the rules into a JSON Schema: the scalar keywords into
// scalar, and the item counts into array when the field is a slice.
func (r *rules) describe(scalar, array map[string]any) {
	if r == nil {
		return
	}
	if r.hasMin {
		scalar["minimum"] = r.min
	}
	if r.hasMax {
		scalar["maximum"] = r.max
	}
	if r.minLen >= 0 {
		scalar["minLength"] = r.minLen
	}
	if r.maxLen >= 0 {
		scalar["maxLength"] = r.maxLen
	}
	if r.pattern != nil {
		scalar["pattern"] = r.pattern.String()
	}
	if r.enum != nil {
		vals := make([]any, len(r.enum))
		for i, e := range r.enum {
			vals[i] = e
			if r.numeric {
				n, _ := strconv.ParseInt(e, 10, 64)
				vals[i] = n
			}
		}
		scalar["enum"] = vals
	}
	if array != nil {
		if r.minItems >= 0 {
			array["minItems"] = r.minItems
		}
		if r.maxItems >= 0 {
			array["maxItems"] = r.maxItems
		}
	}
}

// fieldShape takes the pointer and slice off a field's type, to find the
// type its validation tags apply to.
func fieldShape(t reflect.Type) (scalar reflect.Type, slice bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 {
		t, slice = t.Elem(), true
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
	}
	return t, slice
}

// A bodyType is how to walk a decoded JSON body looking for values to check.
type bodyType struct {
	fields []bodyField // for a struct
	elem   *bodyType   // for a pointer, slice, array or map
}

type bodyField struct {
	index int
	name  string // the JSON name; empty for an embedded struct, whose fields are promoted
	omit  bool
	rules *rules
	slice bool
	child *bodyType
}

var (
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
)

// bodyTypeFor builds the walk for a body type. It returns nil when nothing
// in the type has a validation tag, so most bodies cost nothing to check.
func bodyTypeFor(t reflect.Type) (*bodyType, error) {
	found := false
	bt, err := buildBodyType(t, map[reflect.Type]*bodyType{}, &found)
	if err != nil || !found {
		return nil, err
	}
	return bt, nil
}

func buildBodyType(t reflect.Type, seen map[reflect.Type]*bodyType, found *bool) (*bodyType, error) {
	if bt, ok := seen[t]; ok {
		return bt, nil
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		bt := &bodyType{}
		seen[t] = bt
		elem, err := buildBodyType(t.Elem(), seen, found)
		bt.elem = elem
		return bt, err
	case reflect.Struct:
		if reflect.PointerTo(t).Implements(jsonUnmarshalerType) || reflect.PointerTo(t).Implements(textUnmarshalerType) {
			return nil, nil
		}
		bt := &bodyType{}
		seen[t] = bt
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			tag := sf.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			f := bodyField{index: i, name: name}
			f.omit = strings.Contains(","+opts+",", ",omitempty,") || strings.Contains(","+opts+",", ",omitzero,")
			scalar, slice := fieldShape(sf.Type)
			if sf.Anonymous && name == "" && scalar.Kind() == reflect.Struct && !slice {
				child, err := buildBodyType(sf.Type, seen, found)
				if err != nil {
					return nil, err
				}
				f.child = child
				bt.fields = append(bt.fields, f)
				continue
			}
			if !sf.IsExported() {
				continue
			}
			if name == "" {
				f.name = sf.Name
			}
			r, err := parseRules(sf.Tag, scalar, slice)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", t, sf.Name, err)
			}
			if r != nil {
				*found = true
				f.rules, f.slice = r, slice
			}
			child, err := buildBodyType(sf.Type, seen, found)
			if err != nil {
				return nil, err
			}
			f.child = child
			bt.fields = append(bt.fields, f)
		}
		return bt, nil
	}
	return nil, nil
}

// walk checks v and everything in it, reporting each failure under its JSON
// path, such as "lines[2].qty".
func (bt *bodyType) walk(v reflect.Value, path string, fail func(name, msg string)) {
	if bt == nil {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			bt.elem.walk(v.Elem(), path, fail)
		}
	case reflect.Slice, reflect.Array:
		if bt.elem == nil {
			return
		}
		for i := 0; i < v.Len(); i++ {
			bt.elem.walk(v.Index(i), path+"["+strconv.Itoa(i)+"]", fail)
		}
	case reflect.Map:
		if bt.elem == nil {
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			bt.elem.walk(iter.Value(), join(path, fmt.Sprint(iter.Key().Interface())), fail)
		}
	case reflect.Struct:
		for i := range bt.fields {
			f := &bt.fields[i]
			fv := v.Field(f.index)
			if f.name == "" {
				f.child.walk(fv, path, fail)
				continue
			}
			if f.omit && fv.IsZero() {
				continue // left out of the JSON, so there is nothing to check
			}
			p := join(path, f.name)
			if f.rules != nil {
				f.rules.apply(fv, f.slice, p, fail)
			}
			f.child.walk(fv, p, fail)
		}
	}
}

// apply checks one field's value: its length if it is a slice, then each
// element, or the value itself.
func (r *rules) apply(v reflect.Value, slice bool, name string, fail func(name, msg string)) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if !slice {
		if msg := r.check(v); msg != "" {
			fail(name, msg)
		}
		return
	}
	if msg := r.checkItems(v.Len()); msg != "" {
		fail(name, msg)
	}
	if !r.hasScalar() {
		return
	}
	for i := 0; i < v.Len(); i++ {
		e := v.Index(i)
		for e.Kind() == reflect.Pointer && !e.IsNil() {
			e = e.Elem()
		}
		if e.Kind() == reflect.Pointer {
			continue
		}
		if msg := r.check(e); msg != "" {
			fail(name+"["+strconv.Itoa(i)+"]", msg)
		}
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
