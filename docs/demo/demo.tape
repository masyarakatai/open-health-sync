# The README demo: from an empty folder to REST, FHIR R4, Open mHealth and MCP.
# dark.tape and light.tape set the colors and the output file, then include this file.
# Render from the repository root: see docs/demo/README.md.

Require docker
Require curl
Require jq
Require openssl

Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 20
Set LineHeight 1.2
Set Width 1480
Set Height 980
Set Padding 30
Set Margin 22
Set BorderRadius 10
Set WindowBar Rings
Set Framerate 24
Set TypingSpeed 20ms
Set CursorBlink false
Set WaitPattern /\$\s*$/
Set WaitTimeout 180s

# Not recorded: a scratch folder holding a copy of the compose file, and a compose
# project of its own, so an install already running on this machine is not touched.
Hide
Type `export COMPOSE_PROJECT_NAME=ohs-demo PS1='\[\e[90m\]$\[\e[0m\] ' PS2='  '`
Enter
Type `cd "$(mktemp -d)" && cp -R "$OLDPWD/deploy" "$OLDPWD/.env.example" . && clear`
Enter
Wait
Show

# 1. Start the published image with Postgres and TimescaleDB, as in the README quickstart.
Type "cp .env.example .env"
Enter
Type `sed -i.bak "s|^OHS_ENCRYPTION_KEY=.*|OHS_ENCRYPTION_KEY=$(openssl rand -base64 32)|" .env`
Enter
Type "docker compose --env-file .env -f deploy/docker-compose.yml up -d --wait"
Enter
Sleep 1.5s
# The health checks take a few seconds: cut to the result.
Hide
Wait
Show
Sleep 2.5s
Hide
Type "clear"
Enter
Wait
Show

# 2. Ninety days of synthetic data from six overlapping devices.
Type "docker compose --env-file .env -f deploy/docker-compose.yml exec ohs /ohs seed --demo"
Enter
Wait
Sleep 4.5s

# Not recorded: export what the seed printed. The key is printed once only, so a
# second key of the same demo tenant stands in for it.
Hide
Type@5ms `export OHS_URL=http://localhost:8080 T=$(docker compose --env-file .env -f deploy/docker-compose.yml exec -T ohs /ohs admin tenant list | awk '$2 == "demo" {print $1}')`
Enter
Wait
Type@5ms `export OHS_API_KEY=$(docker compose --env-file .env -f deploy/docker-compose.yml exec -T ohs /ohs admin key create --tenant "$T" | grep -o 'ohs_sk_[A-Za-z0-9_-]*')`
Enter
Wait
Type@5ms `export USER_ID=$(curl -s -H "Authorization: Bearer $OHS_API_KEY" "$OHS_URL/v1/users" | jq -r '.data[0].id') && clear`
Enter
Wait
Show

# 3. One day of the merged view: each figure comes from the best ranked source.
Type "# Six devices, one daily summary. Each figure names the source it came from."
Enter
Type `H="Authorization: Bearer $OHS_API_KEY"`
Enter
Type `curl -s -H "$H" "$OHS_URL/v1/users/$USER_ID/daily?limit=1" \`
Enter
Type `  | jq '.data[0] | {date, step_count, resting_hr, hrv_avg_ms, sleep_score, sources}'`
Enter
Wait
Sleep 5s
Hide
Type "clear"
Enter
Wait
Show

# 4. One reading, three formats.
Type "# One heart rate reading, served as REST, FHIR R4 and Open mHealth."
Enter
Type `curl -s -H "$H" "$OHS_URL/v1/users/$USER_ID/samples?type=heart_rate&limit=1" \`
Enter
Type `  | jq -c '.data[0] | {type, value, unit, source}'`
Enter
Wait
Sleep 1.5s
Type `curl -s -H "$H" "$OHS_URL/v1/users/$USER_ID/fhir/Observation?type=heart_rate&limit=1" \`
Enter
Type `  | jq -c '.entry[0].resource | .code.coding[0], .valueQuantity'`
Enter
Wait
Sleep 2s
Type `curl -s -H "$H" "$OHS_URL/v1/users/$USER_ID/omh/data-points?schema=omh:heart-rate&limit=1" \`
Enter
Type `  | jq -c '.data[0] | .header.schema_id, .body.heart_rate'`
Enter
Wait
Sleep 4.5s
Hide
Type "clear"
Enter
Wait
Show

# 5. The MCP server on the same port, read only.
Type "# For AI agents: a read-only MCP server on the same port."
Enter
Type `curl -s -H "$H" -H "Content-Type: application/json" "$OHS_URL/mcp" \`
Enter
Type `  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq -r '.result.tools[].name'`
Enter
Wait
Sleep 5s

# Not recorded: remove the demo containers and volumes.
Hide
Type "docker compose --env-file .env -f deploy/docker-compose.yml down -v"
Enter
Wait
