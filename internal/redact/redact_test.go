package redact

import (
	"maps"
	"reflect"
	"testing"
)

func TestIsSensitive_SensitiveKeys(t *testing.T) {
	sensitive := []string{
		// exact static-list entries
		"database.password",
		"ssl.keystore.key",
		"sasl.jaas.config",
		"aws.secret.access.key",
		"basic.auth.user.info",
		"tls.private.key",
		"bearer.token",
		// pattern matches
		"password",
		"some.token",
		"my.secret",
		"the.credential",
		// newly-generalized secret families (previously only fully-qualified)
		"some.connector.api.key",
		"service.apikey",
		"gcs.access.key",
		"minio.accesskey",
		"keystore.passphrase",
		"sasl.kerberos.keytab",
		"ssl.private.key",
		"client.privatekey",
		// case-insensitive
		"DATABASE.PASSWORD",
		"MY_SECRET_KEY",
		"Authorization.Token",
		// substring of a static entry behind a prefix (why the static list matters)
		"producer.override.sasl.jaas.config",
		"consumer.override.ssl.keystore.key",
		// pattern as substring
		"consumer.credentials.provider",
	}
	for _, k := range sensitive {
		if !IsSensitive(k) {
			t.Errorf("IsSensitive(%q) = false, want true", k)
		}
	}
}

func TestIsSensitive_BenignKeys_NoFalsePositives(t *testing.T) {
	benign := []string{
		"tasks.max",
		"connector.class",
		"topics",
		"key.converter",
		"value.converter",
		"name",
		"transforms",
		"errors.tolerance",
		"poll.interval.ms",
		"", // empty key
	}
	for _, k := range benign {
		if IsSensitive(k) {
			t.Errorf("IsSensitive(%q) = true, want false (false positive)", k)
		}
	}
}

// TestIsSensitive_AcceptedOverRedaction locks in the deliberate fail-closed
// tradeoff: IsSensitive matches blacklist entries as case-insensitive
// SUBSTRINGS, so a benign key that merely embeds "password"/"token"/"secret"/
// "credential" is redacted even though its value is not a secret. This over-
// redaction is accepted — losing a non-secret value from the persisted config is
// preferred over leaking a real secret. These keys are NOT secrets; if a future
// change stops redacting them, that is a conscious narrowing of the blacklist and
// this test should be updated to match, not silently broken.
func TestIsSensitive_AcceptedOverRedaction(t *testing.T) {
	overRedacted := []struct {
		key string
		why string
	}{
		{"tokenizer.class", `"tokenizer" embeds "token"; this is a class name, not a secret`},
		{"token.bucket.size", `rate-limiter setting; "token" here is not an auth token`},
		{"aws.credentials.provider", `names a credentials-provider class; embeds "credential"`},
		{"credentials.provider.class", `provider class name; embeds "credential"`},
	}
	for _, tc := range overRedacted {
		if !IsSensitive(tc.key) {
			t.Errorf("IsSensitive(%q) = false, want true (accepted over-redaction: %s)", tc.key, tc.why)
		}
	}
}

func TestRedactStringMap_RedactsSensitiveKeepsBenign(t *testing.T) {
	in := map[string]string{
		"database.password": "hunter2",
		"connection.url":    "jdbc:postgresql://db:5432/app",
		"tasks.max":         "3",
		"aws.access.key.id": "AKIA-not-secret-id", // contains no pattern; not in static list as substring? -> see below
	}
	out, count := RedactStringMap(in)

	if out["database.password"] != Placeholder {
		t.Errorf("database.password = %q, want %q", out["database.password"], Placeholder)
	}
	if out["connection.url"] != "jdbc:postgresql://db:5432/app" {
		t.Errorf("connection.url was altered: %q", out["connection.url"])
	}
	if out["tasks.max"] != "3" {
		t.Errorf("tasks.max was altered: %q", out["tasks.max"])
	}
	if count < 1 {
		t.Errorf("count = %d, want at least 1", count)
	}
	// input must not be mutated
	if in["database.password"] != "hunter2" {
		t.Errorf("input map was mutated: %q", in["database.password"])
	}
}

func TestRedactStringMap_CountIsExact(t *testing.T) {
	in := map[string]string{
		"database.password":    "x",    // sensitive
		"oauth2.client.secret": "y",    // sensitive
		"tasks.max":            "3",    // benign
		"connector.class":      "io.x", // benign
		"topics":               "a,b",  // benign
	}
	_, count := RedactStringMap(in)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestRedactStringMap_NilSafe(t *testing.T) {
	out, count := RedactStringMap(nil)
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
	if len(out) != 0 {
		t.Errorf("out = %v, want empty", out)
	}
}

func TestRedactAnyMap_FlatRedaction(t *testing.T) {
	in := map[string]any{
		"database.password": "hunter2",
		"tasks.max":         "3",
	}
	out, count := RedactAnyMap(in)
	if out["database.password"] != Placeholder {
		t.Errorf("database.password = %v, want %q", out["database.password"], Placeholder)
	}
	if out["tasks.max"] != "3" {
		t.Errorf("tasks.max = %v, want unchanged", out["tasks.max"])
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestRedactAnyMap_NestedMap(t *testing.T) {
	// A sensitive key nested inside a NON-sensitive container must be redacted.
	in := map[string]any{
		"config": map[string]any{
			"nested.password": "deepsecret",
			"nested.timeout":  "30",
		},
	}
	out, count := RedactAnyMap(in)
	nested := out["config"].(map[string]any)
	if nested["nested.password"] != Placeholder {
		t.Errorf("nested.password = %v, want %q", nested["nested.password"], Placeholder)
	}
	if nested["nested.timeout"] != "30" {
		t.Errorf("nested.timeout = %v, want unchanged", nested["nested.timeout"])
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestRedactAnyMap_SensitiveKeyWithContainerValueRedactedWholesale(t *testing.T) {
	// Fail-closed: a sensitive key whose value is a container is redacted entirely.
	in := map[string]any{
		"credentials": map[string]any{
			"user": "admin",
			"pass": "p",
		},
	}
	out, count := RedactAnyMap(in)
	if out["credentials"] != Placeholder {
		t.Errorf("credentials = %v, want wholesale %q", out["credentials"], Placeholder)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestRedactAnyMap_ListOfMaps(t *testing.T) {
	in := map[string]any{
		"items": []any{
			map[string]any{"token": "abc", "id": "1"},
			map[string]any{"id": "2"},
		},
	}
	out, count := RedactAnyMap(in)
	items := out["items"].([]any)
	first := items[0].(map[string]any)
	if first["token"] != Placeholder {
		t.Errorf("items[0].token = %v, want %q", first["token"], Placeholder)
	}
	if first["id"] != "1" {
		t.Errorf("items[0].id = %v, want unchanged", first["id"])
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestRedactAnyMap_Empty(t *testing.T) {
	out, count := RedactAnyMap(map[string]any{})
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
	if len(out) != 0 {
		t.Errorf("out = %v, want empty", out)
	}
}

func TestRedactAnyMap_DoesNotMutateInput(t *testing.T) {
	in := map[string]any{"database.password": "hunter2"}
	_, _ = RedactAnyMap(in)
	if in["database.password"] != "hunter2" {
		t.Errorf("input map was mutated: %v", in["database.password"])
	}
}

func TestPlaceholderValue(t *testing.T) {
	if Placeholder != "<kcp-redacted>" {
		t.Errorf("Placeholder = %q, want %q", Placeholder, "<kcp-redacted>")
	}
}

func TestMapContainsRedacted(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		want bool
	}{
		{
			name: "value equal to placeholder",
			in:   map[string]string{"database.password": Placeholder, "tasks.max": "3"},
			want: true,
		},
		{
			name: "no placeholder",
			in:   map[string]string{"connector.class": "io.x", "tasks.max": "3"},
			want: false,
		},
		{
			name: "nil map",
			in:   nil,
			want: false,
		},
		{
			name: "empty map",
			in:   map[string]string{},
			want: false,
		},
		{
			// Exact-equality rule: redaction sets the WHOLE value to Placeholder.
			// A benign value that merely embeds the placeholder text as a substring
			// is NOT considered redacted, so it must not trip the warning.
			name: "placeholder as substring is not a match",
			in:   map[string]string{"notes": "see <kcp-redacted> docs for details"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapContainsRedacted(tt.in); got != tt.want {
				t.Errorf("MapContainsRedacted(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestAnyMapContainsRedacted(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want bool
	}{
		{
			name: "flat value equal to placeholder",
			in:   map[string]any{"database.password": Placeholder, "tasks.max": "3"},
			want: true,
		},
		{
			name: "flat no placeholder",
			in:   map[string]any{"connector.class": "io.x"},
			want: false,
		},
		{
			name: "placeholder nested inside a map",
			in: map[string]any{
				"config": map[string]any{
					"nested.password": Placeholder,
					"nested.timeout":  "30",
				},
			},
			want: true,
		},
		{
			name: "placeholder nested inside a list",
			in: map[string]any{
				"items": []any{
					map[string]any{"id": "1"},
					map[string]any{"token": Placeholder},
				},
			},
			want: true,
		},
		{
			name: "deeply nested non-placeholder",
			in: map[string]any{
				"a": map[string]any{
					"b": []any{
						map[string]any{"c": "value", "d": "30"},
					},
				},
			},
			want: false,
		},
		{
			name: "placeholder as substring is not a match",
			in:   map[string]any{"notes": "see <kcp-redacted> docs for details"},
			want: false,
		},
		{
			name: "nil map",
			in:   nil,
			want: false,
		},
		{
			name: "empty map",
			in:   map[string]any{},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AnyMapContainsRedacted(tt.in); got != tt.want {
				t.Errorf("AnyMapContainsRedacted(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// Guards that a benign config typical of a real connector survives untouched.
func TestRedactStringMap_RealisticBenignConfigUntouched(t *testing.T) {
	in := map[string]string{
		"connector.class": "io.confluent.connect.s3.S3SinkConnector",
		"tasks.max":       "2",
		"topics":          "orders,events",
		"key.converter":   "org.apache.kafka.connect.storage.StringConverter",
		"value.converter": "io.confluent.connect.avro.AvroConverter",
	}
	want := make(map[string]string, len(in))
	maps.Copy(want, in)
	out, count := RedactStringMap(in)
	if count != 0 {
		t.Errorf("count = %d, want 0 (no secrets in benign config)", count)
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("benign config altered:\n got %v\nwant %v", out, want)
	}
}
