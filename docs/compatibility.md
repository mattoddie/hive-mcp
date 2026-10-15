# Compatibility

> [!IMPORTANT]
> hive-mcp is unofficial. It isn't made, endorsed or supported by Hive, British Gas or Centrica. Hive doesn't publish an API: hive-mcp uses the same undocumented API as the Hive app, following what the open-source [pyhiveapi](https://github.com/Pyhass/Pyhive) library (behind the Home Assistant Hive integration) does. Hive can change that API without notice, and when it does, hive-mcp may stop working until it's updated.

## Hive devices

hive-mcp works with what's on your Hive account. These are the products it knows about:

| Hive product | Shows up as | Read | Control |
|---|---|---|---|
| Hive Active Heating: thermostat (including Thermostat mini) with a boiler module or receiver, one or more zones | `heating` product; `thermostatui` and `boilermodule` devices | ✅ temperature, target, mode, boost, schedule, calling for heat, thermostat battery | ✅ target, schedule / manual / off, boost and cancel |
| Hive hot water control (on dual-channel receivers, such as SLR2) | `hotwater` product | ✅ on or off, mode, boost, schedule | ✅ on / off / schedule, boost and cancel |
| Hive Radiator Valve (TRV) | `trvcontrol` product; `trv` devices | ✅ zone temperature, target, mode, schedule; each valve's battery, calibration, mounting and child lock | ✅ target, schedule / manual / off, boost and cancel |
| Hive Active Plug | `activeplug` | ✅ on or off, power use | ✅ on and off |
| Hive Active Light Dimmable | `warmwhitelight` | ✅ on or off, brightness | ✅ on, off, brightness |
| Hive Active Light Cool to Warm White | `tuneablelight` | ✅ also colour temperature | ✅ also colour temperature |
| Hive Active Light Colour | `colourtuneablelight` | ✅ also colour mode, hue and saturation | ✅ on, off, brightness, white colour temperature (not colours yet) |
| Hive window or door sensor | `contactsensor` | ✅ open or closed, battery | |
| Hive motion sensor | `motionsensor` | ✅ motion, temperature, battery | |
| Hive hub | `hub` | ✅ online, signal, firmware | |
| Holiday mode | | ✅ | ✅ set and cancel |

Every device also reports whether it's online, its model and firmware, and its battery (for battery devices) through `hive_list_devices`. Anything else on the account is still visible through `hive_list_devices` and the raw API tool.

hive-mcp hasn't yet been tested against a live Hive account. The device support above follows pyhiveapi's code and the real (anonymised) `/nodes/all` response it ships as test data, and the tests run against a mock built in the same shape. Please [report](https://github.com/mattoddie/hive-mcp/issues/new/choose) anything that doesn't match your home.

### Discontinued Hive products

Hive has stopped supporting some products. hive-mcp reports them, flagged as discontinued, but doesn't try to control them:

| Product | Status |
|---|---|
| Hive View and Hive View Outdoor cameras | Support ended on 1 August 2025. |
| HomeShield (alarm keypad, siren and the security service) | Support ended on 1 August 2025. Keypads and sirens left on an account show as `keypad` and `siren` devices. |
| Hive Leak Sensor | Support ended on 1 September 2023. |

Window, door and motion sensors were also sold as part of HomeShield. They still appear in the API data that pyhiveapi uses, but whether Hive still updates them since HomeShield ended hasn't been confirmed. Treat their readings with care. Heating, hot water, radiator valves, plugs and lights are Hive's current products.

### Not covered

- **Changing schedules.** hive-mcp reads heating and hot water schedules but can't edit them yet. Use the Hive app.
- **Colour (hue and saturation) for colour bulbs**, Hive "actions" (automations), heat-on-demand, and frost protection settings. The raw API tool can reach them if you know the request.
- **Hive heat pumps, EV chargers and energy data**, which use other systems.
- **More than one home.** Hive accounts can have more than one home. hive-mcp reads what Hive returns by default, which is the primary home; switching between homes hasn't been worked out.

## How hive-mcp talks to Hive

| | Detail |
|---|---|
| Login | AWS Cognito (`cognito-idp.eu-west-1.amazonaws.com`) with SRP and SMS two-step verification, then Cognito's "remembered device" SRP for later logins. The Cognito pool and app client are read from Hive's sign-in page, `sso.hivehome.com`, as pyhiveapi does. |
| API | `https://beekeeper.hivehome.com/1.0`, with the Cognito ID token in the `Authorization` header. One `GET /nodes/all` returns the whole home; changes are `POST /nodes/<type>/<id>`; holiday mode is `/holiday-mode`. |
| Heating limits | 5 to 32 °C, in steps of 0.5, as the Hive app and pyhiveapi apply them. |
| Polling | One request reads the whole home, and it's cached for `HIVE_CACHE_SECONDS` (30 seconds by default). Changes clear the cache. |
| Rate limits | Hive doesn't publish any. hive-mcp retries a few times with increasing waits when it gets `429` or `503`, and waits five minutes after a failed automatic login. |

Older guides and versions of pyhiveapi used `beekeeper-uk.hivehome.com` and Hive's own `/cognito/login` endpoint. Current pyhiveapi uses `beekeeper.hivehome.com` and logs in directly with Cognito, and hive-mcp follows it. `HIVE_API_URL` can point elsewhere if Hive moves again. See [Advanced: Hive's endpoints](configuration.md#advanced-hives-endpoints).

## Running hive-mcp

| Component | Requirement |
|---|---|
| Docker image | `linux/amd64` or `linux/arm64` (for example Raspberry Pi 4/5) |
| Go (to build without Docker) | 1.25 or later |
| Operating system for the binary | Linux and macOS. Windows should work, but isn't tested. |
| Network | Outbound HTTPS to `beekeeper.hivehome.com`, `sso.hivehome.com` and `cognito-idp.eu-west-1.amazonaws.com` |
| MCP clients | Any client supporting Streamable HTTP or stdio. See [Connecting MCP clients](clients.md). |

The server is built on the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) and supports tools, prompts and tool annotations.

## Help improve this page

Found a device that doesn't work, a field that's missing, or a change in Hive's API? [Open an issue](https://github.com/mattoddie/hive-mcp/issues/new/choose) or start a [discussion](https://github.com/mattoddie/hive-mcp/discussions). Please remove your email address, tokens, device IDs and postcode first.
