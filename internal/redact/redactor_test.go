package redact

import (
	"strings"
	"testing"
)

func TestSecretsRedactsLanguageAgnosticAssignments(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`OPENAI_API_KEY=sk-actual-secret-value`,
		`{"password": "json secret value"}`,
		`const clientSecret = 'typescript-secret';`,
		`token: yaml-secret-value`,
		`password := os.Getenv("PASSWORD")`,
		`const token = process.env.TOKEN;`,
		`secret: "example-placeholder"`,
	}, "\n")

	result := Secrets(input)
	if result.Count != 4 {
		t.Fatalf("Count = %d, want 4\n%s", result.Count, result.Text)
	}
	for _, secret := range []string{"sk-actual-secret-value", "json secret value", "typescript-secret", "yaml-secret-value"} {
		if strings.Contains(result.Text, secret) {
			t.Errorf("redacted text contains %q:\n%s", secret, result.Text)
		}
	}
	for _, reference := range []string{`os.Getenv("PASSWORD")`, "process.env.TOKEN", "example-placeholder"} {
		if !strings.Contains(result.Text, reference) {
			t.Errorf("dynamic reference or placeholder %q was changed:\n%s", reference, result.Text)
		}
	}
	if strings.Count(result.Text, "\n") != strings.Count(input, "\n") {
		t.Fatal("redaction changed line count")
	}
}

func TestSecretsRedactsTokensURLsAndAuthorization(t *testing.T) {
	t.Parallel()

	githubToken := "ghp_" + strings.Repeat("A", 24)
	jwt := "eyJ" + strings.Repeat("A", 10) + "." + strings.Repeat("B", 10) + "." + strings.Repeat("C", 10)
	input := strings.Join([]string{
		`client.Do("` + githubToken + `")`,
		`url := "postgres://user:database-password@localhost/app"`,
		`Authorization: Bearer bearer-token-value`,
		`jwt := "` + jwt + `"`,
	}, "\n")

	result := Secrets(input)
	if result.Count != 4 {
		t.Fatalf("Count = %d, want 4\n%s", result.Count, result.Text)
	}
	for _, secret := range []string{githubToken, "database-password", "bearer-token-value", jwt} {
		if strings.Contains(result.Text, secret) {
			t.Errorf("redacted text contains %q:\n%s", secret, result.Text)
		}
	}
	if !strings.Contains(result.Text, "postgres://user:"+Placeholder+"@localhost/app") {
		t.Fatalf("URL structure was not preserved:\n%s", result.Text)
	}
}

func TestSecretsRedactsPrivateKeyBlockAndPreservesDiffShape(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`+const key = "value"`,
		`+-----BEGIN ENCRYPTED PRIVATE KEY-----`,
		`+MIIE6TAbBgkqhkiG9w0BBQMwDgQI`,
		`+more-private-key-material`,
		`+-----END ENCRYPTED PRIVATE KEY-----`,
		`+password = "added-secret"`,
		`-password = "removed-secret"`,
	}, "\r\n")

	result := Secrets(input)
	if result.Count != 3 {
		t.Fatalf("Count = %d, want 3\n%s", result.Count, result.Text)
	}
	for _, secret := range []string{"MIIE6TAbBgkqhkiG9w0BBQMwDgQI", "more-private-key-material", "added-secret", "removed-secret"} {
		if strings.Contains(result.Text, secret) {
			t.Errorf("redacted text contains %q:\n%s", secret, result.Text)
		}
	}
	if strings.Count(result.Text, "\r\n") != strings.Count(input, "\r\n") {
		t.Fatal("private-key redaction changed line count")
	}
	if !strings.Contains(result.Text, "+"+Placeholder+"\r\n+") {
		t.Fatalf("diff prefixes were not preserved:\n%s", result.Text)
	}
}

func TestSecretsIsIdempotent(t *testing.T) {
	t.Parallel()

	first := Secrets(`password = "actual-secret-value"`)
	second := Secrets(first.Text)
	if second.Text != first.Text || second.Count != 0 {
		t.Fatalf("second redaction = %#v, first = %#v", second, first)
	}
}
