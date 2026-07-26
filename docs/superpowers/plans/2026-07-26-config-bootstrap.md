# Config Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Laat de Windows-executable bij een ontbrekende `config.json` een volledige, leesbare invulconfiguratie aanmaken en daarna succesvol afsluiten zonder de MCP-server te starten.

**Architecture:** Een nieuwe functie in `internal/config` verzorgt uitsluitend exclusieve bootstrapcreatie en laat de bestaande `Load`-validatie ongemoeid. De testbare `run`-functie in `cmd/mcp-file-tool` krijgt het executablepad en stderr als dependencies, roept bootstrap vóór alle andere initialisatie aan en keert direct terug wanneer de configuratie nieuw is.

**Tech Stack:** Go 1.26, standaardbibliotheek `encoding/json`, `errors`, `io`, `os`, `path/filepath`; bestaande test- en buildtooling.

## Global Constraints

- `config.json` wordt uitsluitend aangemaakt wanneer het bestand ontbreekt.
- Exclusieve creatie voorkomt iedere overschrijving van bestaande inhoud.
- Het template gebruikt `json.MarshalIndent` en bevat de volledige configuratiestructuur.
- Paden en S3-locatievelden zijn leeg; credential-environmentvariabelenamen hebben veilige standaardwaarden.
- De eerste bootstrapstart schrijft alleen een melding naar stderr, retourneert succes en start geen index, rclone of MCP-server.
- Andere I/O-fouten blijven fatale fouten.
- Een bestaande config doorloopt ongewijzigd de huidige lees- en validatielogica.

---

## File Structure

| Pad | Wijziging |
|---|---|
| `internal/config/bootstrap.go` | Nieuwe template en exclusieve `EnsureFile`-creatie |
| `internal/config/bootstrap_test.go` | Structuur-, overschrijf-, concurrency- en fouttests |
| `cmd/mcp-file-tool/main.go` | Testbare run-signatuur en vroege bootstrap-exit |
| `cmd/mcp-file-tool/main_test.go` | Eerste-start- en tweede-startgedrag |
| `README.md` | Automatische eerste-startinstructies |

---

### Task 1: Exclusieve configuratiebootstrap

**Files:**
- Create: `internal/config/bootstrap.go`
- Create: `internal/config/bootstrap_test.go`

**Interfaces:**
- Produces: `config.EnsureFile(path string) (created bool, err error)`
- Consumes later: `cmd/mcp-file-tool.run`

- [ ] **Step 1: Schrijf falende bootstraptests**

Maak tests met letterlijk afgeleide verwachtingen:

```go
func TestEnsureFileCreatesIndentedTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	created, err := EnsureFile(path)
	if err != nil || !created {
		t.Fatalf("EnsureFile() = %v, %v; want true, nil", created, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "{\n  \"source_root\": \"\",\n  \"workspace_root\": \"\",\n  \"index_path\": \"\",\n  \"remote\": {\n    \"endpoint\": \"\",\n    \"region\": \"\",\n    \"bucket\": \"\",\n    \"prefix\": \"\",\n    \"access_key_id_env\": \"MCP_S3_ACCESS_KEY_ID\",\n    \"secret_access_key_env\": \"MCP_S3_SECRET_ACCESS_KEY\"\n  }\n}\n"
	if string(data) != want {
		t.Fatalf("config = %q, want %q", data, want)
	}
}

func TestEnsureFileNeverOverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := EnsureFile(path)
	if err != nil || created {
		t.Fatalf("EnsureFile() = %v, %v; want false, nil", created, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep-me" {
		t.Fatalf("existing config changed to %q", data)
	}
}
```

Voeg daarnaast toe:

- `TestEnsureFileConcurrentCallsCreateExactlyOnce`: start 16 goroutines; exact één resultaat heeft `created == true`, vijftien hebben `false`, geen fout, eind-JSON is geldig.
- `TestEnsureFileReturnsCreateError`: gebruik een pad waarvan de bovenliggende directory ontbreekt; verwacht een niet-nil fout en geen bestand.

- [ ] **Step 2: Bevestig de rode fase**

Run:

```powershell
go test ./internal/config -run TestEnsureFile -v
```

Expected: FAIL met `undefined: EnsureFile`.

- [ ] **Step 3: Implementeer de minimale bootstrapfunctie**

Maak in `bootstrap.go`:

```go
func EnsureFile(path string) (created bool, err error)
```

Gebruik dezelfde private `fileConfig` en `remoteConfig` als `Load`, met:

```go
template := fileConfig{
	Remote: remoteConfig{
		AccessKeyIDEnv:     "MCP_S3_ACCESS_KEY_ID",
		SecretAccessKeyEnv: "MCP_S3_SECRET_ACCESS_KEY",
	},
}
```

Marshal met `json.MarshalIndent(template, "", "  ")` en voeg precies één newline toe. Open met:

```go
os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
```

Behandel `errors.Is(err, os.ErrExist)` als `false, nil`. Houd een `createdByCall`-flag bij; verwijder bij write/sync/close-fouten uitsluitend dit nieuwe gedeeltelijke bestand. Roep achtereenvolgens `Write`, `Sync` en `Close` aan.

- [ ] **Step 4: Verifieer bootstrapgedrag**

Run:

```powershell
gofmt -w internal/config
go test ./internal/config -run TestEnsureFile -v
go test ./internal/config -v
go vet ./internal/config
```

Expected: alle tests PASS en vet exitcode 0.

- [ ] **Step 5: Commit**

```powershell
git add internal/config/bootstrap.go internal/config/bootstrap_test.go
git commit -m "feat: create missing config template"
```

---

### Task 2: Vroege succesvolle exit en documentatie

**Files:**
- Modify: `cmd/mcp-file-tool/main.go`
- Create: `cmd/mcp-file-tool/main_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `config.EnsureFile(path string) (bool, error)`
- Produces: `run(ctx context.Context, executablePath string, stderr io.Writer) error`

- [ ] **Step 1: Schrijf falende opstarttests**

Maak:

```go
func TestRunCreatesConfigAndExitsBeforeServerInitialization(t *testing.T) {
	exeDir := t.TempDir()
	var stderr bytes.Buffer
	err := run(context.Background(), filepath.Join(exeDir, "mcp-file-tool.exe"), &stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.String() != "config.json is aangemaakt; vul het configuratiebestand in en start de applicatie opnieuw.\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(exeDir, "config.json")); err != nil {
		t.Fatalf("config was not created: %v", err)
	}
	// Er staat geen rclone.exe en er bestaan geen source/workspacepaden.
	// Een poging tot verdere initialisatie zou deze test dus laten falen.
}

func TestRunLoadsExistingBootstrapConfigOnSecondStart(t *testing.T) {
	exeDir := t.TempDir()
	executable := filepath.Join(exeDir, "mcp-file-tool.exe")
	if err := run(context.Background(), executable, io.Discard); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), executable, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "source_root is required") {
		t.Fatalf("second run error = %v, want config validation error", err)
	}
}
```

- [ ] **Step 2: Bevestig dat de tests om de juiste reden falen**

Run:

```powershell
go test ./cmd/mcp-file-tool -run TestRun -v
```

Expected: FAIL omdat de bestaande `run` niet de nieuwe signatuur of bootstrap heeft.

- [ ] **Step 3: Implementeer vroege bootstrap-exit**

Wijzig `main`:

```go
func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	if err := run(context.Background(), executable, os.Stderr); err != nil {
		log.Fatal(err)
	}
}
```

Wijzig `run`:

```go
func run(ctx context.Context, executablePath string, stderr io.Writer) error {
	exeDir := filepath.Dir(executablePath)
	configPath := filepath.Join(exeDir, "config.json")
	created, err := config.EnsureFile(configPath)
	if err != nil {
		return fmt.Errorf("ensure config: %w", err)
	}
	if created {
		_, err := fmt.Fprintln(stderr, "config.json is aangemaakt; vul het configuratiebestand in en start de applicatie opnieuw.")
		return err
	}
	cfg, err := config.Load(configPath, exeDir)
	// Bestaande dependency-assemblage blijft hierna ongewijzigd.
```

- [ ] **Step 4: Werk de README bij**

Vervang het buildvoorbeeld dat `config.example.json` handmatig kopieert door:

```powershell
go build -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
Copy-Item C:\pad\naar\rclone.exe .\dist\rclone.exe
.\dist\mcp-file-tool.exe
```

Leg direct eronder uit: de eerste start maakt `config.json`, sluit succesvol af, waarna de gebruiker het bestand invult en de executable opnieuw start.

- [ ] **Step 5: Run volledige verificatie**

Run:

```powershell
gofmt -w cmd/mcp-file-tool
go test ./...
go vet ./...
go build -p 1 -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
git diff --check
```

Expected: alle tests en vet PASS, build exitcode 0 en geen whitespacefouten.

- [ ] **Step 6: Commit**

```powershell
git add cmd/mcp-file-tool/main.go cmd/mcp-file-tool/main_test.go README.md
git commit -m "feat: bootstrap config on first start"
```

---

## Final Acceptance Check

Run:

```powershell
go test ./...
go vet ./...
go build -p 1 -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
git status --short
```

Handmatige rooktest in een lege tijdelijke distributiemap:

1. Start de executable zonder `config.json`.
2. Controleer exitcode `0`, de stderr-melding en de ingesprongen JSON.
3. Wijzig één teken in `config.json`.
4. Start opnieuw en controleer dat de gewijzigde inhoud niet wordt overschreven.
5. Controleer dat de tweede start een validatiefout geeft totdat de config geldig is.
