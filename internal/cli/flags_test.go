package cli

import "testing"

func TestParseConfigValidate(t *testing.T) {
	cmd, err := Parse([]string{"config", "validate", "-c", "mirror.conf"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cmd.Name != "config" || cmd.Subcommand != "validate" || cmd.ConfigPath != "mirror.conf" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParseURLUppercase(t *testing.T) {
	cmd, err := Parse([]string{"config", "generate", "--URL", "http://example.test/repo/"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cmd.URL != "http://example.test/repo/" {
		t.Fatalf("unexpected URL: %q", cmd.URL)
	}
}

func TestParseRejectsLowercaseURL(t *testing.T) {
	_, err := Parse([]string{"config", "generate", "--url", "http://example.test/repo/"})
	if err == nil {
		t.Fatal("expected lowercase --url to be rejected")
	}
}

func TestParseDateFlag(t *testing.T) {
	cmd, err := Parse([]string{"rollback", "-n", "ubuntu-xenial", "-d", "2024-12-01"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cmd.Date != "2024-12-01" {
		t.Fatalf("unexpected date: %q", cmd.Date)
	}
}

func TestParseCreateConfig(t *testing.T) {
	cmd, err := Parse([]string{"create", "-c", "mirror.conf"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cmd.Name != "create" || cmd.ConfigPath != "mirror.conf" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParseUpdateName(t *testing.T) {
	cmd, err := Parse([]string{"update", "-n", "ubuntu"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cmd.Name != "update" || cmd.NameRef != "ubuntu" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParsePeriodicCommandsRejectIdentityFlags(t *testing.T) {
	for _, command := range []string{"daily", "weekly", "monthly"} {
		if _, err := Parse([]string{command, "-n", "ubuntu"}); err == nil {
			t.Fatalf("expected %s -n to be rejected", command)
		}
		if _, err := Parse([]string{command, "-c", "mirror.conf"}); err == nil {
			t.Fatalf("expected %s -c to be rejected", command)
		}
	}
}

func TestParsePeriodicCommandsAcceptNoArgs(t *testing.T) {
	for _, command := range []string{"daily", "weekly", "monthly"} {
		cmd, err := Parse([]string{command})
		if err != nil {
			t.Fatalf("Parse(%s) returned error: %v", command, err)
		}
		if cmd.Name != command {
			t.Fatalf("unexpected command: %#v", cmd)
		}
	}
}

func TestParseCleanupDays(t *testing.T) {
	cmd, err := Parse([]string{"cleanup", "-n", "ubuntu-xenial", "--days", "30"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !cmd.CleanupDaysSet || cmd.CleanupDays != 30 {
		t.Fatalf("unexpected cleanup days: %#v", cmd)
	}
}

func TestParseCleanupAll(t *testing.T) {
	cmd, err := Parse([]string{"cleanup", "-n", "ubuntu-xenial", "--all"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !cmd.CleanupAll {
		t.Fatal("expected cleanup all flag")
	}
}

func TestParseCleanupRejectsDate(t *testing.T) {
	_, err := Parse([]string{"cleanup", "-n", "ubuntu-xenial", "-d", "2026-05-01"})
	if err == nil {
		t.Fatal("expected cleanup date to be rejected")
	}
}

func TestParseCleanupRejectsAmbiguousMode(t *testing.T) {
	_, err := Parse([]string{"cleanup", "-n", "ubuntu-xenial", "--days", "30", "--all"})
	if err == nil {
		t.Fatal("expected ambiguous cleanup mode to be rejected")
	}
}

// TestParsePeriodicCommandsRejectCleanupDaysFlag covers the Phase 16 contract:
// periodic commands accept no flags, including cleanup-specific flags.
func TestParsePeriodicCommandsRejectCleanupDaysFlag(t *testing.T) {
	for _, command := range []string{"daily", "weekly", "monthly"} {
		_, err := Parse([]string{command, "--days", "7"})
		if err == nil {
			t.Errorf("%s --days 7 was accepted; want rejection", command)
		}
	}
}

// TestParsePeriodicCommandsRejectCleanupAllFlag covers the Phase 16 contract:
// periodic commands accept no flags, including cleanup-specific flags.
func TestParsePeriodicCommandsRejectCleanupAllFlag(t *testing.T) {
	for _, command := range []string{"daily", "weekly", "monthly"} {
		_, err := Parse([]string{command, "--all"})
		if err == nil {
			t.Errorf("%s --all was accepted; want rejection", command)
		}
	}
}

func TestParseInfoURL(t *testing.T) {
	cmd, err := Parse([]string{"info", "--URL", "http://example.test/repo/"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cmd.URL != "http://example.test/repo/" {
		t.Fatalf("unexpected URL: %q", cmd.URL)
	}
}
