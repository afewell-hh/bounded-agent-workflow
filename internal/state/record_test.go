package state

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
)

const (
	tID     = "00112233445566778899aabbccddeeff"
	tTicket = "https://github.com/afewell-hh/bounded-agent-workflow/issues/9"
	tScope  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tPol1   = "1111111111111111111111111111111111111111"
	tHead1  = "2222222222222222222222222222222222222222"
	tPol256 = "3333333333333333333333333333333333333333333333333333333333333333"
	tHd256  = "4444444444444444444444444444444444444444444444444444444444444444"
	tTime   = "2026-10-01T12:34:56Z"
)

// field is one hand-authored record member: raw JSON name and value.
type field struct{ k, v string }

// fields1 is the independently written valid SHA-1 record, in contract order.
func fields1() []field {
	return []field{
		{"schema_version", `1`},
		{"run_id", `"` + tID + `"`},
		{"record_state", `"recorded"`},
		{"ticket_url", `"` + tTicket + `"`},
		{"scope_sha256", `"` + tScope + `"`},
		{"policy_commit", `"` + tPol1 + `"`},
		{"repository_object_format", `"sha1"`},
		{"repository_head", `"` + tHead1 + `"`},
		{"created_at", `"` + tTime + `"`},
	}
}

func obj(fs []field) string {
	var parts []string
	for _, f := range fs {
		parts = append(parts, `"`+f.k+`":`+f.v)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func set(fs []field, k, v string) []field {
	out := append([]field(nil), fs...)
	for i := range out {
		if out[i].k == k {
			out[i].v = v
			return out
		}
	}
	return append(out, field{k, v})
}

func drop(fs []field, k string) []field {
	var out []field
	for _, f := range fs {
		if f.k != k {
			out = append(out, f)
		}
	}
	return out
}

func codeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "not-a-state-error"
}

func TestParseValid(t *testing.T) {
	r, err := Parse([]byte(obj(fields1())+"\n"), tID)
	if err != nil {
		t.Fatal(err)
	}
	want := Record{1, tID, "recorded", tTicket, tScope, tPol1, "sha1", tHead1, tTime}
	if r != want {
		t.Fatalf("got %+v", r)
	}
	s256 := set(set(set(fields1(), "policy_commit", `"`+tPol256+`"`), "repository_head", `"`+tHd256+`"`), "repository_object_format", `"sha256"`)
	if _, err := Parse([]byte(obj(s256)), tID); err != nil {
		t.Fatal(err)
	}
	// Marshal output must carry exactly the nine hand-listed names.
	data, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	nine := []string{"created_at", "policy_commit", "record_state", "repository_head", "repository_object_format", "run_id", "schema_version", "scope_sha256", "ticket_url"}
	if strings.Join(keys, ",") != strings.Join(nine, ",") {
		t.Fatalf("keys %v", keys)
	}
}

func TestParseBoundary(t *testing.T) {
	body := obj(fields1())
	pad := MaxRecordBytes - len(body)
	exact := strings.Repeat(" ", pad/2) + body + strings.Repeat("\n\t \r", 0) + strings.Repeat("\n", pad-pad/2)
	if len(exact) != 16384 {
		t.Fatalf("fixture length %d", len(exact))
	}
	if _, err := Parse([]byte(exact), tID); err != nil {
		t.Fatalf("16384-byte whitespace-padded record rejected: %v", err)
	}
	if _, err := Parse([]byte(exact+" "), tID); codeOf(err) != "record_too_large" {
		t.Fatalf("16385 bytes: %v", err)
	}
}

func TestParseInvalid(t *testing.T) {
	f := fields1()
	huge := strings.Repeat("9", 5000)
	cases := []struct {
		name, data string
		want       Code
	}{
		{"invalid utf8", strings.Replace(obj(f), "recorded", "record\xff", 1), "invalid_record"},
		{"empty", "", "invalid_record"},
		{"array", "[" + obj(f) + "]", "invalid_record"},
		{"trailing object", obj(f) + obj(f), "invalid_record"},
		{"trailing value", obj(f) + " 1", "invalid_record"},
		{"truncated", obj(f)[:50], "invalid_record"},
		{"trailing comma", strings.TrimSuffix(obj(f), "}") + ",}", "invalid_record"},
		{"duplicate key", obj(append(f, field{"run_id", `"` + tID + `"`})), "invalid_record"},
		{"duplicate escaped key", obj(append(f, field{`run_id`, `"` + tID + `"`})), "invalid_record"},
		{"duplicate escaped key v2", obj(append(set(f, "schema_version", "2"), field{`schema_version`, "2"})), "invalid_record"},
		{"case variant key", obj(append(drop(f, "run_id"), field{"Run_ID", `"` + tID + `"`})), "invalid_record"},
		{"missing version", obj(drop(f, "schema_version")), "invalid_record"},
		{"version quoted", obj(set(f, "schema_version", `"1"`)), "invalid_record"},
		{"version 1.0", obj(set(f, "schema_version", `1.0`)), "invalid_record"},
		{"version 1e0", obj(set(f, "schema_version", `1e0`)), "invalid_record"},
		{"version null", obj(set(f, "schema_version", `null`)), "invalid_record"},
		{"version true", obj(set(f, "schema_version", `true`)), "invalid_record"},
		{"version 0", obj(set(f, "schema_version", `0`)), "invalid_record"},
		{"version negative", obj(set(f, "schema_version", `-1`)), "invalid_record"},
		{"version leading zero", obj(set(f, "schema_version", `01`)), "invalid_record"},
		{"version 2", obj(set(f, "schema_version", `2`)), "unsupported_record_version"},
		{"huge lexical version", obj(set(f, "schema_version", huge)), "unsupported_record_version"},
		{"version 2 extra field", obj(append(set(f, "schema_version", `2`), field{"extra", `1`})), "unsupported_record_version"},
		{"version 2 missing field", obj(drop(set(f, "schema_version", `2`), "run_id")), "unsupported_record_version"},
		{"version 2 duplicate", obj(append(set(f, "schema_version", `2`), field{"run_id", `"x"`})), "invalid_record"},
		{"extra field", obj(append(f, field{"extra", `"x"`})), "invalid_record"},
		{"missing field", obj(drop(f, "created_at")), "invalid_record"},
		{"null field", obj(set(f, "ticket_url", `null`)), "invalid_record"},
		{"wrong type", obj(set(f, "record_state", `1`)), "invalid_record"},
		{"other state", obj(set(f, "record_state", `"completed"`)), "invalid_record"},
		{"mismatched run id", obj(set(f, "run_id", `"ffeeddccbbaa99887766554433221100"`)), "invalid_record"},
		{"uppercase scope", obj(set(f, "scope_sha256", `"`+strings.ToUpper(tScope)+`"`)), "invalid_record"},
		{"uppercase head", obj(set(f, "repository_head", `"`+strings.ToUpper("abcdef")+tHead1[6:]+`"`)), "invalid_record"},
		{"policy width mismatch", obj(set(f, "policy_commit", `"`+tPol256+`"`)), "invalid_record"},
		{"head width mismatch", obj(set(f, "repository_head", `"`+tHd256+`"`)), "invalid_record"},
		{"bad format", obj(set(f, "repository_object_format", `"md5"`)), "invalid_record"},
		{"bad ticket", obj(set(f, "ticket_url", `"https://github.com/a/b/issues/1?x=1"`)), "invalid_record"},
		{"ticket trailing slash", obj(set(f, "ticket_url", `"`+tTicket+`/"`)), "invalid_record"},
		{"time fraction", obj(set(f, "created_at", `"2026-10-01T12:34:56.5Z"`)), "invalid_record"},
		{"time offset", obj(set(f, "created_at", `"2026-10-01T12:34:56+00:00"`)), "invalid_record"},
		{"time lowercase z", obj(set(f, "created_at", `"2026-10-01T12:34:56z"`)), "invalid_record"},
		{"time impossible date", obj(set(f, "created_at", `"2026-02-30T12:34:56Z"`)), "invalid_record"},
		{"time hour 24", obj(set(f, "created_at", `"2026-10-01T24:00:00Z"`)), "invalid_record"},
		{"time leap second", obj(set(f, "created_at", `"2026-12-31T23:59:60Z"`)), "invalid_record"},
		{"version 1 duplicate plus extra", obj(append(append(f, field{"extra", "1"}), field{"extra", "1"})), "invalid_record"},
	}
	for _, c := range cases {
		r, err := Parse([]byte(c.data), tID)
		if codeOf(err) != c.want {
			t.Errorf("%s: got %v want %s", c.name, err, c.want)
		}
		if r != (Record{}) {
			t.Errorf("%s: partial record returned", c.name)
		}
	}
}

// Malformed content is never echoed through error text.
func TestParseErrorsWithholdContent(t *testing.T) {
	secret := "sparrow-indigo-6061-recordsecret\x1b[31m"
	for _, data := range []string{
		obj(set(fields1(), "ticket_url", `"`+secret+`"`)),
		`{"` + secret + `":1}`,
		secret,
	} {
		_, err := Parse([]byte(data), tID)
		if err == nil || strings.Contains(err.Error(), "sparrow") || strings.Contains(err.Error(), "\x1b") {
			t.Fatalf("error %v", err)
		}
	}
}

func TestValidators(t *testing.T) {
	for _, u := range []string{tTicket, "https://github.com/a/b.c_d-e/issues/2147483647"} {
		if !ValidTicketURL(u) {
			t.Errorf("rejected %q", u)
		}
	}
	for _, u := range []string{
		"http://github.com/a/b/issues/1", "https://github.com:443/a/b/issues/1", "https://u@github.com/a/b/issues/1",
		"https://github.com/a/b/issues/1#x", "https://github.com/a/b/issues/01", "https://github.com/a/b/issues/0",
		"https://github.com/a/b/pull/1", "https://github.com/a/../issues/1", "https://github.com/-a/b/issues/1",
		"https://GitHub.com/a/b/issues/1", "https://github.com/a/b/issues/2147483648", "https://github.com/a/b/issues/1/",
	} {
		if ValidTicketURL(u) {
			t.Errorf("accepted %q", u)
		}
	}
	for _, id := range []string{"", tID + "0", strings.ToUpper(tID), "../" + tID[3:], tID[:31] + "g", tID[:31] + "/"} {
		if ValidRunID(id) {
			t.Errorf("accepted id %q", id)
		}
	}
}
