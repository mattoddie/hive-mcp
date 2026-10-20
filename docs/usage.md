# Using hive-mcp

Once the server is connected and logged in, just talk to your assistant about your home. This page shows what works well and explains how the tools behave, so you know what to expect.

- [Example questions](#example-questions)
- [Example conversations](#example-conversations)
- [Built-in prompts](#built-in-prompts)
- [Making changes](#making-changes)
- [Zones, names and IDs](#zones-names-and-ids)
- [Fresh and cached readings](#fresh-and-cached-readings)
- [The raw API tool](#the-raw-api-tool)
- [Tips for good results](#tips-for-good-results)

## Example questions

**How's the house?**

- *"How warm is the house?"* (`hive_get_overview`)
- *"Is the heating on right now?"*
- *"Is the back door shut?"*
- *"Do any of my Hive devices need new batteries?"* (`hive_list_devices`)

**Heating and hot water**

- *"What's the heating schedule on weekdays?"* (`hive_get_heating_schedule`)
- *"When does the hot water next come on?"* (`hive_get_hot_water`)
- *"Boost the heating to 21 for an hour."* (writes)
- *"Put the bedroom radiator on 18 and keep it there."* (writes)
- *"Turn the hot water on for half an hour."* (writes)

**Away and smart devices**

- *"We're away from the 20th to the 28th. Set holiday mode at 12 degrees, back on the 28th at lunchtime."* (writes)
- *"Is anything left on?"* (`hive_list_smart_devices`)
- *"Turn the living room light down to 30%."* (writes)

## Example conversations

These show the kind of back-and-forth to expect. Your assistant's wording will differ.

### Warming up

> **You:** *It's freezing in here. Can you warm the house up?*
>
> **Assistant:** *The living room is 17.5 °C and the heating is following its schedule, which has it at 16 °C until 17:00. I could boost it to 21 °C for an hour; afterwards it goes back to the schedule. Shall I?*
>
> **You:** *Yes please.*
>
> **Assistant:** *Done. The heating is boosting to 21 °C for 60 minutes, and the boiler's on.*

Behind the scenes the assistant used `hive_get_overview` (or `hive_get_heating`), then `hive_boost_heating`. The `warm_up` prompt asks for exactly this flow.

### A bath in half an hour

> **You:** */mcp__hive__hot_water_now 30*
>
> **Assistant:** *The hot water is off and isn't scheduled on again until 18:00, so I've boosted it for 30 minutes. It goes back to the schedule at 16:45.*

### Going away

> **You:** *We're off to Cornwall from Saturday morning, back on the 28th around 6pm.*
>
> **Assistant:** *I'd set holiday mode from Saturday 08:00 until the 28th at 16:00, holding 10 °C, so the house is warming up when you get back. The lamp plug is on; shall I switch it off too?*
>
> **You:** *Yes to both.*
>
> **Assistant:** *Holiday mode is set until 28 October 16:00 at 10 °C, and the lamp plug is off.*

### Batteries

> **You:** */mcp__hive__home_check*
>
> **Assistant:** *All good, with one thing to do. Heating: living room 20.4 °C (target 20, schedule), bedroom 18.1 °C (TRV, target 18). Hot water is off until 18:00. To do: replace the bedroom radiator valve's batteries (20%). The HomeShield keypad on your account is offline; Hive stopped supporting HomeShield in 2025, so you can remove it in the Hive app.*

## Built-in prompts

The server includes five prompts. Each one is a ready-made, multi-step request that tells the model which tools to use and how to report. Most clients show them as slash commands or in a prompt picker. In Claude Code they're `/mcp__hive__<name>`, for example `/mcp__hive__home_check`.

| Prompt | Arguments | What it does |
|---|---|---|
| `home_check` | | Temperatures, what the heating and hot water are doing, holiday mode, and a to-do list of low batteries, offline and discontinued devices. Doesn't change anything. |
| `warm_up` | `temperature`, `minutes`, `zone` (all optional) | Checks the zone, shows you the boost it would start, and starts it after you agree. |
| `hot_water_now` | `minutes` (optional, default 30) | Boosts the hot water unless it's already on or about to come on. |
| `going_away` | `leave` (optional), `return` | Suggests a holiday temperature and end time, and what to switch off, then sets it after you agree. |
| `schedule_review` | | Reviews the heating and hot water schedules for comfort and wasted energy, and suggests changes to make in the Hive app. |

The prompts that change things need writes enabled, and ask first.

## Making changes

With `HIVE_ALLOW_WRITES=true` the assistant can change your heating, hot water, holiday mode, plugs and lights. The server tells the model to confirm changes with you first, and good clients also ask before running any tool that isn't read-only. Every change comes back with what was sent and how the zone or device looks afterwards.

### Heating modes and boosts

A heating zone (a Hive thermostat) or TRV zone (Hive radiator valves) has one of these modes, as in the Hive app:

| Mode | What it does |
|---|---|
| `schedule` | Follows the weekly schedule. Setting a temperature in this mode lasts until the next scheduled change, like turning the thermostat dial. |
| `manual` | Holds one temperature until changed. |
| `off` | Heats only to protect against frost (usually 7 °C). Setting a temperature saves it but doesn't heat. |
| `boost` | Heats to a temperature for a set time (up to six hours), then goes back to the previous mode. |

- `hive_set_heating_temperature` sets the target; pass `mode: manual` to hold it.
- `hive_set_heating_mode` switches mode, with an optional temperature for manual.
- Manual mode lasts until something changes it, so switching to it, or changing the target of a zone that's already in manual, needs `confirm: true`. The model is told to pass it only once you've agreed.
- `hive_boost_heating` starts a boost (default 60 minutes); `hive_cancel_heating_boost` ends it early and restores the previous mode, and for manual or off, the previous temperature.

Temperatures are 5 °C up to `HIVE_MAX_TEMP` (25 °C unless you change it; Hive itself allows up to 32), in steps of 0.5, for every mode, boost and holiday mode. Anything else is refused before it reaches Hive. For a temporary change, prefer a boost: it can't be forgotten.

### Hot water

The hot water is `on` (stays on), `off`, on its `schedule`, or boosting for a set time. `hive_set_hot_water_mode` switches between the first three, and `hive_boost_hot_water` and `hive_cancel_hot_water_boost` handle boosts. Like manual heating, `on` needs `confirm: true`.

### Holiday mode

`hive_set_holiday_mode` holds the heating at one temperature from a start time (default now) to an end time, given as `YYYY-MM-DD` or `YYYY-MM-DDTHH:MM` in your home's local time. It can last at most 60 days. `hive_cancel_holiday_mode` ends it straight away.

### Plugs and lights

`hive_set_plug` switches a plug on or off. `hive_set_light` switches a light on or off and sets brightness (5–100%, rounded to the nearest 5) and, for tuneable and colour bulbs, colour temperature in kelvin. Setting a brightness or colour temperature switches the light on.

## Zones, names and IDs

Tools take a zone, plug or light by **name** ("Bedroom", "living room") or Hive's ID. Names match without regard to case, and a unique part of a name is enough ("living" finds "Living room"). If there's only one, you can leave it out. When a name is ambiguous or unknown, the error lists the choices.

## Fresh and cached readings

Reading the whole home is one request to Hive, and hive-mcp reuses the result for `HIVE_CACHE_SECONDS` (30 seconds by default), so a conversation's several tool calls don't each ask Hive. Every change clears the cache. Read tools take `fresh: true` when you need the very latest reading, and `raw: true` for Hive's full objects.

Hive itself only hears from devices every so often, so even a fresh reading can be a minute or two behind what's on the thermostat. `hive_list_devices` shows when each device was last seen.

## The raw API tool

`hive_api_request` (toolset `raw`) calls any path of Hive's API, such as `/nodes/all`, `/devices` or `/actions`. It's a fallback for things the other tools don't cover. Without `HIVE_ALLOW_WRITES` it only allows `GET`. With writes on it allows `POST`, `PUT` and `DELETE` too, with **no validation**, so it's marked destructive. It isn't part of `HIVE_TOOLSETS=all` (the default): add it by name, for example `all,raw`. The account's user profile (name, email, phone, address) is redacted from what it returns.

## Tips for good results

- **Name the room.** *"The bedroom"* is clearer than *"upstairs"* when there's more than one zone.
- **Say how long.** *"For an hour"* leads to a boost rather than a change that sticks.
- **Ask for a plan first.** For changes, *"What would you do? Don't do it yet."* then *"go ahead"*.
- **Fewer tools, better focus.** If the assistant picks the wrong tools, narrow `HIVE_TOOLSETS`.
- **Check on it.** Hive can take a minute to show a change; ask again with *"check again"* (which uses `fresh: true`) if something looks stale.
