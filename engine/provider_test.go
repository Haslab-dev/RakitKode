package engine

import "testing"

func TestEnvOnlyProvider(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	e, err := New(Options{Home: t.TempDir(), Workspace: t.TempDir(), Model: "deepseek/deepseek-chat"})
	if err != nil {
		t.Fatal(err)
	}
	if !e.HasProvider() {
		t.Fatal("expected env-only provider to be detected")
	}
	if provider, model := e.ModelInfo(); provider != "deepseek" || model != "deepseek-chat" {
		t.Fatalf("model = %s/%s", provider, model)
	}
}

func TestNoProvider(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "ZHIPU_API_KEY", "GLM_API_KEY"} {
		t.Setenv(key, "")
	}
	e, err := New(Options{Home: t.TempDir(), Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if e.HasProvider() {
		t.Fatal("expected no provider without keys")
	}
}
