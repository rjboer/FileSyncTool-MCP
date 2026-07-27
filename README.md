# MCP-bestandsindex

Een native Windows MCP-server die bestanden vanuit een door AI georganiseerde bronmap naar een tijdelijke werkmap kopieert, in JSON indexeert en met rclone naar S3-compatible opslag synchroniseert.

Na succesvolle upload controleert de server ieder bestand met `rclone check`. Alleen een geverifieerde lokale kopie wordt automatisch verwijderd. Het oorspronkelijke relatieve pad en de checksum blijven met status `RemoteOnly` in de index staan.

## Vereisten

- Windows
- Go 1.26 of nieuwer om zelf te bouwen
- `rclone.exe`
- Een S3-compatible endpoint, region, bucket, access-key-ID en secret access key

## Bouwen

```powershell
go build -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
Copy-Item C:\pad\naar\rclone.exe .\dist\rclone.exe
.\dist\mcp-file-tool.exe
```

De eerste start maakt automatisch een volledige `config.json` naast `mcp-file-tool.exe`, meldt dat het bestand moet worden ingevuld en sluit succesvol af. Vul de configuratie in en start de executable opnieuw. Een bestaande `config.json` wordt nooit door de bootstrap overschreven.

`config.json` en `rclone.exe` moeten naast `mcp-file-tool.exe` staan. De server schrijft nooit protocoldata buiten stdout; start- en foutmeldingen gaan naar stderr.

## Configuratie

```json
{
  "source_root": "C:\\AI-organisatie",
  "workspace_root": "C:\\MCP-werkmap",
  "index_path": "C:\\MCP-werkmap\\file-index.json",
  "remote": {
    "endpoint": "https://s3.example.com",
    "region": "eu-central-1",
    "bucket": "mijn-bucket",
    "prefix": "",
    "access_key_id_env": "MCP_S3_ACCESS_KEY_ID",
    "secret_access_key_env": "MCP_S3_SECRET_ACCESS_KEY"
  }
}
```

- `source_root` bevat de door AI georganiseerde bestanden.
- `workspace_root` is de tijdelijke lokale stagingmap. Deze mag niet overlappen met `source_root`.
- `index_path` wijst naar de persistente JSON-index.
- `prefix` is optioneel. Met `prefix: "archief"` komt `KlantA/a.pdf` terecht als `s3://mijn-bucket/archief/KlantA/a.pdf`.

Bron- en werkmap moeten vóór het starten bestaan. Ontbrekende submappen voor toegevoegde bestanden worden automatisch aangemaakt.

### Credentials

Zet de credentials in de environment van het proces dat de MCP-server start:

```powershell
$env:MCP_S3_ACCESS_KEY_ID = "jouw-access-key-id"
$env:MCP_S3_SECRET_ACCESS_KEY = "jouw-secret-access-key"
```

De namen van deze environment variables staan in `config.json`; de geheime waarden zelf niet. De server filtert beide waarden uit rclone-fouten.

## MCP-client

Configureer de gebouwde executable als stdio-server. Een generiek configuratievoorbeeld:

```json
{
  "mcpServers": {
    "file-index": {
      "command": "C:\\Apps\\mcp-file-tool\\mcp-file-tool.exe",
      "args": [],
      "env": {
        "MCP_S3_ACCESS_KEY_ID": "jouw-access-key-id",
        "MCP_S3_SECRET_ACCESS_KEY": "jouw-secret-access-key"
      }
    }
  }
}
```

Gebruik bij voorkeur de secret-store of environment-configuratie van de betreffende MCP-client in plaats van credentials letterlijk in dit JSON-voorbeeld te bewaren.

## Tools

### `add_file`

Invoer:

```json
{
  "path": "C:\\AI-organisatie\\KlantA\\rapporten\\juli.pdf"
}
```

De tool:

1. controleert dat het een reguliere file binnen `source_root` is;
2. bepaalt extensie en xxHash64;
3. retourneert de bestaande registratie wanneer extensie en checksum al bekend zijn;
4. kopieert nieuwe inhoud geverifieerd naar `workspace_root`;
5. bewaart de relatieve mappenstructuur;
6. schrijft de entry direct als `OnFileNotRemote` naar de JSON-index.

Een bestaand doelbestand met andere inhoud wordt nooit overschreven.

### `add_directory`

Verwerkt recursief één directory binnen `source_root`. `source_root` zelf is
ook toegestaan.

Invoer:

```json
{
  "path": "C:\\AI-organisatie\\KlantA"
}
```

De tool doorloopt de directory in vaste volgorde en gebruikt per regulier
bestand dezelfde padcontrole, xxHash64-identiteit, geverifieerde kopie en
directe indexopslag als `add_file`.

| Bekende status voor type + checksum | Actie op het bronbestand |
| --- | --- |
| Niet bekend | Kopiëren en indexeren als `OnFileNotRemote`. |
| `OnFileNotRemote` | Niet opnieuw toevoegen; bron behouden. |
| `OnFileAndRemote` | Niet opnieuw toevoegen; bron behouden. |
| `RemoteOnly` | Identiteit opnieuw controleren en alleen het bronbestand verwijderen. |

Een fout bij één bestand behoudt het bronbestand, wordt gelogd en verhindert
niet dat latere bestanden worden verwerkt. Alleen een ongeldig startpad,
onbeschikbare foutlogging, een fout in het log zelf of annulering stopt de
batch. Symlinks, junctions en andere niet-reguliere entries worden niet
gevolgd of verwerkt. Als het live `index_path` onder `source_root` staat,
wordt ook dat bestand altijd overgeslagen.

Iedere geaccepteerde aanroep maakt een afzonderlijk CSV-log onder:

```text
<workspace_root>\logs\add_directory\
```

Het gestructureerde resultaat bevat `scanned`, `added`,
`retained_on_file_not_remote`, `retained_on_file_and_remote`,
`removed_remote_only`, `skipped`, `failed` en het absolute
`error_log_path`.

De tool verwijdert nooit directory's, ook niet wanneer die na het verwijderen
van `RemoteOnly`-bestanden leeg zijn. De bestaande mappenstructuur blijft dus
als raamwerk aanwezig. `add_directory` start geen upload; roep daarvoor
`sync_files` afzonderlijk aan.

### `sync_files`

Heeft geen parameters. Eén aanroep verwerkt alle entries met `OnFileNotRemote`:

1. `rclone copy` uploadt de geselecteerde relatieve paden.
2. `rclone check --one-way --combined` controleert ieder pad.
3. Een match wordt direct `OnFileAndRemote`.
4. De lokale stagingkopie wordt verwijderd.
5. De entry wordt direct `RemoteOnly`.

Het resultaat bevat aantallen voor geselecteerd, geüpload, geverifieerd, verwijderd, overgeslagen en mislukt, plus een fout per mislukt pad.

## Statussen en herstel

- `OnFileNotRemote`: lokale stagingkopie bestaat en is nog niet remote geverifieerd.
- `OnFileAndRemote`: remote kopie is geverifieerd, maar lokale verwijdering of de laatste statuswrite moet nog worden afgerond.
- `RemoteOnly`: remote kopie is geverifieerd en de lokale stagingkopie is verwijderd.

Bij een upload- of verificatiefout blijft het lokale bestand staan. Een volgende `sync_files` probeert `OnFileNotRemote` opnieuw. Achtergebleven `OnFileAndRemote`-entries worden zonder nieuwe upload lokaal opgeruimd.

Als een bestaande index corrupt is, stopt de server met een fout. Herstel of vervang die index bewust; de server voert alleen een initiële scan uit wanneer het indexbestand werkelijk ontbreekt.

## Ontwikkeling en tests

```powershell
go test ./...
go vet ./...
go build -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
```

De echte rclone-roundtriptest is opt-in en gebruikt een tijdelijke lokale remote:

```powershell
$env:MCP_TEST_RCLONE_EXE = (Resolve-Path .\rclone.exe).Path
go test ./internal/syncer -run TestLocalRcloneRoundTrip -v
Remove-Item Env:MCP_TEST_RCLONE_EXE
```

Zonder `MCP_TEST_RCLONE_EXE` wordt deze ene test overgeslagen. Een echte S3-smoketest moet een tijdelijke, afzonderlijke prefix gebruiken.

Commit nooit `config.json`, de lokale index, credentials of gebouwde executables.
