package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildProducesValidEgg(t *testing.T) {
	e := build("#!/bin/ash\necho hi\n")

	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}

	if got := e.Meta.Version; got != "PTDL_v2" {
		t.Errorf("meta.version = %q, want PTDL_v2", got)
	}
	if e.Scripts.Installation.Script != "#!/bin/ash\necho hi\n" {
		t.Error("install script was not embedded verbatim")
	}

	// config values must themselves be JSON documents.
	for name, doc := range map[string]string{"files": e.Config.Files, "startup": e.Config.Startup, "logs": e.Config.Logs} {
		var v map[string]any
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			t.Errorf("config.%s is not a JSON object: %v", name, err)
		}
	}
}

func TestVariablesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}

	for _, v := range build("").Variables {
		if !strings.HasPrefix(v.EnvVariable, "KEST_") {
			t.Errorf("%s: env variable must start with KEST_", v.EnvVariable)
		}
		if seen[v.EnvVariable] {
			t.Errorf("%s defined twice", v.EnvVariable)
		}
		seen[v.EnvVariable] = true

		if v.Name == "" || v.Description == "" || v.Rules == "" || v.FieldType == "" {
			t.Errorf("%s: name, description, rules and field_type are required", v.EnvVariable)
		}
		if strings.Contains(v.Rules, "required") && v.DefaultValue == "" &&
			v.EnvVariable != "KEST_TELEGRAM_TOKEN" && v.EnvVariable != "KEST_OPENROUTER_API_KEY" && v.EnvVariable != "KEST_OWNER_ID" {
			t.Errorf("%s: required variable without a default would block server creation", v.EnvVariable)
		}
	}

	for _, must := range []string{"KEST_TELEGRAM_TOKEN", "KEST_OPENROUTER_API_KEY", "KEST_OWNER_ID", "KEST_VERSION"} {
		if !seen[must] {
			t.Errorf("egg is missing %s", must)
		}
	}
}

func TestStartupDoneMatchesAppReadyMessage(t *testing.T) {
	// internal/app.ReadyMessage is "kest: ready"; the egg waits for that text.
	if !strings.Contains(build("").Config.Startup, "kest: ready") {
		t.Error("egg startup.done does not match app.ReadyMessage")
	}
}

func TestBrowserEggUsesRuntimeAndSafeDefaults(t *testing.T) {
	e := buildBrowser("installer")
	if e.DockerImages["Chromium (local image on Wings node)"] != "kest-browser:local" {
		t.Fatal("browser image missing")
	}
	settings := map[string]string{}
	for _, v := range e.Variables {
		settings[v.EnvVariable] = v.DefaultValue
	}
	if !strings.Contains(settings["KEST_PLUGINS"], "browser") || settings["KEST_BROWSER_SANDBOX"] != "true" {
		t.Fatal("unsafe or disabled browser defaults")
	}
	if e.Scripts.Installation.Script != "installer" {
		t.Fatal("browser egg lost checksum installer")
	}
}
