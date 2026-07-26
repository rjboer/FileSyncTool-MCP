# MCP-bestandsindex Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bouw een native Windows MCP stdio-server in Go die AI-georganiseerde bestanden veilig indexeert, naar een tijdelijke werkmap kopieert, met rclone naar S3 synchroniseert en lokale kopieën pas na verificatie verwijdert.

**Architecture:** De executable assembleert kleine packages voor configuratie, JSON-indexopslag, lokale bestandsverwerking, rclone-synchronisatie en de MCP-adapter. Eén gedeelde operationele mutex serialiseert `add_file` en `sync_files`; de indexstore schrijft iedere mutatie atomair. Rclone draait rechtstreeks als child process en levert via een `--combined`-rapport bewijs per bestand voordat lokale data wordt verwijderd.

**Tech Stack:** Go 1.26.5, `github.com/modelcontextprotocol/go-sdk/mcp` v1.6.1, `github.com/cespare/xxhash/v2` v2.3.0, standaardbibliotheek `encoding/json`, `os`, `os/exec`, `path/filepath`, `sync`, en een los meegeleverde `rclone.exe`.

## Global Constraints

- De server draait native op Windows en gebruikt MCP over stdio.
- `config.json` en `rclone.exe` staan standaard naast de gebouwde executable.
- De JSON-index is de primaire waarheid en wordt alleen initieel opgebouwd als hij ontbreekt.
- Iedere toevoeging en iedere afzonderlijke statuswijziging wordt direct atomair opgeslagen.
- De enige statussen zijn `OnFileNotRemote`, `OnFileAndRemote` en `RemoteOnly`.
- Bestandidentiteit is de combinatie van genormaliseerde extensie en xxHash64.
- De relatieve structuur vanaf `source_root` blijft behouden in `workspace_root` en onder de S3-prefix.
- Een lokale kopie wordt uitsluitend na een positieve rclone-controle verwijderd.
- S3-geheimen komen uit environment variables en verschijnen nooit in logs of MCP-resultaten.
- Een corrupte bestaande index veroorzaakt een startfout en nooit een stille herscan.
- Implementeer alleen `add_file` en `sync_files`; voeg geen watcher, database of achtergrondsync toe.

---

## File Structure

| Pad | Verantwoordelijkheid |
|---|---|
| `go.mod`, `go.sum` | Module- en dependency-pinning |
| `.gitignore` | Buildproducten, lokale config, index en tijdelijke bestanden uitsluiten |
| `config.example.json` | Veilige voorbeeldconfiguratie zonder credentials |
| `cmd/mcp-file-tool/main.go` | Executable-locatie bepalen, dependencies assembleren en stdio-server starten |
| `internal/config/config.go` | JSON lezen, paden resolven en alle configuratie valideren |
| `internal/config/config_test.go` | Configuratie- en secretreferentietests |
| `internal/index/model.go` | Indexschema, entries, statussen en statusovergangen |
| `internal/index/store.go` | Laden, initieel bouwen, snapshots en atomische mutaties |
| `internal/index/replace_windows.go` | Atomische Windows-vervanging via `MoveFileExW` |
| `internal/index/store_test.go` | Scan-, corruptie-, uitsluitings- en atomiciteitstests |
| `internal/files/metadata.go` | Extensie en xxHash64 streamend berekenen |
| `internal/files/paths.go` | Veilige relatieve paden en begrensde lege-map-opruiming |
| `internal/files/add.go` | Gecontroleerd kopiëren en `add_file`-domeinlogica |
| `internal/files/add_test.go` | Toevoegen, duplicaten, botsende doelen en foutcleanup |
| `internal/syncer/runner.go` | Testbare `os/exec`-adapter zonder shell |
| `internal/syncer/rclone.go` | Tijdelijke rclone-config/list/report en copy/check-aanroepen |
| `internal/syncer/rclone_test.go` | Argument-, rapportparser-, cleanup- en geheimtests |
| `internal/syncer/service.go` | Batchselectie, verificatie, statusovergangen en lokale verwijdering |
| `internal/syncer/service_test.go` | Volledige, gedeeltelijke en herstelstatusscenario's |
| `internal/mcpserver/server.go` | MCP-tooldefinities en omzetting naar gestructureerde output |
| `internal/mcpserver/server_test.go` | Toolregistratie en contracttests via een MCP-client |
| `internal/syncer/local_integration_test.go` | Optionele integratietest tegen een lokale rclone-remote |
| `README.md` | Build, configuratie, MCP-installatie, rclone en operationeel herstel |

---

### Task 1: Module, configuratie en veilige padresolutie

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `.gitignore`
- Create: `config.example.json`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Load(path, exeDir string) (config.Resolved, error)`
- Produces: `config.Resolved` met absolute `SourceRoot`, `WorkspaceRoot`, `IndexPath`, `RclonePath` en `Remote`
- Produces: `config.Remote` met `Endpoint`, `Region`, `Bucket`, `Prefix`, `AccessKeyID`, `SecretAccessKey`

- [ ] **Step 1: Initialiseer de module en pin dependencies**

Maak `go.mod`:

```go
module mcp-file-tool

go 1.26.0

require (
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/modelcontextprotocol/go-sdk v1.6.1
)
```

Voer uit:

```powershell
go mod download
```

Verwacht: exitcode 0 en een nieuw `go.sum`.

- [ ] **Step 2: Schrijf falende configuratietests**

Leg in `internal/config/config_test.go` minimaal deze tabeltests vast:

```go
func TestLoadResolvesAndValidates(t *testing.T) {
	t.Setenv("TEST_S3_ID", "id-value")
	t.Setenv("TEST_S3_SECRET", "secret-value")
	// Schrijf een config in t.TempDir(), maak source/workspace en rclone.exe.
	// Verwacht absolute opgeschoonde paden, lege prefix en ingelezen credentials.
}

func TestLoadRejectsMissingSecretEnvironmentVariable(t *testing.T) {
	// Een niet-bestaande secret_access_key_env moet een fout noemen,
	// maar de eventuele access-keywaarde mag niet in err.Error() staan.
}

func TestLoadRejectsWorkspaceInsideSourceRoot(t *testing.T) {
	// Bron en staging mogen niet overlappen: dat voorkomt recursief herimporteren.
}
```

Gebruik een helper die exact de JSON-velden uit de designspecificatie schrijft.

- [ ] **Step 3: Run de tests en bevestig de rode fase**

Run:

```powershell
go test ./internal/config -run TestLoad -v
```

Verwacht: FAIL omdat package `internal/config` nog geen implementatie bevat.

- [ ] **Step 4: Implementeer configuratie laden en valideren**

Definieer in `internal/config/config.go`:

```go
type fileConfig struct {
	SourceRoot    string       `json:"source_root"`
	WorkspaceRoot string       `json:"workspace_root"`
	IndexPath     string       `json:"index_path"`
	Remote        remoteConfig `json:"remote"`
}

type remoteConfig struct {
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	Bucket             string `json:"bucket"`
	Prefix             string `json:"prefix"`
	AccessKeyIDEnv     string `json:"access_key_id_env"`
	SecretAccessKeyEnv string `json:"secret_access_key_env"`
}

type Remote struct {
	Endpoint, Region, Bucket, Prefix string
	AccessKeyID, SecretAccessKey     string
}

type Resolved struct {
	SourceRoot, WorkspaceRoot, IndexPath, RclonePath string
	Remote                                           Remote
}

func Load(path, exeDir string) (Resolved, error)
```

Gebruik `json.Decoder.DisallowUnknownFields`, `filepath.Abs`, `filepath.Clean` en `filepath.Rel`. Vereis bestaande directories voor bron en workspace, een reguliere `rclone.exe`, een `.json` indexpad, een niet-lege endpoint/region/bucket en niet-lege benoemde credential-environment variables. Normaliseer prefix met `/`, zonder voor- of achterliggende slash. Weiger gelijke of overlappende bron- en werkmappen.

- [ ] **Step 5: Voeg veilige voorbeeldbestanden toe**

Maak `.gitignore`:

```gitignore
/config.json
/file-index.json
/*.exe
/*.tmp
coverage.out
```

Maak `config.example.json` met de exacte velden uit de spec, lege `prefix`, en alleen environmentvariabelenamen.

- [ ] **Step 6: Run en formatteer**

Run:

```powershell
gofmt -w internal/config
go test ./internal/config -v
go vet ./internal/config
```

Verwacht: alle tests PASS en `go vet` exitcode 0.

- [ ] **Step 7: Commit**

```powershell
git add go.mod go.sum .gitignore config.example.json internal/config
git commit -m "feat: add validated runtime configuration"
```

---

### Task 2: Versieerbare JSON-index en eenmalige scan

**Files:**
- Create: `internal/index/model.go`
- Create: `internal/index/store.go`
- Create: `internal/index/replace_windows.go`
- Create: `internal/index/store_test.go`

**Interfaces:**
- Consumes: absolute `workspaceRoot` en `indexPath` uit `config.Resolved`
- Produces: `index.Status`, `index.Entry`, `index.Document`
- Produces: `index.Open(indexPath, workspaceRoot string, fingerprint FingerprintFunc) (*Store, error)`
- Produces: `(*Store).Snapshot() Document`, `FindIdentity`, `Add`, en `Transition`

- [ ] **Step 1: Schrijf falende model- en storetests**

Definieer tests voor:

```go
func TestTransitionAllowsOnlyLifecycleEdges(t *testing.T) {
	// Toegestaan: OnFileNotRemote -> OnFileAndRemote -> RemoteOnly.
	// Geweigerd: RemoteOnly -> OnFileNotRemote en direct NotRemote -> RemoteOnly.
}

func TestOpenBuildsMissingIndexOnce(t *testing.T) {
	// Twee workspacefiles leveren Version 1 en twee OnFileNotRemote entries op.
	// indexPath en ".mcp-file-*.tmp" worden niet opgenomen.
}

func TestOpenRejectsCorruptExistingIndex(t *testing.T) {
	// Bestaande "{broken" JSON levert fout en blijft byte-voor-byte bestaan.
}

func TestMutationPersistsValidJSONImmediately(t *testing.T) {
	// Na Add en na iedere Transition is het indexbestand direct opnieuw leesbaar.
}
```

- [ ] **Step 2: Bevestig dat de tests falen**

Run:

```powershell
go test ./internal/index -v
```

Verwacht: FAIL omdat types en store ontbreken.

- [ ] **Step 3: Implementeer schema en overgangsregels**

Maak in `model.go`:

```go
type Status string

const (
	OnFileNotRemote Status = "OnFileNotRemote"
	OnFileAndRemote Status = "OnFileAndRemote"
	RemoteOnly      Status = "RemoteOnly"
)

type Entry struct {
	RelativePath string `json:"relative_path"`
	FileName     string `json:"file_name"`
	FileType     string `json:"file_type"`
	Checksum     string `json:"checksum"`
	Status       Status `json:"status"`
}

type Document struct {
	Version int     `json:"version"`
	Files   []Entry `json:"files"`
}

func ValidateTransition(from, to Status) error
```

Valideer bij laden: versie exact `1`, unieke `relative_path`, geldige status, alleen slashgescheiden veilige relatieve paden, en checksums met prefix `xxh64:` plus 16 hextekens.

- [ ] **Step 4: Implementeer store en atomisch schrijven**

Definieer:

```go
type FingerprintFunc func(path string) (fileType, checksum string, err error)

type Store struct {
	mu   sync.RWMutex
	path string
	doc  Document
}

func Open(indexPath, workspaceRoot string, fingerprint FingerprintFunc) (*Store, error)
func (s *Store) Snapshot() Document
func (s *Store) FindIdentity(fileType, checksum string) (Entry, bool)
func (s *Store) Add(entry Entry) error
func (s *Store) Transition(relativePath string, from, to Status) error
```

`writeAtomic` maakt met `os.CreateTemp(filepath.Dir(path), ".mcp-file-index-*.tmp")` een buur-bestand, encodeert ingesprongen JSON en roept `Sync` en `Close` aan. `replace_windows.go` vervangt daarna het doel met `MoveFileExW` en de flags `MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH`. Geen enkele fout mag de laatste geldige index vernietigen; een mislukt tijdelijk bestand wordt verwijderd.

- [ ] **Step 5: Implementeer de eenmalige scan**

Als `os.Stat(indexPath)` `os.ErrNotExist` oplevert, scan met `filepath.WalkDir`. Neem alleen reguliere files op. Sla indexpad, `.mcp-file-index-*.tmp`, `.mcp-copy-*.tmp`, `.mcp-rclone-*.conf`, `.mcp-rclone-files-*.txt` en `.mcp-rclone-check-*.txt` over. Sorteer entries lexicografisch op slashgenormaliseerd relatief pad voordat versie 1 wordt geschreven.

- [ ] **Step 6: Verifieer packagegedrag**

Run:

```powershell
gofmt -w internal/index
go test ./internal/index -race -v
go vet ./internal/index
```

Verwacht: PASS, inclusief race detector.

- [ ] **Step 7: Commit**

```powershell
git add internal/index
git commit -m "feat: add atomic JSON file index"
```

---

### Task 3: Metadata, veilige paden en `add_file`-domeinservice

**Files:**
- Create: `internal/files/metadata.go`
- Create: `internal/files/paths.go`
- Create: `internal/files/add.go`
- Create: `internal/files/add_test.go`

**Interfaces:**
- Consumes: `index.Store`, `config.Resolved`, en een gedeelde `*sync.Mutex`
- Produces: `files.Fingerprint(path string) (fileType, checksum string, err error)`
- Produces: `files.NewAddService(sourceRoot, workspaceRoot string, store *index.Store, opMu *sync.Mutex) *AddService`
- Produces: `(*AddService).Add(ctx context.Context, sourcePath string) (AddResult, error)`
- Produces: `files.RemoveEmptyParents(path, workspaceRoot string) error`

- [ ] **Step 1: Schrijf falende tests voor metadata en toevoegen**

Gebruik tabletests met tijdelijke mappen:

```go
func TestFingerprintUsesLowercaseExtensionAndXXHash64(t *testing.T) {
	// "Report.PDF" met bytes "abc" -> type ".pdf" en bekende xxh64-vector.
}

func TestAddPreservesRelativeTreeAndPersistsEntry(t *testing.T) {
	// source/KlantA/rapport.pdf wordt workspace/KlantA/rapport.pdf.
	// Result.New is true en status is OnFileNotRemote.
}

func TestAddReturnsKnownIdentityWithoutSecondCopy(t *testing.T) {
	// Dezelfde bytes en extensie op een tweede sourcepad geven de bestaande entry.
}

func TestAddRejectsPathOutsideSourceRootAndConflictingDestination(t *testing.T) {
	// Geen enkel bestaand doelbestand wordt gewijzigd.
}
```

- [ ] **Step 2: Run de rode tests**

Run:

```powershell
go test ./internal/files -v
```

Verwacht: FAIL wegens ontbrekende implementatie.

- [ ] **Step 3: Implementeer metadata en veilige padhelpers**

`Fingerprint` opent de file eenmaal, streamt hem door `xxhash.New()`, gebruikt `strings.ToLower(filepath.Ext(path))`, en retourneert:

```go
fmt.Sprintf("xxh64:%016x", digest.Sum64())
```

Maak een helper:

```go
func RelativeWithin(root, path string) (string, error)
```

Los symlinks/junctions van root en bron op met `filepath.EvalSymlinks`, gebruik `filepath.Rel`, en weiger `".."`, absolute resultaten of een resultaat gelijk aan `"."`. Zet indexpaden met `filepath.ToSlash` om.

- [ ] **Step 4: Implementeer atomisch en geverifieerd kopiëren**

Definieer:

```go
type AddResult struct {
	Entry index.Entry `json:"entry"`
	New   bool        `json:"new"`
}
```

Houd `opMu` vast gedurende de volledige Add-operatie. Controleer een reguliere bronfile, fingerprint, bekende identiteit en het definitieve doel. Kopieer via `io.Copy` naar `os.CreateTemp(targetDir, ".mcp-copy-*.tmp")`, roep `Sync` en `Close` aan, fingerprint de tijdelijke kopie, vergelijk type/checksum, en rename pas daarna. Bij een mislukte `store.Add` verwijder uitsluitend de kopie die deze aanroep zelf heeft gemaakt.

- [ ] **Step 5: Implementeer begrensd opruimen van lege mappen**

`RemoveEmptyParents(fileDir, workspaceRoot)` verwijdert alleen lege directories vanaf de ouder van een verwijderd bestand omhoog. Stop vóór `workspaceRoot`, bij de eerste niet-lege map of bij iedere padvalidatiefout.

- [ ] **Step 6: Run packageverificatie**

Run:

```powershell
gofmt -w internal/files
go test ./internal/files -race -v
go test ./internal/index ./internal/files -cover
go vet ./internal/files
```

Verwacht: alle tests PASS; index en files hebben betekenisvolle branchdekking voor fouten.

- [ ] **Step 7: Commit**

```powershell
git add internal/files
git commit -m "feat: add verified file ingestion"
```

---

### Task 4: Rclone-procesadapter en gecontroleerde S3-verificatie

**Files:**
- Create: `internal/syncer/runner.go`
- Create: `internal/syncer/rclone.go`
- Create: `internal/syncer/rclone_test.go`

**Interfaces:**
- Consumes: `config.Resolved` met rclonepad, workspace en remotegegevens
- Produces: `syncer.CommandRunner.Run(ctx, executable string, args, env []string) (CommandResult, error)`
- Produces: `syncer.Remote.Copy(ctx, paths) error`
- Produces: `syncer.Remote.Check(ctx, paths) (CheckReport, error)`
- Produces: `syncer.NewRcloneRemote(cfg config.Resolved, runner CommandRunner) *RcloneRemote`

- [ ] **Step 1: Schrijf falende runner- en rclonetests**

Maak een fake runner die alle argumenten bewaart en vooraf ingestelde output geeft. Test:

```go
func TestRcloneCopyUsesFilesFromRawAndNoShell(t *testing.T) {
	// Executable is exact cfg.RclonePath; args bevatten copy, workspace,
	// mcp-s3:bucket/prefix, --files-from-raw en --config.
}

func TestRcloneCheckParsesCombinedReportPerFile(t *testing.T) {
	// "= a.txt", "+ b.txt", "* c.txt", "! d.txt" worden respectievelijk
	// match, missing, different en error.
}

func TestRcloneTemporaryFilesAreRemovedAndErrorsAreRedacted(t *testing.T) {
	// Na succes en fout bestaan config/list/report niet meer.
	// Geen access key of secret komt in de geretourneerde fout voor.
}
```

- [ ] **Step 2: Bevestig de rode fase**

Run:

```powershell
go test ./internal/syncer -run "TestRclone" -v
```

Verwacht: FAIL omdat runner en remote ontbreken.

- [ ] **Step 3: Implementeer de directe procesrunner**

Definieer:

```go
type CommandResult struct {
	Stdout string
	Stderr string
}

type CommandRunner interface {
	Run(ctx context.Context, executable string, args, env []string) (CommandResult, error)
}

type ExecRunner struct{}
```

`ExecRunner.Run` gebruikt uitsluitend `exec.CommandContext(ctx, executable, args...)`, zet `cmd.Env = append(os.Environ(), env...)`, en vangt stdout/stderr afzonderlijk op. Gebruik geen `cmd.exe`, PowerShell of samengestelde commandostring.

- [ ] **Step 4: Implementeer tijdelijke rclone-input**

Definieer:

```go
type CheckState string

const (
	Match      CheckState = "match"
	Missing    CheckState = "missing"
	Different  CheckState = "different"
	CheckError CheckState = "error"
)

type CheckReport struct {
	Files map[string]CheckState
}

type Remote interface {
	Copy(context.Context, []string) error
	Check(context.Context, []string) (CheckReport, error)
}

type RcloneRemote struct {
	workspaceRoot string
	rclonePath    string
	remoteName    string
	remoteConfig  string
	destination   string
	runner        CommandRunner
}
```

Schrijf tijdelijke bestanden onder `workspaceRoot` met namen uit de globale uitsluitingslijst. Gebruik slashpaden en `--files-from-raw`. Het tijdelijke configformaat is:

```ini
[mcp-s3]
type = s3
provider = Other
env_auth = false
access_key_id = <runtime value>
secret_access_key = <runtime value>
region = <configured region>
endpoint = <configured endpoint>
```

`NewRcloneRemote` vult `remoteName` met `mcp-s3`, bouwt `remoteConfig` uit `config.Resolved` en zet `destination` op `mcp-s3:<bucket[/prefix]>`. Open tijdelijke config met permissie `0600`, sluit vóór rclone start en verwijder via `defer`.

- [ ] **Step 5: Implementeer copy en check**

Copy-argumenten:

```text
copy <workspaceRoot> mcp-s3:<bucket[/prefix]> --files-from-raw <list> --config <config> --no-traverse
```

Check-argumenten:

```text
check <workspaceRoot> mcp-s3:<bucket[/prefix]> --files-from-raw <list> --config <config> --one-way --combined <report>
```

Parse iedere niet-lege rapportregel exact als `<symbool><spatie><pad>`: `=` match, `+` ontbreekt op bestemming, `*` verschilt en `!` fout. Een gevraagd pad zonder rapportregel krijgt `CheckError`. Redacteer access-key-ID en secret uit alle runnerfouten en stderr voordat een fout wordt teruggegeven.

- [ ] **Step 6: Run packageverificatie**

Run:

```powershell
gofmt -w internal/syncer
go test ./internal/syncer -race -run "TestRclone" -v
go vet ./internal/syncer
```

Verwacht: PASS en geen tijdelijke bestanden na tests.

- [ ] **Step 7: Commit**

```powershell
git add internal/syncer/runner.go internal/syncer/rclone.go internal/syncer/rclone_test.go
git commit -m "feat: add verified rclone transport"
```

---

### Task 5: `sync_files`-domeinservice en herstelgedrag

**Files:**
- Create: `internal/syncer/service.go`
- Create: `internal/syncer/service_test.go`

**Interfaces:**
- Consumes: `index.Store`, `syncer.Remote`, workspacepad en gedeelde `*sync.Mutex`
- Produces: `syncer.NewService(workspaceRoot string, store *index.Store, remote Remote, opMu *sync.Mutex) *Service`
- Produces: `(*Service).Sync(ctx context.Context) SyncResult`

- [ ] **Step 1: Schrijf falende status- en hersteltests**

Maak een fake Remote met instelbare Copy-fout en per-pad CheckState. Test:

```go
func TestSyncVerifiedFilesTransitionAndDelete(t *testing.T) {
	// Twee matches: beide doorlopen OnFileAndRemote -> RemoteOnly,
	// lokale files verdwijnen en resultaat telt 2 verified/deleted.
}

func TestSyncKeepsUnverifiedFilesLocal(t *testing.T) {
	// Missing/Different/Error blijven OnFileNotRemote en fysiek aanwezig.
}

func TestSyncKeepsOnFileAndRemoteWhenRemoveFails(t *testing.T) {
	// Een niet-verwijderbare lokale kopie behoudt OnFileAndRemote.
}

func TestSyncRetriesCleanupWithoutUploadingOnFileAndRemote(t *testing.T) {
	// Bestaande OnFileAndRemote entry wordt verwijderd en RemoteOnly;
	// fake Remote ontvangt dit pad niet in Copy.
}

func TestSyncChecksPartialCopyResults(t *testing.T) {
	// Ook bij een batch-copyfout wordt Check uitgevoerd; alleen Match-items
	// mogen worden verwijderd, overige blijven veilig lokaal.
}
```

- [ ] **Step 2: Run de rode tests**

Run:

```powershell
go test ./internal/syncer -run "TestSync" -v
```

Verwacht: FAIL omdat `Service` ontbreekt.

- [ ] **Step 3: Implementeer resultaattypes en selectie**

Definieer:

```go
type FileFailure struct {
	RelativePath string `json:"relative_path"`
	Error        string `json:"error"`
}

type SyncResult struct {
	Selected, Uploaded, Verified, Deleted, Skipped, Failed int
	Failures []FileFailure `json:"failures,omitempty"`
}
```

Houd `opMu` gedurende de volledige sync vast. Neem een snapshot, selecteer `OnFileNotRemote` voor upload en `OnFileAndRemote` voor cleanup. Sorteer beide groepen op relatief pad zodat tests en output deterministisch zijn.

- [ ] **Step 4: Implementeer gecontroleerde statusovergangen**

Roep `remote.Copy` éénmaal aan voor alle `OnFileNotRemote`-paden en daarna `remote.Check` voor dezelfde lijst, ook als Copy een gedeeltelijke fout meldt. Alleen `Match`:

1. `store.Transition(path, OnFileNotRemote, OnFileAndRemote)`;
2. lokale file met een opnieuw gevalideerd pad onder workspace verwijderen;
3. `files.RemoveEmptyParents`;
4. `store.Transition(path, OnFileAndRemote, RemoteOnly)`.

Verwijder bij iedere storefout niets verder voor dat bestand. Voor bestaande `OnFileAndRemote`-entries voer alleen stappen 2–4 uit. Een ontbrekende lokale file bij `OnFileAndRemote` mag als reeds verwijderd worden beschouwd en naar `RemoteOnly` gaan; een ontbrekende file bij `OnFileNotRemote` is een fout.

- [ ] **Step 5: Maak tellingen en fouten exact**

`Selected` is het aantal `OnFileNotRemote`-entries bij start. `Uploaded` is `Selected` wanneer Copy exitcode 0 heeft; bij batchfout is het aantal onbekend en blijft `0`. `Verified` telt `Match`; `Deleted` telt succesvolle verwijdering of aantoonbaar reeds ontbrekende `OnFileAndRemote` files; `Skipped` telt non-match checks; `Failed` is het aantal unieke paden met een operationele fout. Voeg maximaal één geredigeerde `FileFailure` per pad toe.

- [ ] **Step 6: Run alle syncertests**

Run:

```powershell
gofmt -w internal/syncer
go test ./internal/syncer -race -v
go test ./internal/syncer -cover
go vet ./internal/syncer
```

Verwacht: PASS; alle drie statuswaarden en gedeeltelijke batchfouten zijn gedekt.

- [ ] **Step 7: Commit**

```powershell
git add internal/syncer/service.go internal/syncer/service_test.go
git commit -m "feat: add recoverable batch synchronization"
```

---

### Task 6: MCP stdio-adapter en executable

**Files:**
- Create: `internal/mcpserver/server.go`
- Create: `internal/mcpserver/server_test.go`
- Create: `cmd/mcp-file-tool/main.go`

**Interfaces:**
- Consumes: `files.AddService` en `syncer.Service`
- Produces: `mcpserver.New(add *files.AddService, sync *syncer.Service) *mcp.Server`
- Produces: MCP-tools `add_file` en `sync_files`

- [ ] **Step 1: Schrijf falende MCP-contracttests**

Start de server in-process met de SDK-testtransport of gekoppelde in-memory transports. Test:

```go
func TestServerListsExpectedTools(t *testing.T) {
	// Exact twee toolnamen: add_file en sync_files.
}

func TestAddFileToolRequiresOnlyPathAndReturnsStructuredResult(t *testing.T) {
	// CallTool met {"path": source}; output decodeert naar files.AddResult.
}

func TestSyncFilesToolAcceptsEmptyObjectAndReturnsCounts(t *testing.T) {
	// CallTool met {} levert syncer.SyncResult zonder vrije secretbevattende tekst.
}
```

- [ ] **Step 2: Run de rode contracttests**

Run:

```powershell
go test ./internal/mcpserver -v
```

Verwacht: FAIL omdat de MCP-serveradapter ontbreekt.

- [ ] **Step 3: Implementeer de twee MCP-tools**

Definieer:

```go
type AddFileInput struct {
	Path string `json:"path" jsonschema:"absolute path to a regular file inside source_root"`
}

type SyncFilesInput struct{}
```

Maak de server met:

```go
server := mcp.NewServer(
	&mcp.Implementation{Name: "mcp-file-tool", Version: "1.0.0"},
	nil,
)
```

Registreer met `mcp.AddTool`. Handlers hebben de officiële generieke vorm:

```go
func(ctx context.Context, req *mcp.CallToolRequest, input AddFileInput) (*mcp.CallToolResult, files.AddResult, error)
```

en:

```go
func(ctx context.Context, req *mcp.CallToolRequest, input SyncFilesInput) (*mcp.CallToolResult, syncer.SyncResult, error)
```

Geef domeinfouten als toolfout terug zonder absolute credentialwaarden. Schrijf operationele logs uitsluitend naar stderr, omdat stdout voor MCP gereserveerd is.

- [ ] **Step 4: Assembleer de applicatie in main**

`main.go`:

1. bepaalt `exeDir` met `os.Executable`;
2. laadt `<exeDir>/config.json`;
3. opent/bouwt de store met `files.Fingerprint`;
4. maakt één `opMu := &sync.Mutex{}`;
5. bouwt AddService, ExecRunner, RcloneRemote en SyncService;
6. bouwt de MCP-server;
7. draait `server.Run(context.Background(), &mcp.StdioTransport{})`;
8. schrijft start- en fatale fouten alleen naar stderr.

- [ ] **Step 5: Run MCP- en buildverificatie**

Run:

```powershell
gofmt -w internal/mcpserver cmd/mcp-file-tool
go test ./internal/mcpserver -race -v
go test ./... -race
go vet ./...
go build -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
```

Verwacht: tests PASS, vet exitcode 0 en `dist\mcp-file-tool.exe` bestaat.

- [ ] **Step 6: Commit**

```powershell
git add internal/mcpserver cmd/mcp-file-tool
git commit -m "feat: expose file index over MCP stdio"
```

---

### Task 7: Lokale rclone-integratietest en gebruikersdocumentatie

**Files:**
- Create: `internal/syncer/local_integration_test.go`
- Create: `README.md`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: gebouwde domeinservices en een lokaal beschikbare `rclone.exe`
- Produces: reproduceerbare Windows-build- en configuratie-instructies

- [ ] **Step 1: Schrijf een opt-in lokale rclone-integratietest**

De test slaat over tenzij `MCP_TEST_RCLONE_EXE` naar een bestaande executable wijst:

```go
func TestLocalRcloneRoundTrip(t *testing.T) {
	rclone := os.Getenv("MCP_TEST_RCLONE_EXE")
	if rclone == "" {
		t.Skip("set MCP_TEST_RCLONE_EXE to run the rclone integration test")
	}
	// Gebruik temp source/workspace/remote.
	// Instantieer binnen package syncer een RcloneRemote met:
	// remoteName "mcp-local", config "[mcp-local]\ntype = local\n",
	// en bestemming "mcp-local:<slash-genormaliseerde temp remote>".
	// Voeg nested/KlantA/a.txt toe, sync, controleer remote bytes,
	// controleer lokale afwezigheid en indexstatus RemoteOnly.
}
```

Plaats de test in package `syncer`, zodat hij de interne velden van `RcloneRemote` gecontroleerd kan instellen zonder een productie-API voor willekeurige backends toe te voegen. Productiecode blijft uitsluitend S3 uit `config.Resolved` bouwen.

- [ ] **Step 2: Run standaard- en optionele integratietests**

Run:

```powershell
go test ./... -race
$env:MCP_TEST_RCLONE_EXE = (Resolve-Path '.\rclone.exe').Path
go test ./internal/syncer -run TestLocalRcloneRoundTrip -v
Remove-Item Env:MCP_TEST_RCLONE_EXE
```

Verwacht: de eerste opdracht PASS met eventueel één SKIP; als `rclone.exe` aanwezig is, de tweede opdracht PASS.

- [ ] **Step 3: Schrijf README met exact operationeel gebruik**

Documenteer:

- vereisten: Windows, `rclone.exe` naast `mcp-file-tool.exe`, S3 endpoint/bucket/region;
- buildcommando `go build -o dist/mcp-file-tool.exe ./cmd/mcp-file-tool`;
- `config.example.json` kopiëren naar `config.json`;
- PowerShell-voorbeelden voor `MCP_S3_ACCESS_KEY_ID` en `MCP_S3_SECRET_ACCESS_KEY`;
- MCP-clientconfig met `command` naar de absolute executable en `"args": []`;
- schema en betekenis van beide tools;
- statuslevenscyclus;
- waarom lokale verwijdering pas na `rclone check` gebeurt;
- herstel bij `OnFileAndRemote`, corrupte index en ontbrekende credentials;
- waarschuwing dat `config.json`, index en secrets niet in Git horen.

- [ ] **Step 4: Voeg buildoutput aan `.gitignore` toe**

Voeg toe:

```gitignore
/dist/
```

- [ ] **Step 5: Eindverificatie**

Run:

```powershell
gofmt -w .
go mod tidy
go test ./... -race -coverprofile=coverage.out
go vet ./...
go build -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
git diff --check
git status --short
```

Verwacht: tests en vet PASS, build exitcode 0, geen whitespacefouten, en alleen de bedoelde taakbestanden gewijzigd.

- [ ] **Step 6: Commit**

```powershell
git add README.md .gitignore internal/syncer/local_integration_test.go go.mod go.sum
git commit -m "docs: add Windows setup and rclone verification"
```

---

## Final Acceptance Check

Voer na alle commits uit:

```powershell
go test ./... -race
go vet ./...
go build -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
git log --oneline --decorate -8
git status --short
```

Controleer handmatig met een tijdelijke S3-testprefix:

1. Voeg een nested bronfile via `add_file` toe.
2. Bevestig `OnFileNotRemote` en de lokale stagingkopie.
3. Roep `sync_files` aan.
4. Bevestig dezelfde relatieve key onder bucket/prefix.
5. Bevestig dat de lokale kopie weg is en de index `RemoteOnly` bewaart.
6. Roep `sync_files` opnieuw aan en bevestig een lege, idempotente run.
7. Verwijder de tijdelijke S3-testobjecten buiten de MCP-tool om.

De feature is pas gereed als alle tien acceptatiecriteria uit `docs/superpowers/specs/2026-07-26-mcp-file-index-design.md` aantoonbaar zijn afgedekt.
