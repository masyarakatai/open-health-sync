# COROS (research notes)

There is **no COROS connector**. COROS runs a partner API for established platforms and shares
its documentation only after registration; this page records what is public. Open Health Sync
has requested partner access (api@coros.com). What works today does not change: activity files
from the [COROS Training Hub export](activity-files.md), and COROS activities through
[intervals.icu](intervals-icu.md).

Research checked on **2026-10-20**. Statements no COROS page confirms are marked **(to
confirm)**; the sources are at the end.

## Access: who can get the API

| | |
|---|---|
| Program | [COROS Partner API](https://support.coros.com/hc/en-us/articles/53181766856724-Partner-API-Access): multi-user OAuth, webhooks, two-way sync, FIT downloads |
| Who can join | An established platform with a user base, a registered company with an authorized technical representative, security and privacy compliance, and agreement to the API Terms of Use |
| Can a developer join today | **Yes, by application.** COROS states it grants access through its standard OAuth 2.0 framework "to any platform that satisfies our standard security and operational requirements", framed around GDPR and the EU Data Act |
| Documentation | Not public. The full docs are shared after registration (independently reported by [Nango](https://nango.dev/docs/integrations/all/coros), which keeps no endpoint list for COROS for that reason) |
| Cost | No fee is stated publicly **(to confirm)**; the terms include system rate limits |

The [application process](https://support.coros.com/hc/en-us/articles/17085887816340-Submit-an-API-Application):

1. **Submit technical details** — company details, technical contacts and OAuth 2.0 redirect
   URIs, through the form on that page and by email to api@coros.com.
2. **Review and accept the terms** — the standard API Terms of Use, with the security, privacy
   and rate-limit conditions.
3. **Receive credentials** — after verification COROS issues a client id and secret.

Rate limit of the partner API: 1,000 calls per minute.

### Below that scale: the MCP

The [COROS MCP](https://support.coros.com/hc/en-us/articles/53181619102996-Build-on-COROS-MCP)
is self-service, no application: OAuth 2.0 login against `mcp.coros.com/mcp`, aimed at
individuals connecting their account to an AI tool (the
[Connect Your COROS to AI](https://support.coros.com/hc/en-us/articles/50841795180948-Connect-Your-COROS-to-AI)
guide covers ChatGPT and Claude, including building workouts and training plans that sync back
to the COROS library). It is a personal connection, not credentials a platform serves its own
users with; COROS points platforms that are not yet at scale at it in the meantime.

## Auth flow and webhooks

- **OAuth 2.0.** The partner API reference lists authorize, access token, refresh, deauthorize
  and bind state: a normal per-user authorization against the client id and secret issued at
  registration, with the redirect URIs fixed at application time.
- **Webhooks exist at the partner level.** A workout summary is pushed to the registered
  endpoint within about five minutes, with retry logic, authenticated by the client id and
  secret in the header. Whether daily health data or other types also push is part of the
  documentation shared after registration **(to confirm)**.

## Data types

- **Activities** — `getWorkoutRecords` (a 30-day maximum window, a 3-month lookback) and
  `getWorkoutDetailFit`, the workout as a FIT file download. Activities can also be uploaded
  the other way (`file/synchronous`, `file/upload`), and routes pushed as GPX or KML up to
  50 MB.
- **Daily health metrics** — `getDailyData`.
- **Fitness assessments** — listed for Coros in the [connector table](README.md); the endpoint
  behind them is in the private documentation **(to confirm)**.
- **Training plans and structured workouts** — pushed to COROS users (`tp/list/push`,
  `tp/workout/deleteById`): the write half of the two-way sync.
- `getUserInfo` identifies the connected account.

## How COROS data reaches Open Health Sync today

- **Activity files**: in the Training Hub, *Activity List*, *Export Data* saves FIT files the
  [activity file import](activity-files.md) stores, with the source slug `coros` from the file.
  No API access is needed.
- **intervals.icu**: activities COROS syncs to intervals.icu come through the
  [intervals.icu connector](intervals-icu.md), keeping `coros` as their upstream source.

## Revisit

If the partner access request is answered, or COROS publishes a public reference, open an issue
with the [connector request form](https://github.com/soma-intelligence/open-health-sync/issues/new/choose)
and recheck the sources below: the help center articles change without notice.

## Sources

- [Partner API Access](https://support.coros.com/hc/en-us/articles/53181766856724-Partner-API-Access)
  — requirements, what you get, the application steps and the API reference table (updated
  2026-09-03).
- [Submit an API Application](https://support.coros.com/hc/en-us/articles/17085887816340-Submit-an-API-Application)
  — the form, the GDPR and EU Data Act framing, who each path is for.
- [Build on COROS MCP](https://support.coros.com/hc/en-us/articles/53181619102996-Build-on-COROS-MCP)
  and [Connect Your COROS to AI](https://support.coros.com/hc/en-us/articles/50841795180948-Connect-Your-COROS-to-AI)
  — the self-service MCP and what it can do.
- [Nango's COROS notes](https://nango.dev/docs/integrations/all/coros) — third party: the API
  documentation is private.
- [run-garden's COROS research](https://github.com/kyranstar/run-garden/blob/main/docs/research/coros-official-api.md)
  — third party, checked 2026-08-01: `open.coros.com` serves nothing publicly and no public
  endpoint documentation was found on the COROS API hostnames.
