# README demo

`demo-dark.gif` and `demo-light.gif` are terminal recordings of the [quickstart](../../README.md#quickstart),
made with [VHS](https://github.com/charmbracelet/vhs). Nothing in them is mocked: the commands are
the quickstart's, run against the image `deploy/docker-compose.yml` names
(`ghcr.io/soma-intelligence/open-health-sync:0.1.0`), and the data is the synthetic set of `ohs seed --demo`
([docs/demo.md](../demo.md)), one invented person seen through six devices.

What it shows, in about a minute:

1. `docker compose ... up -d --wait` starts `ohs` and Postgres with TimescaleDB.
2. `ohs seed --demo` writes 90 days from Oura, WHOOP, Withings, Fitbit, Polar and Apple Health.
3. `GET /v1/users/{id}/daily`: one day of the merged view, each figure with the source it came from.
4. The same heart rate reading from `/samples`, `/fhir/Observation` (LOINC `8867-4`, UCUM `/min`)
   and `/omh/data-points` (`omh:heart-rate`).
5. `tools/list` on the read-only MCP server at `/mcp`.

## Files

| File | Role |
|---|---|
| `demo.tape` | The commands, the timing and the terminal size. |
| `dark.tape`, `light.tape` | The colors and the output file of each variant, then `Source demo.tape`. |
| `demo-dark.gif`, `demo-light.gif` | The recordings. The README picks one with `prefers-color-scheme`. |

## Render it again

You need [VHS](https://github.com/charmbracelet/vhs#installation) (it brings `ttyd` and `ffmpeg`),
Docker with Compose v2.24 or later, `curl`, `jq`, `openssl` and the
[JetBrains Mono](https://www.jetbrains.com/lp/mono/) font. Port 8080 must be free. From the
repository root:

```sh
vhs docs/demo/dark.tape
vhs docs/demo/light.tape
```

Each run takes about three minutes and works in a temporary folder with a copy of `deploy/` and
`.env.example`, under the compose project `ohs-demo`, so your `.env` and an install already
running under the default project are not touched. It removes its containers and volumes at the
end (`down -v` on `ohs-demo` only). Run the two tapes one after the other, not at the same time.

Two steps are cut from the recording: the wait for the health checks after `up`, and the export
of `OHS_URL`, `OHS_API_KEY` and `USER_ID` after the seed (a second key of the demo tenant stands in
for the one the seed prints, since that one is shown only once). On Linux as root, set
`VHS_NO_SANDBOX=1` so VHS can start its browser.

After a change to the API or to the seed, render both variants and check that every command
still fits in the frame.
