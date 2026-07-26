# MCP-bestandsindex — ontwerp

## Doel

De applicatie is een zelfstandige MCP-server in Go voor het tijdelijk beheren van lokaal georganiseerde bestanden voordat ze naar S3-compatible cloudopslag worden gekopieerd. De computer fungeert als jump host. De server bewaart een persistente JSON-index, voorkomt dubbele opslag op basis van bestandstype en checksum, behoudt de door AI aangebrachte mappenstructuur en verwijdert lokale kopieën uitsluitend nadat de remote kopie is geverifieerd.

## Reikwijdte

De eerste versie:

- draait native op Windows;
- communiceert als MCP-server via stdio;
- ontvangt bij toevoegen alleen een absoluut bestandspad;
- gebruikt een JSON-configuratiebestand;
- gebruikt `rclone.exe` naast de gebouwde Go-executable;
- synchroniseert alle bestanden met status `OnFileNotRemote` in één aanroep;
- ondersteunt een S3-compatible remote met een optionele prefix;
- scant de beheerde werkmap alleen wanneer de JSON-index nog niet bestaat.

Een database, periodieke achtergrondscan, watcher en automatische synchronisatie na iedere toevoeging vallen buiten de reikwijdte.

## Bestandslocaties en configuratie

De applicatie zoekt standaard `config.json` en `rclone.exe` in dezelfde map als de eigen executable. De configuratie bevat:

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

`prefix` is optioneel en standaard leeg. Met prefix `archief` wordt bijvoorbeeld `KlantA/rapporten/juli.pdf` opgeslagen als `s3://mijn-bucket/archief/KlantA/rapporten/juli.pdf`.

S3-credentials worden via benoemde environment variables gelezen en komen niet als platte tekst in de configuratie, logs of MCP-resultaten. Voor iedere synchronisatie maakt de server een tijdelijk rclone-configbestand met beperkte bestandstoegang en verwijdert dit bestand na afloop.

## Architectuur

De server bestaat uit duidelijk begrensde onderdelen:

1. **Configuratie** leest en valideert paden en remote-instellingen.
2. **Padbeheer** normaliseert paden, bewaakt `source_root` en vertaalt absolute bronpaden naar relatieve paden.
3. **Metadata** bepaalt bestandsextensie en xxHash64-checksum.
4. **Indexopslag** leest, valideert en atomair schrijft de JSON-index.
5. **Bestandsservice** verzorgt toevoegen, kopiëren, duplicaatdetectie en initiële indexopbouw.
6. **Syncservice** stuurt rclone aan, verifieert remote bestanden, verwijdert lokale kopieën en verwerkt statusovergangen.
7. **MCP-adapter** publiceert de tools via stdio en vertaalt domeinresultaten naar MCP-responses.

De index en muterende bewerkingen worden met een mutex beschermd. `add_file` en `sync_files` kunnen daardoor niet gelijktijdig de index of bijbehorende bestanden wijzigen.

## Indexmodel

De JSON-index is de primaire waarheid en heeft een expliciete schemaversie:

```json
{
  "version": 1,
  "files": [
    {
      "relative_path": "KlantA/rapporten/juli.pdf",
      "file_name": "juli.pdf",
      "file_type": ".pdf",
      "checksum": "xxh64:0123456789abcdef",
      "status": "OnFileNotRemote"
    }
  ]
}
```

Toegestane statussen:

- `OnFileNotRemote`: de lokale beheerde kopie bestaat en is nog niet geverifieerd op de remote.
- `OnFileAndRemote`: de remote kopie is geverifieerd, maar de lokale kopie is nog aanwezig.
- `RemoteOnly`: de remote kopie is geverifieerd en de lokale kopie is verwijderd.

Een bestand wordt als reeds bekend beschouwd wanneer bestandstype en xxHash64 gelijk zijn aan een bestaande entry. Deze niet-cryptografische checksum is gekozen vanwege de hoge snelheid. De kleine theoretische kans op een hashbotsing wordt voor deze toepassing geaccepteerd.

## Initiële indexopbouw

Als de index bij het starten niet bestaat, scant de server `workspace_root` eenmaal recursief. Iedere gevonden reguliere file krijgt een entry met status `OnFileNotRemote`.

De scan sluit minimaal uit:

- het indexbestand;
- tijdelijke indexbestanden;
- tijdelijke kopieerbestanden;
- tijdelijke rclone-bestanden en bestandenlijsten.

Na een succesvolle scan wordt de volledige index atomair opgeslagen. Als een index wel bestaat maar niet leesbaar, ongeldig of incompatibel is, stopt de server met een duidelijke fout. Hij vervangt een corrupte index nooit stilzwijgend door een nieuwe scan.

## MCP-tool `add_file`

### Contract

Invoer:

```json
{
  "path": "C:\\AI-organisatie\\KlantA\\rapporten\\juli.pdf"
}
```

De tool accepteert uitsluitend een absoluut pad naar een bestaande reguliere file binnen `source_root`.

Uitvoer bevat minimaal:

- relatief pad;
- bestandstype;
- xxHash64;
- status;
- indicatie of de entry nieuw of al bekend was.

### Verwerking

1. Normaliseer en canonicaliseer het bronpad.
2. Controleer dat het pad binnen `source_root` ligt en naar een reguliere file verwijst.
3. Bepaal het relatieve pad vanaf `source_root`.
4. Bepaal bestandstype en xxHash64.
5. Zoek in de index naar dezelfde combinatie van bestandstype en checksum.
6. Als die combinatie bestaat, kopieer niets en geef de bestaande registratie terug.
7. Maak ontbrekende doelmappen onder `workspace_root` aan.
8. Weiger de bewerking als op het doelpad al andere inhoud staat.
9. Kopieer naar een tijdelijk bestand in dezelfde doelmap.
10. Bereken xxHash64 van de kopie en vergelijk die met de bron.
11. Hernoem de geverifieerde tijdelijke kopie atomair naar het definitieve doelpad.
12. Voeg de entry met `OnFileNotRemote` toe en schrijf de index onmiddellijk atomair.

Als het indexschrijven na het kopiëren mislukt, retourneert de tool een fout en probeert hij de zojuist gemaakte onbeheerde kopie veilig op te ruimen. Bestaande gebruikersbestanden worden daarbij nooit verwijderd.

## MCP-tool `sync_files`

### Contract

De tool heeft geen invoerparameters. Hij verwerkt alle entries met `OnFileNotRemote`. Daarnaast probeert hij achtergebleven lokale kopieën met status `OnFileAndRemote` opnieuw te verwijderen.

De uitvoer bevat:

- aantal geselecteerde bestanden;
- aantal geüploade bestanden;
- aantal geverifieerde bestanden;
- aantal verwijderde bestanden;
- aantal overgeslagen bestanden;
- aantal mislukte bestanden;
- een foutbeschrijving per mislukt bestand zonder credentials of andere geheimen.

### Overdracht en verificatie

De syncservice:

1. Maakt een tijdelijke `--files-from`-lijst van de relatieve paden met `OnFileNotRemote`.
2. Maakt een tijdelijk rclone-configbestand op basis van de gevalideerde S3-configuratie en environment variables.
3. Voert `rclone copy` uit met `workspace_root` als bron en `bucket/prefix` als bestemming.
4. Voert voor dezelfde selectie `rclone check --one-way` uit.
5. Parseert het resultaat per bestand en behandelt alleen bevestigde bestanden als remote aanwezig.

Rclone wordt rechtstreeks als proces gestart met afzonderlijke argumenten, niet via een shell. Daardoor worden paden niet als shellcode geïnterpreteerd. De relatieve structuur onder `source_root` en `workspace_root` blijft onder de S3-prefix intact.

### Statusovergangen en verwijdering

Voor ieder afzonderlijk geverifieerd bestand:

1. Wijzig de status naar `OnFileAndRemote` en schrijf de index onmiddellijk.
2. Verwijder de lokale kopie.
3. Ruim lege bovenliggende mappen op, maar nooit boven `workspace_root`.
4. Wijzig de status naar `RemoteOnly` en schrijf de index opnieuw onmiddellijk.

Als de overdracht of verificatie mislukt, blijft het bestand `OnFileNotRemote` en wordt de lokale kopie niet verwijderd. Als alleen de lokale verwijdering mislukt, blijft het bestand `OnFileAndRemote`. Een volgende `sync_files`-aanroep probeert de verwijdering opnieuw zonder een onnodige upload.

## Persistente opslag en crashgedrag

Iedere toevoeging en iedere statuswijziging wordt direct opgeslagen. Atomisch opslaan gebeurt door:

1. JSON volledig naar een tijdelijk bestand in dezelfde map te schrijven;
2. de inhoud naar schijf te flushen;
3. het tijdelijke bestand atomair over het vorige indexbestand te hernoemen.

Een crash kan daardoor hoogstens leiden tot opnieuw uitvoeren van een idempotente stap:

- een geüpload maar nog niet geregistreerd bestand wordt bij de volgende sync opnieuw gecontroleerd of gekopieerd;
- een geverifieerd bestand met `OnFileAndRemote` wordt bij de volgende sync opnieuw lokaal verwijderd;
- een `RemoteOnly`-bestand wordt niet opnieuw geüpload.

## Foutafhandeling en veiligheid

- Paden buiten `source_root`, directories, ontbrekende bestanden en ongeldige configuratie worden geweigerd.
- Een bestaand doelbestand met andere inhoud wordt nooit stil overschreven.
- Ontbrekende credentials, een ontbrekende `rclone.exe` of verbindingsproblemen wijzigen of verwijderen geen lokale bestanden.
- Tijdelijke bestanden worden via gegarandeerde cleanup-paden verwijderd, ook na fouten.
- Verwijdering en opruiming gebruiken gecontroleerde absolute paden onder `workspace_root`.
- Procesuitvoer wordt gefilterd voordat die in MCP-foutresultaten terechtkomt.
- Een corrupte index vereist handmatige correctie; automatische reconstructie gebeurt alleen wanneer de index werkelijk ontbreekt.

## Tests

Unit-tests dekken:

- padnormalisatie en bescherming tegen ontsnappen uit `source_root` of `workspace_root`;
- relatieve padberekening;
- xxHash64 en lokale kopieverificatie;
- duplicaatdetectie;
- alle geldige en ongeldige statusovergangen;
- initiële scan en uitsluitingen;
- atomisch indexschrijven en foutgedrag;
- configuratievalidatie en geheimenfiltering.

Integratietests gebruiken tijdelijke mappen en een lokale rclone-bestemming om te verifiëren:

- behoud van mappenstructuur;
- batchupload van alle `OnFileNotRemote`-entries;
- remote controle vóór lokale verwijdering;
- overgang via `OnFileAndRemote` naar `RemoteOnly`;
- herstel na mislukte upload, verificatie, indexwrite of lokale verwijdering;
- verwerking van gedeeltelijk geslaagde batches.

De rclone-proceslaag krijgt een kleine interface zodat foutscenario's deterministisch met een fake runner getest kunnen worden. Een optionele handmatige S3-smoketest kan met echte credentials worden uitgevoerd, maar maakt geen deel uit van de standaard testset.

## Acceptatiecriteria

De implementatie is gereed wanneer:

1. de server op Windows als MCP stdio-server start met een geldige configuratie;
2. een ontbrekende index exact eenmaal vanuit `workspace_root` wordt opgebouwd;
3. `add_file` de bronstructuur behoudt, de kopie verifieert en direct indexeert;
4. bekende inhoud niet opnieuw wordt gekopieerd;
5. `sync_files` alle `OnFileNotRemote`-bestanden naar de geconfigureerde S3-locatie stuurt;
6. geen lokale kopie vóór succesvolle rclone-verificatie wordt verwijderd;
7. geverifieerde lokale kopieën automatisch worden verwijderd en als `RemoteOnly` geregistreerd blijven;
8. iedere toevoeging en statuswijziging direct en atomair in JSON wordt opgeslagen;
9. fouten geen stil dataverlies, overschrijving of geheimenlek veroorzaken;
10. de geautomatiseerde tests op Windows slagen.
