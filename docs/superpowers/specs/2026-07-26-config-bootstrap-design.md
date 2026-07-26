# Config-bootstrap — ontwerp

## Doel

De executable moet zichzelf bij de eerste start gebruiksklaar maken door naast zichzelf een leesbare `config.json` met de volledige configuratiestructuur aan te maken. De MCP-server start pas bij een volgende start met een bestaande, geldige configuratie.

## Opstartgedrag

Bij het starten bepaalt de applicatie eerst de eigen executablemap en het pad `<exe-map>\config.json`.

De opstartvolgorde is:

1. Probeer `config.json` te openen.
2. Als openen `os.ErrNotExist` oplevert:
   - maak een nieuwe Go-configuratiestructuur met lege of veilige standaardwaarden;
   - zet die structuur met `json.MarshalIndent` om naar leesbare JSON;
   - schrijf de JSON via exclusieve bestandscreatie;
   - meld via stderr dat het bestand is aangemaakt en moet worden ingevuld;
   - sluit succesvol af zonder index, rclone of MCP-server te starten.
3. Als openen om een andere reden mislukt, retourneer een fout en sluit met een foutstatus af.
4. Als het bestand bestaat:
   - lees de volledige inhoud;
   - zet die met `json.Unmarshal` om naar de Go-configuratiestructuur;
   - valideer alle configuratie zoals in de huidige implementatie;
   - start uitsluitend bij geldige configuratie de index en MCP-server.

## Gegenereerde configuratie

De gegenereerde JSON bevat dezelfde velden als `config.example.json`:

```json
{
  "source_root": "",
  "workspace_root": "",
  "index_path": "",
  "remote": {
    "endpoint": "",
    "region": "",
    "bucket": "",
    "prefix": "",
    "access_key_id_env": "MCP_S3_ACCESS_KEY_ID",
    "secret_access_key_env": "MCP_S3_SECRET_ACCESS_KEY"
  }
}
```

De paden en S3-locatiegegevens zijn leeg omdat de applicatie daarvoor geen veilige omgevingsonafhankelijke standaard kan kiezen. De environmentvariabelenamen hebben de bestaande standaardwaarden, zodat alleen de bijbehorende credentials in de procesomgeving hoeven te worden gezet. De optionele prefix is leeg.

## Componentgrens

De config-package krijgt één bootstrapfunctie die uitsluitend verantwoordelijk is voor veilig aanmaken wanneer het bestand ontbreekt. De bestaande `Load`-functie blijft verantwoordelijk voor lezen, unmarshallen en valideren.

Voorgestelde interface:

```go
func EnsureFile(path string) (created bool, err error)
```

`main.run` roept eerst `EnsureFile` aan:

- `created == true`: schrijf de gebruikersmelding naar stderr en retourneer `nil`;
- `created == false`: roep de bestaande `config.Load` aan;
- `err != nil`: retourneer de fout.

Deze scheiding houdt bootstrap en validatie afzonderlijk testbaar en voorkomt dat een nieuw, bewust onvolledig template direct als een validatiefout wordt behandeld.

## Veilige bestandscreatie

`EnsureFile` gebruikt `os.OpenFile` met `os.O_WRONLY|os.O_CREATE|os.O_EXCL` en permissie `0600`.

- `O_EXCL` voorkomt overschrijven wanneer een ander proces het bestand tussen controle en creatie aanmaakt.
- Bij `os.ErrExist` behandelt de functie het bestand als bestaand en retourneert zij `created == false`.
- Na schrijven wordt `Sync` en `Close` uitgevoerd.
- Als marshallen, schrijven, syncen of sluiten mislukt, wordt uitsluitend het door deze aanroep gedeeltelijk aangemaakte bestand opgeruimd en wordt de fout teruggegeven.
- Een vooraf bestaand bestand wordt onder geen enkele omstandigheid gewijzigd.

## Meldingen en exitstatus

Na succesvolle creatie schrijft de applicatie uitsluitend naar stderr:

```text
config.json is aangemaakt; vul het configuratiebestand in en start de applicatie opnieuw.
```

De applicatie retourneert daarna exitcode `0`. De MCP stdio-verbinding wordt niet gestart, zodat stdout leeg blijft.

Andere lees- of schrijffouten blijven fatale fouten en resulteren via de bestaande `main`-foutafhandeling in een niet-nul exitcode.

## Tests

Unit-tests voor `config.EnsureFile` bewijzen:

- een ontbrekend bestand wordt als ingesprongen geldige JSON met de exacte structuur aangemaakt;
- de standaardnamen voor beide credential-environment variables staan in het bestand;
- een bestaand bestand blijft byte-voor-byte ongewijzigd;
- een creatierace met `os.ErrExist` wordt als bestaand behandeld;
- een onschrijfbare bestemming levert een fout.

Een testbare opstartfunctie bewijst:

- een eerste start maakt de config, meldt dit via stderr en start de server niet;
- een tweede start probeert de bestaande config te laden en valideert hem;
- een andere openfout wordt teruggegeven;
- stdout blijft bij bootstrap leeg.

## Documentatie

De README vermeldt dat handmatig kopiëren van `config.example.json` niet meer nodig is. Bij de eerste start wordt `config.json` automatisch aangemaakt; de gebruiker vult het bestand in en start de executable opnieuw.

## Acceptatiecriteria

1. Een eerste start zonder `config.json` maakt exact één leesbaar configuratiebestand.
2. Het aangemaakte bestand bevat de volledige verwachte structuur.
3. De eerste start eindigt succesvol zonder MCP-server.
4. Bestaande configinhoud wordt nooit overschreven.
5. Andere I/O-fouten eindigen met een fout.
6. Alleen een bestaande, geldige configuratie leidt tot het starten van de MCP-server.
7. Geautomatiseerde tests en de Windows-releasebuild slagen.
