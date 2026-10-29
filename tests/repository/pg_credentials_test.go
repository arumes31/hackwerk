package repository_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPGCredentialsKeepPasswordsOffArgv runs the shared ops helper against a
// stub libpq client that records its argv and the passfile it was given.
//nolint:gosec // Fake test-only passwords; the stub client must be executable.
func TestPGCredentialsKeepPasswordsOffArgv(t *testing.T) {
	t.Parallel()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX sh is not available")
	}
	_, current, _, _ := runtime.Caller(0)
	helper := filepath.Join(filepath.Dir(current), "..", "..", "scripts", "ops", "lib", "pg-credentials.sh")

	tests := []struct {
		name       string
		connection string
		wantArgs   string
		wantPass   string
		wantError  string
	}{
		{
			name:       "percent encoded url password",
			connection: "postgres://app:p%40ss%3Aw%5Cord%25x@db.example:5432/hackwerk?sslmode=verify-full",
			wantArgs:   "--dbname=postgres://app@db.example:5432/hackwerk?sslmode=verify-full|-c|q",
			wantPass:   `*:*:*:*:p@ss\:w\\ord%x`,
		},
		{
			name:       "postgresql scheme with password query parameter",
			connection: "postgresql://app@db.example/hackwerk?password=test-only-s3cr3t&sslmode=require",
			wantArgs:   "--dbname=postgresql://app@db.example/hackwerk?sslmode=require|-c|q",
			wantPass:   "*:*:*:*:test-only-s3cr3t",
		},
		{
			name:       "unencoded query delimiter in password",
			connection: "postgres://app:a?b&c@postgres/hackwerk",
			wantArgs:   "--dbname=postgres://app@postgres/hackwerk|-c|q",
			wantPass:   "*:*:*:*:a?b&c",
		},
		{
			name:       "utf-8 password",
			connection: "postgres://app:p%C3%A4ss@[::1]:5432/hackwerk",
			wantArgs:   "--dbname=postgres://app@[::1]:5432/hackwerk|-c|q",
			wantPass:   "*:*:*:*:päss",
		},
		{
			name:       "url without password",
			connection: "postgres://app@/hackwerk?host=/run/postgresql",
			wantArgs:   "--dbname=postgres://app@/hackwerk?host=/run/postgresql|-c|q",
			wantPass:   "none",
		},
		{
			name:       "conninfo without password",
			connection: "host=db.example dbname=hackwerk sslmode=verify-full",
			wantArgs:   "--dbname=host=db.example dbname=hackwerk sslmode=verify-full|-c|q",
			wantPass:   "none",
		},
		{name: "conninfo with password", connection: "host=db.example password=s3cr3t", wantError: "Key=Value"},
		{name: "unsupported scheme", connection: "mysql://app:s3cr3t@db/hackwerk", wantError: "Schema"},
		{name: "invalid percent encoding", connection: "postgres://app:s3%zzcr3t@db/hackwerk", wantError: "Prozentkodierung"},
		{name: "encoded newline", connection: "postgres://app:a%0Ab@db/hackwerk", wantError: "Steuerzeichen"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			stub := filepath.Join(directory, "psql")
			script := "#!/bin/sh\n" +
				"out=$STUB_OUT\n" +
				"first=1; for arg in \"$@\"; do if [ $first = 1 ]; then printf '%s' \"$arg\" > \"$out.args\"; first=0; else printf '|%s' \"$arg\" >> \"$out.args\"; fi; done\n" +
				"if [ -n \"${PGPASSFILE:-}\" ]; then cat \"$PGPASSFILE\" > \"$out.pass\"; ls -l \"$PGPASSFILE\" | cut -c1-10 > \"$out.mode\"; else echo none > \"$out.pass\"; fi\n"
			if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			driver := `. "$HELPER"; trap pg_credentials_cleanup EXIT; pg_connection_prepare target "$CONNECTION"; pg_run target psql -c q; printf '%s' "$pg_credentials_dir" > "$STUB_OUT.dir"`
			command := exec.CommandContext(t.Context(), shell, "-eu", "-c", driver)
			command.Env = append(os.Environ(),
				"HELPER="+filepath.ToSlash(helper), "CONNECTION="+test.connection,
				"STUB_OUT="+filepath.ToSlash(filepath.Join(directory, "out")),
				"TMPDIR="+filepath.ToSlash(directory),
				"PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if test.wantError != "" {
				if err == nil || !strings.Contains(string(output), test.wantError) {
					t.Fatalf("expected failure containing %q, err=%v output=%s", test.wantError, err, output)
				}
				if strings.Contains(string(output), "s3cr3t") {
					t.Fatalf("error output leaked the password: %s", output)
				}
				return
			}
			if err != nil {
				t.Fatalf("helper failed: %v: %s", err, output)
			}
			args := readStubOutput(t, directory, "out.args")
			if args != test.wantArgs {
				t.Fatalf("argv = %q, want %q", args, test.wantArgs)
			}
			if pass := strings.TrimRight(readStubOutput(t, directory, "out.pass"), "\n"); pass != test.wantPass {
				t.Fatalf("passfile = %q, want %q", pass, test.wantPass)
			}
			if test.wantPass != "none" && runtime.GOOS != "windows" {
				if mode := strings.TrimSpace(readStubOutput(t, directory, "out.mode")); mode != "-rw-------" {
					t.Fatalf("passfile mode = %q", mode)
				}
			}
			if credentials := readStubOutput(t, directory, "out.dir"); credentials != "" {
				if _, err := os.Stat(credentials); !os.IsNotExist(err) {
					t.Fatalf("credential directory %q was not removed: %v", credentials, err)
				}
			}
		})
	}
}

func TestPostgresOpsScriptsRouteConnectionsThroughCredentialHelper(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"backup-postgres.sh", "restore-postgres.sh", "restore-test.sh"} {
		script := repositoryFile(t, "scripts", "ops", name)
		if !strings.Contains(script, `. "$script_dir/lib/pg-credentials.sh"`) || !strings.Contains(script, "pg_credentials_cleanup") {
			t.Fatalf("%s does not load and clean up the credential helper", name)
		}
		if strings.Contains(script, "--dbname=") {
			t.Fatalf("%s passes a connection string to a libpq client directly", name)
		}
	}
	if compose := repositoryFile(t, "compose.prod.example.yaml"); !strings.Contains(compose, "./scripts/ops:/opt/hackwerk:ro") {
		t.Fatal("backup container no longer mounts the whole ops directory including lib/")
	}
	if tools := repositoryFile(t, "scripts", "release", "postgres-tools.Dockerfile"); !strings.Contains(tools, "COPY scripts/ops /ops") {
		t.Fatal("restore smoke image no longer ships the whole ops directory including lib/")
	}
}

//nolint:gosec // Reads files the test itself created in its temp directory.
func readStubOutput(t *testing.T, directory, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
