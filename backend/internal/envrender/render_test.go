package envrender

import "testing"

func TestExpandSimple(t *testing.T) {
	got, err := Expand("PORT=${PORT}\n", func(key string) (string, bool) {
		if key == "PORT" {
			return "8080", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "PORT=8080\n" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandChained(t *testing.T) {
	env := map[string]string{"A": "${B}", "B": "ok"}
	got, err := Expand("${A}", func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandMissing(t *testing.T) {
	_, err := Expand("${MISSING}", func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("expected error")
	}
}
