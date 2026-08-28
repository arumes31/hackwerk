package repository_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseScriptsReadSchemaVersionFromBinary(t *testing.T) {
	t.Parallel()
	restore := repositoryFile(t, "scripts", "ops", "restore-postgres.sh")
	migration := repositoryFile(t, "scripts", "release", "migration-smoke.sh")
	toolsImage := repositoryFile(t, "scripts", "release", "postgres-tools.Dockerfile")

	if !strings.Contains(restore, `"$schema_binary" schema-version`) || strings.Contains(restore, `"$schema_version" = 10`) {
		t.Fatal("restore script does not use the release binary's canonical schema version")
	}
	if !strings.Contains(migration, `"$image" schema-version`) || strings.Contains(migration, `"$version" = 10`) {
		t.Fatal("migration smoke does not use the release image's canonical schema version")
	}
	if !strings.Contains(toolsImage, "COPY --from=hackwerk /hackwerk /hackwerk") || !strings.Contains(toolsImage, "HACKWERK_BINARY=/hackwerk") {
		t.Fatal("restore smoke image does not carry the exact HackWerk release binary")
	}
}

func TestProductionComposeHealthAndProviderSecretContracts(t *testing.T) {
	t.Parallel()
	compose := repositoryFile(t, "compose.prod.example.yaml")
	worker := section(t, compose, "  worker:\n", "  backup:\n")
	app := section(t, compose, "  app:\n", "  worker:\n")

	if !strings.Contains(app, `test: ["CMD", "/hackwerk", "healthcheck"]`) {
		t.Fatal("app container does not use the local binary healthcheck")
	}
	if !strings.Contains(app, "VOICE_OPENAI_API_KEY_FILE: /run/secrets/voice_openai_api_key") ||
		!strings.Contains(app, "secrets: [database_url, confirmation_token_keys, voice_openai_api_key, map_tile_token]") ||
		!strings.Contains(compose, "voice_openai_api_key: {file: ./secrets/voice_openai_api_key}") {
		t.Fatal("app container does not inject the optional voice provider key through a mounted secret")
	}
	if strings.Contains(app, "VOICE_OPENAI_API_KEY:") {
		t.Fatal("app container forwards the voice provider key directly through the environment")
	}
	if !strings.Contains(app, "MAP_TILE_TOKEN_FILE: /run/secrets/map_tile_token") ||
		!strings.Contains(app, "secrets: [database_url, confirmation_token_keys, voice_openai_api_key, map_tile_token]") ||
		!strings.Contains(compose, "map_tile_token: {file: ./secrets/map_tile_token}") || strings.Contains(compose, "MAP_TILE_TOKEN:") {
		t.Fatal("map tile token is not isolated to the web container secret")
	}
	if !strings.Contains(worker, `test: ["CMD", "/hackwerk", "healthcheck", "worker"]`) {
		t.Fatal("worker container has no heartbeat-aware healthcheck")
	}
	for _, secret := range []string{
		"MAIL_SMTP_USERNAME_FILE: /run/secrets/mail_smtp_username",
		"MAIL_SMTP_PASSWORD_FILE: /run/secrets/mail_smtp_password",
		"SENDBERRY_API_KEY_FILE: /run/secrets/sendberry_api_key",
		"SENDBERRY_ACCESS_NAME_FILE: /run/secrets/sendberry_access_name",
		"SENDBERRY_ACCESS_PASSWORD_FILE: /run/secrets/sendberry_access_password",
		"SMS_WEBHOOK_HMAC_SECRET_FILE: /run/secrets/sms_webhook_hmac_secret",
	} {
		if !strings.Contains(worker, secret) {
			t.Fatalf("worker compose section is missing %q", secret)
		}
	}
	for _, secretName := range []string{
		"mail_smtp_username", "mail_smtp_password", "sendberry_api_key", "sendberry_access_name", "sendberry_access_password", "sms_webhook_hmac_secret",
	} {
		if !strings.Contains(worker, "      - "+secretName+"\n") {
			t.Fatalf("worker does not mount provider secret %q", secretName)
		}
		if !strings.Contains(compose, "  "+secretName+": {file: ./secrets/"+secretName+"}") {
			t.Fatalf("production compose does not declare provider secret %q", secretName)
		}
	}
	for _, directSecret := range []string{"MAIL_SMTP_USERNAME:", "MAIL_SMTP_PASSWORD:", "SENDBERRY_API_KEY:", "SENDBERRY_ACCESS_NAME:", "SENDBERRY_ACCESS_PASSWORD:", "SMS_WEBHOOK_HMAC_SECRET:"} {
		if strings.Contains(worker, directSecret) {
			t.Fatalf("worker compose section forwards provider secret directly via %q", directSecret)
		}
	}
	if strings.Contains(app, "SENDBERRY_") || strings.Contains(app, "MAIL_SMTP_") {
		t.Fatal("web application received worker provider credentials")
	}
}

func TestBuildAndPostgresImagesAreDigestPinned(t *testing.T) {
	t.Parallel()
	const goImage = "golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452"
	const postgresImage = "postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2"

	if !strings.Contains(repositoryFile(t, "Dockerfile"), goImage) {
		t.Fatal("Go builder image is not pinned to the reviewed multi-architecture digest")
	}
	for _, path := range [][]string{
		{"compose.yaml"}, {"compose.prod.example.yaml"}, {"scripts", "release", "postgres-tools.Dockerfile"},
		{"scripts", "release", "migration-smoke.sh"}, {"scripts", "release", "container-smoke.sh"},
		{"scripts", "release", "backup-restore-smoke.sh"}, {".github", "workflows", "ci.yml"},
	} {
		if !strings.Contains(repositoryFile(t, path...), postgresImage) {
			t.Fatalf("%s does not pin the reviewed PostgreSQL image digest", filepath.Join(path...))
		}
	}
}

func TestOSRMRuntimeAndUpdaterAreIsolated(t *testing.T) {
	t.Parallel()
	compose := repositoryFile(t, "compose.prod.example.yaml")
	developmentCompose := repositoryFile(t, "compose.yaml")
	runtime := section(t, compose, "  osrm:\n", "  osrm-update:\n")
	updater := section(t, compose, "  osrm-update:\n", "  app:\n")
	app := section(t, compose, "  app:\n", "  worker:\n")

	for _, required := range []string{
		`["osrm-routed", "--threads", "2", "--algorithm", "mld", "--mmap"`,
		"networks: [routing]",
		"/data:ro",
		"read_only: true",
		`user: "65532:65532"`,
		"no-new-privileges:true",
		"cap_drop: [ALL]",
	} {
		if !strings.Contains(runtime, required) {
			t.Fatalf("OSRM runtime is missing %q", required)
		}
	}
	if strings.Contains(runtime, "ports:") || strings.Contains(runtime, "egress") || strings.Contains(runtime, "backend") {
		t.Fatal("OSRM runtime must not have a host port, egress, or database network")
	}
	if !strings.Contains(updater, "networks: [egress]") || strings.Contains(updater, "routing") || strings.Contains(updater, "backend") {
		t.Fatal("OSRM updater must have egress only and no runtime or database network")
	}
	if !strings.Contains(app, "routing: {}") || !strings.Contains(compose, "  routing:\n    internal: true") {
		t.Fatal("application and OSRM are not connected through an isolated routing network")
	}
	for _, resourceLimit := range []string{`cpus: "2.00"`, "mem_limit: 4g", "memswap_limit: 6g"} {
		if !strings.Contains(developmentCompose, resourceLimit) {
			t.Fatalf("development OSRM updater is missing resource limit %q", resourceLimit)
		}
	}
}

func TestOSRMBuildUsesPinnedToolchainAndCropBeforeMerge(t *testing.T) {
	t.Parallel()
	const osrmImage = "ghcr.io/project-osrm/osrm-backend:v26.7.3-debian@sha256:a7091038e39a73659767f34ef2d389909b42ea80b09bd2bdca482dce2991cbad"
	dockerfile := repositoryFile(t, "scripts", "ops", "osrm-tools.Dockerfile")
	if !strings.Contains(dockerfile, osrmImage) {
		t.Fatal("OSRM toolchain image is not pinned to the reviewed digest")
	}

	updater := repositoryFile(t, "scripts", "ops", "update-osrm.sh")
	for _, required := range []string{
		"9.4,46.3,17.5,49.5",
		"https://download.geofabrik.de/europe/austria-latest.osm.pbf",
		"https://download.geofabrik.de/europe/germany/bayern-latest.osm.pbf",
		"https://download.geofabrik.de/europe/czech-republic-latest.osm.pbf",
		"--strategy=complete_ways",
		"osrm-partition",
		"osrm-customize",
		"--trial",
		"mv -Tf -- \"$data_root/current.next.$$\" \"$data_root/current\"",
		"regional OSM sources do not share one replication timestamp",
		"OSRM validation route has no positive road metrics or geometry",
	} {
		if !strings.Contains(updater, required) {
			t.Fatalf("OSRM updater is missing %q", required)
		}
	}
	if strings.Index(updater, "osmium extract") > strings.Index(updater, "osmium merge") {
		t.Fatal("OSRM source files are merged before they are cropped")
	}

	hostUpdate := repositoryFile(t, "scripts", "ops", "update-osrm-host.sh")
	for _, required := range []string{
		"install -d -m 0750 -o 65532 -g 65532",
		"docker image inspect",
		"wait_for_healthy_osrm",
		"osrm-update rollback",
		"osrm-update prune",
	} {
		if !strings.Contains(hostUpdate, required) {
			t.Fatalf("OSRM host updater is missing %q", required)
		}
	}
}

func TestReleaseScannerAndDockerfileFrontendImagesAreDigestPinned(t *testing.T) {
	t.Parallel()
	const dockerfileFrontend = "# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e"
	const trivyImage = "ghcr.io/aquasecurity/trivy:0.74.0@sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969"
	const syftImage = "anchore/syft:v1.51.0@sha256:678bfa565b60f747aac0f8e964fe5588a24445b8d0a480e91f6efd70020dfbb0"

	firstLine, _, _ := strings.Cut(repositoryFile(t, "Dockerfile"), "\n")
	if strings.TrimSuffix(firstLine, "\r") != dockerfileFrontend {
		t.Fatal("Dockerfile frontend is not pinned to the reviewed multi-architecture digest")
	}
	makefile := repositoryFile(t, "Makefile")
	for _, invocation := range []string{trivyImage + " image --input", syftImage + " docker-archive:"} {
		if !strings.Contains(makefile, invocation) {
			t.Fatalf("release scanner invocation %q is not pinned to the reviewed multi-architecture digest", invocation)
		}
	}
}

func TestBuildVersionIncrementsWithEveryCommit(t *testing.T) {
	t.Parallel()
	versionScript := repositoryFile(t, "scripts", "version.sh")
	if !strings.Contains(versionScript, `prefix="${HACKWERK_VERSION_PREFIX:-0.1}"`) ||
		!strings.Contains(versionScript, `git rev-list --count HEAD`) ||
		!strings.Contains(versionScript, `printf '%s.%s\n' "$prefix" "$commit_count"`) {
		t.Fatal("build version is not derived from the monotonically increasing Git commit count")
	}
	for _, workflow := range []string{"ci.yml", "ghcr.yml"} {
		content := repositoryFile(t, ".github", "workflows", workflow)
		if !strings.Contains(content, "fetch-depth: 0") {
			t.Fatalf("%s uses a shallow checkout and could derive the wrong commit version", workflow)
		}
	}
	ghcr := repositoryFile(t, ".github", "workflows", "ghcr.yml")
	if !strings.Contains(ghcr, `build_version="$(sh scripts/version.sh)"`) ||
		!strings.Contains(ghcr, "org.opencontainers.image.version=${{ steps.build.outputs.release }}") ||
		!strings.Contains(ghcr, "at.hackwerk.build.version=${{ steps.build.outputs.version }}") ||
		!strings.Contains(ghcr, `release_version="${GITHUB_REF_NAME#v}"`) {
		t.Fatal("GHCR publishing does not use the per-commit build version")
	}
}

func repositoryFile(t *testing.T, elements ...string) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository contract test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	content, err := os.ReadFile(filepath.Join(append([]string{root}, elements...)...))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}

func section(t *testing.T, document, start, end string) string {
	t.Helper()
	startIndex := strings.Index(document, start)
	if startIndex < 0 {
		t.Fatalf("section start %q not found", start)
	}
	endIndex := strings.Index(document[startIndex+len(start):], end)
	if endIndex < 0 {
		t.Fatalf("section end %q not found", end)
	}
	return document[startIndex : startIndex+len(start)+endIndex]
}
