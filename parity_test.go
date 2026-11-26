package calldataverifier

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type parityError struct {
	Type   string
	Field  string
	CallID *string
}

type parityCall struct {
	Call         CalldataCall
	FunctionName string
	Args         []any
	Calls        []parityCall
}

type parityVector struct {
	Name          string
	Intent        json.RawMessage
	Calls         json.RawMessage
	Proof         json.RawMessage
	Error         *parityError
	Decoded       []parityCall
	JSONTypeError *string
	HasCause      bool
}

func parityCalls(calls []DecodedCalldataCall) []parityCall {
	result := make([]parityCall, len(calls))
	for i, call := range calls {
		args := make([]any, len(call.Args))
		for j, arg := range call.Args {
			args[j] = parityArg(reflect.ValueOf(arg))
		}
		result[i] = parityCall{call.Call, call.FunctionName, args, parityCalls(call.Calls)}
	}
	return result
}

func parityArg(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	if n, ok := value.Interface().(*big.Int); ok {
		return n.String()
	}
	switch value.Kind() {
	case reflect.Array, reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, value.Len())
			for i := range b {
				b[i] = byte(value.Index(i).Uint())
			}
			return "0x" + hex.EncodeToString(b)
		}
		a := make([]any, value.Len())
		for i := range a {
			a[i] = parityArg(value.Index(i))
		}
		return a
	case reflect.Struct:
		obj := make(map[string]any, value.NumField())
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			key := field.Tag.Get("json")
			if key == "" {
				key = field.Name
			}
			obj[strings.ToLower(key)] = parityArg(value.Field(i))
		}
		return obj
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10)
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	default:
		return value.Interface()
	}
}

var parityReference = regexp.MustCompile(`"\$p([0-9]+)"`)

func TestParityFixturePins(t *testing.T) {
	generator, err := os.ReadFile("generate-snapshot.ts")
	if err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`(?m)^const revision = "([a-f0-9]{40})";\r?$`).FindSubmatch(generator)
	if len(pin) != 2 || string(pin[1]) != vectorRevision {
		t.Fatal("generator and Go vector pins differ")
	}
	var snapshot struct{ Revision string }
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != vectorRevision {
		t.Fatal("SDK snapshot revision differs from the generator pin")
	}
	files, err := filepath.Glob("testdata/ts-parity-*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("expected parity fixtures: %v", err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct{ Revision string }
		if err := json.Unmarshal(raw, &fixture); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if fixture.Revision != vectorRevision {
			t.Fatalf("%s is not generated at %s; run generate-parity.py", file, vectorRevision)
		}
	}
}

func runParity(t *testing.T, family string, intent func([]byte) (CalldataIntent, error), verify func(CalldataIntent, []CalldataCall, json.RawMessage) ([]DecodedCalldataCall, error)) {
	t.Helper()
	data, err := os.ReadFile("testdata/ts-parity-" + family + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Revision string
		Strings  []json.RawMessage
		Cases    []json.RawMessage
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Revision != vectorRevision || len(fixture.Cases) == 0 {
		t.Fatal("invalid parity fixture provenance or empty cases")
	}
	for _, rawCase := range fixture.Cases {
		expanded := parityReference.ReplaceAllFunc(rawCase, func(ref []byte) []byte {
			i, err := strconv.Atoi(string(ref[3 : len(ref)-1]))
			if err != nil || i >= len(fixture.Strings) || !bytes.HasPrefix(fixture.Strings[i], []byte{'"'}) {
				t.Fatalf("invalid parity fixture string reference %s", ref)
			}
			return fixture.Strings[i]
		})
		var vector parityVector
		if err := json.Unmarshal(expanded, &vector); err != nil {
			t.Fatal(err)
		}
		t.Run(vector.Name, func(t *testing.T) {
			request, err := intent(vector.Intent)
			var calls []CalldataCall
			if err == nil {
				err = json.Unmarshal(vector.Calls, &calls)
			}
			var decoded []DecodedCalldataCall
			if err == nil {
				if verify == nil {
					if len(vector.Proof) != 0 {
						t.Fatal("proof fixture must run through the proof verifier")
					}
					decoded, err = VerifyCalldata(request, calls)
				} else {
					decoded, err = verify(request, calls, vector.Proof)
				}
			}
			if field := vector.JSONTypeError; field != nil {
				var mismatch *json.UnmarshalTypeError
				if !errors.As(err, &mismatch) {
					t.Fatalf("want JSON type error on %q, got %v", *field, err)
				}
				got := mismatch.Field
				path := strings.Split(got, ".")
				last := path[len(path)-1]
				if _, err := strconv.Atoi(last); err == nil && len(path) > 1 {
					last = path[len(path)-2]
				}
				if !strings.EqualFold(last, *field) {
					t.Fatalf("want JSON type error on %q, got %v", *field, err)
				}
				return
			}
			if vector.Error == nil {
				if err != nil {
					t.Fatalf("expected success, got %T %v", err, err)
				}
				if !reflect.DeepEqual(parityCalls(decoded), vector.Decoded) {
					t.Fatal("decoded call tree or ABI arguments differ")
				}
				return
			}
			var typed checkError
			if !errors.As(err, &typed) {
				t.Fatalf("expected %+v, got %T %v", *vector.Error, err, err)
			}
			d := typed.location()
			want := vector.Error
			if reflect.TypeOf(typed).Elem().Name() != want.Type || d.Field != want.Field || !reflect.DeepEqual(d.CallID, want.CallID) {
				t.Fatalf("expected %s %s id=%s, got %T %s id=%s", want.Type, want.Field, showID(want.CallID), err, d.Field, showID(d.CallID))
			}
			if vector.HasCause && errors.Unwrap(err) == nil {
				t.Fatal("expected an underlying error")
			}
			var nestedDecode *CalldataDecodeError
			if want.Type == "CalldataDecodeError" && errors.As(errors.Unwrap(err), &nestedDecode) {
				t.Fatal("decode error wraps another decode error")
			}
		})
	}
}

func showID(id *string) string {
	if id == nil {
		return "(none)"
	}
	return *id
}

func parityIntent[T CalldataIntent](raw []byte) (CalldataIntent, error) {
	var intent T
	err := json.Unmarshal(raw, &intent)
	return intent, err
}

func TestParityVault(t *testing.T) {
	runParity(t, "vault", parityIntent[VaultCalldataIntent], nil)
}
