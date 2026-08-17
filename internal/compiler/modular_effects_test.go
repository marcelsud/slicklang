package compiler_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"

	_ "modernc.org/sqlite"
)

const modularEffectsToken = "TOP_SECRET_MODULAR_EFFECTS_TOKEN"

type modularEffectsConfig struct {
	inputPath    string
	riskURL      string
	databasePath string
	reportPath   string
	nowEpoch     string
}

type modularEffectsSummary struct {
	Processed int
	Accepted  int
	Review    int
	Rejected  int
}

type modularEffectsStoredShipment struct {
	id          string
	destination string
	decision    string
	observedAt  int
}

func TestModularEffectsEndToEnd(t *testing.T) {
	projectPath := filepath.Join("..", "..", "examples", "modular-effects")
	inputPath, err := filepath.Abs(filepath.Join(projectPath, "fixtures", "shipments.json"))
	if err != nil {
		t.Fatal(err)
	}
	riskServer := newModularEffectsRiskServer(t, false)

	type runner struct {
		name string
		run  func(*testing.T, modularEffectsConfig) string
	}
	runners := []runner{{
		name: "interpreter",
		run: func(t *testing.T, config modularEffectsConfig) string {
			setModularEffectsEnv(t, config)
			output, diagnostics, err := compiler.RunPath(projectPath)
			if err != nil {
				t.Fatalf("run interpreter: %v", err)
			}
			assertNoDiagnostics(t, diagnostics)
			return output
		},
	}}
	for _, backend := range []compiler.Backend{compiler.BackendGo, compiler.BackendLLVM} {
		binary := filepath.Join(t.TempDir(), "modular-effects")
		diagnostics, err := compiler.BuildPathBackend(projectPath, binary, backend)
		if err != nil {
			t.Fatalf("build %s modular-effects: %v", backend, err)
		}
		assertNoDiagnostics(t, diagnostics)
		runners = append(runners, runner{
			name: string(backend),
			run: func(t *testing.T, config modularEffectsConfig) string {
				return runModularEffectsNative(t, binary, config)
			},
		})
	}

	for _, runner := range runners {
		t.Run(runner.name, func(t *testing.T) {
			root := t.TempDir()
			config := modularEffectsConfig{
				inputPath:    inputPath,
				riskURL:      riskServer.URL,
				databasePath: filepath.Join(root, "shipments.db"),
				reportPath:   filepath.Join(root, "audit.json"),
				nowEpoch:     "1700000000",
			}
			if output := strings.TrimSpace(runner.run(t, config)); output != "processed=3;accepted=1;review=1;rejected=1" {
				t.Fatalf("summary = %q", output)
			}
			verifyModularEffectsDatabase(t, config.databasePath)
			verifyModularEffectsReport(t, config.reportPath)
		})
	}

	t.Run("adapter failures are translated", func(t *testing.T) {
		failureServer := newModularEffectsRiskServer(t, true)
		tests := []struct {
			name    string
			want    string
			prepare func(*testing.T, modularEffectsConfig)
			config  modularEffectsConfig
		}{
			{
				name: "source",
				want: "source: could not load shipments",
				config: modularEffectsConfig{
					inputPath: filepath.Join(t.TempDir(), "PRIVATE_INPUT_PATH", "missing.json"), riskURL: riskServer.URL,
				},
			},
			{
				name: "risk",
				want: "risk: could not assess shipment",
				config: modularEffectsConfig{
					inputPath: inputPath, riskURL: failureServer.URL,
				},
			},
			{
				name: "store",
				want: "store: could not persist shipment",
				config: modularEffectsConfig{
					inputPath: inputPath, riskURL: riskServer.URL,
				},
				prepare: seedIncompatibleShipmentsTable,
			},
			{
				name: "audit",
				want: "audit: could not write audit report",
				config: modularEffectsConfig{
					inputPath: inputPath, riskURL: riskServer.URL,
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				for _, runner := range runners {
					t.Run(runner.name, func(t *testing.T) {
						config := test.config
						root := t.TempDir()
						config.databasePath = filepath.Join(root, "PRIVATE_DATABASE_PATH.db")
						config.reportPath = filepath.Join(root, "PRIVATE_REPORT_PATH", "audit.json")
						config.nowEpoch = "1700000000"
						if test.name != "audit" {
							config.reportPath = filepath.Join(root, "audit.json")
						}
						if test.prepare != nil {
							test.prepare(t, config)
						}
						output := strings.TrimSpace(runner.run(t, config))
						if output != test.want {
							t.Fatalf("output = %q, want %q", output, test.want)
						}
						for _, secret := range []string{modularEffectsToken, config.inputPath, config.databasePath, config.reportPath, "CREATE TABLE", "INSERT INTO"} {
							if strings.Contains(output, secret) {
								t.Fatalf("output leaked %q: %q", secret, output)
							}
						}
					})
				}
			})
		}
	})
}

func TestModularEffectsFormatFixedPoint(t *testing.T) {
	projectPath := filepath.Join("..", "..", "examples", "modular-effects")
	sources, err := compiler.LoadSources(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		formatted, diagnostics, err := compiler.Format(source)
		if err != nil || len(diagnostics) != 0 {
			t.Fatalf("format %s: diagnostics=%+v err=%v", source.Name, diagnostics, err)
		}
		if formatted != source.Text {
			t.Fatalf("%s is not a format fixed point", source.Name)
		}
	}
}

func newModularEffectsRiskServer(t *testing.T, fail bool) *httptest.Server {
	t.Helper()
	decisions := map[string]string{
		"/shipment-100": "accepted",
		"/shipment-200": "review",
		"/shipment-300": "rejected",
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+modularEffectsToken {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		if fail {
			http.Error(response, "TOP_SECRET_UPSTREAM_FAILURE", http.StatusInternalServerError)
			return
		}
		decision, ok := decisions[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]string{"decision": decision})
	}))
	t.Cleanup(server.Close)
	return server
}

func setModularEffectsEnv(t *testing.T, config modularEffectsConfig) {
	t.Helper()
	for key, value := range modularEffectsEnv(config) {
		t.Setenv(key, value)
	}
}

func modularEffectsEnv(config modularEffectsConfig) map[string]string {
	return map[string]string{
		"MODULAR_EFFECTS_INPUT":      config.inputPath,
		"MODULAR_EFFECTS_RISK_URL":   config.riskURL,
		"MODULAR_EFFECTS_RISK_TOKEN": modularEffectsToken,
		"MODULAR_EFFECTS_DATABASE":   config.databasePath,
		"MODULAR_EFFECTS_REPORT":     config.reportPath,
		"MODULAR_EFFECTS_NOW_EPOCH":  config.nowEpoch,
	}
}

func runModularEffectsNative(t *testing.T, binary string, config modularEffectsConfig) string {
	t.Helper()
	command := exec.Command(binary)
	command.Env = os.Environ()
	for key, value := range modularEffectsEnv(config) {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run native modular-effects: %v: %s", err, output)
	}
	return string(output)
}

func verifyModularEffectsDatabase(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query("SELECT shipment_id, destination, decision, observed_at_epoch FROM shipments ORDER BY shipment_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []modularEffectsStoredShipment
	for rows.Next() {
		var shipment modularEffectsStoredShipment
		if err := rows.Scan(&shipment.id, &shipment.destination, &shipment.decision, &shipment.observedAt); err != nil {
			t.Fatal(err)
		}
		got = append(got, shipment)
	}
	want := []modularEffectsStoredShipment{
		{id: "shipment-100", destination: "NL", decision: "accepted", observedAt: 1700000000},
		{id: "shipment-200", destination: "US", decision: "review", observedAt: 1700000000},
		{id: "shipment-300", destination: "BR", decision: "rejected", observedAt: 1700000000},
	}
	if len(got) != len(want) {
		t.Fatalf("stored shipments = %+v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("stored shipment %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func verifyModularEffectsReport(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var summary modularEffectsSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if summary != (modularEffectsSummary{Processed: 3, Accepted: 1, Review: 1, Rejected: 1}) {
		t.Fatalf("audit summary = %+v", summary)
	}
}

func seedIncompatibleShipmentsTable(t *testing.T, config modularEffectsConfig) {
	t.Helper()
	database, err := sql.Open("sqlite", config.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE shipments (unexpected TEXT)"); err != nil {
		t.Fatal(err)
	}
}
